/*
 * Copyright 2026 ScopeDB, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package scopedbexporter

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exporterhelper/xexporterhelper"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
	pdatareq "go.opentelemetry.io/collector/pdata/xpdata/request"
	"go.uber.org/zap"
)

// deliveryRequest tracks remaining items until Collector finishes its retries.
// Settled batches are counted as they complete; request completion accounts for
// any remaining ambiguous batch and records whose retry budget was exhausted.
// The persistent representation remains the original OTLP protobuf request.
type deliveryRequest struct {
	original any
	err      error
	once     sync.Once
	exporter *dbExporter
	delivery *appendDelivery
}

func newDeliveryRequest(data any, exporter *dbExporter) *deliveryRequest {
	return &deliveryRequest{original: data, exporter: exporter}
}

func (r *deliveryRequest) ItemsCount() int {
	if r.delivery != nil {
		return r.delivery.remaining()
	}
	switch data := r.original.(type) {
	case plog.Logs:
		return data.LogRecordCount()
	case ptrace.Traces:
		return data.SpanCount()
	case pmetric.Metrics:
		return data.DataPointCount()
	default:
		panic("unsupported telemetry request")
	}
}

func (r *deliveryRequest) BytesSize() int {
	switch data := r.original.(type) {
	case plog.Logs:
		return (&plog.ProtoMarshaler{}).LogsSize(data)
	case ptrace.Traces:
		return (&ptrace.ProtoMarshaler{}).TracesSize(data)
	case pmetric.Metrics:
		return (&pmetric.ProtoMarshaler{}).MetricsSize(data)
	default:
		panic("unsupported telemetry request")
	}
}

// The delivery cursor retains the pending batch; retrying must not rebuild a
// smaller OTLP request or remap bytes that may already have been written.
func (r *deliveryRequest) OnError(error) xexporterhelper.Request { return r }

func (r *deliveryRequest) send(ctx context.Context) error {
	if r.delivery == nil {
		var payload *ingestPayload
		var err error
		switch data := r.original.(type) {
		case plog.Logs:
			payload, err = mapLogs(data)
		case ptrace.Traces:
			payload, err = mapTraces(data)
		case pmetric.Metrics:
			payload, err = mapMetrics(data)
		}
		if err == nil {
			r.delivery, err = r.exporter.client.newDelivery(r.exporter.signal, payload, r.exporter.cfg.AmbiguousRetry)
		}
		if err != nil {
			r.err = consumererror.NewPermanent(err)
			r.exporter.statuses.recordWrite(r.exporter.signal, r.ItemsCount(), time.Now(), r.err, true)
			return r.err
		}
	}
	if pending := r.delivery.pending; pending != nil && r.exporter.stopping.Load() {
		r.err = pending.err
		return r.err
	}
	started := time.Now()
	result := r.delivery.send(ctx)
	r.exporter.recordDelivery(r.delivery.payload, result, started)
	r.err = result.err
	return r.err
}

func (r *deliveryRequest) finish() {
	r.once.Do(func() {
		if r.err == nil || consumererror.IsPermanent(r.err) {
			return
		}
		remaining := r.ItemsCount()
		if r.exporter.persistent && r.exporter.stopping.Load() {
			r.exporter.logger.Warn("ScopeDB delivery interrupted; original request retained in file queue",
				zap.String("signal", r.exporter.signal), zap.Int("remaining_records", remaining))
			return
		}
		if r.delivery != nil && r.delivery.pending != nil && r.delivery.pending.ambiguous {
			pending := r.delivery.pending
			r.exporter.recordAmbiguous(len(pending.indexes), time.Now(), pending.err)
			remaining -= len(pending.indexes)
		}
		r.exporter.statuses.recordRetryExhausted(r.exporter.signal, remaining)
	})
}

type deliveryEncoding struct{ exporter *dbExporter }

func (e deliveryEncoding) Marshal(ctx context.Context, request xexporterhelper.Request) ([]byte, error) {
	switch data := request.(*deliveryRequest).original.(type) {
	case plog.Logs:
		return pdatareq.MarshalLogs(ctx, data)
	case ptrace.Traces:
		return pdatareq.MarshalTraces(ctx, data)
	case pmetric.Metrics:
		return pdatareq.MarshalMetrics(ctx, data)
	default:
		panic("unsupported telemetry request")
	}
}

func (e deliveryEncoding) Unmarshal(body []byte) (context.Context, xexporterhelper.Request, error) {
	var data any
	var ctx context.Context
	var err error
	switch e.exporter.signal {
	case signalLogs:
		ctx, data, err = pdatareq.UnmarshalLogs(body)
		if errors.Is(err, pdatareq.ErrInvalidFormat) {
			ctx = context.Background()
			data, err = (&plog.ProtoUnmarshaler{}).UnmarshalLogs(body)
		}
	case signalTraces:
		ctx, data, err = pdatareq.UnmarshalTraces(body)
		if errors.Is(err, pdatareq.ErrInvalidFormat) {
			ctx = context.Background()
			data, err = (&ptrace.ProtoUnmarshaler{}).UnmarshalTraces(body)
		}
	case signalMetrics:
		ctx, data, err = pdatareq.UnmarshalMetrics(body)
		if errors.Is(err, pdatareq.ErrInvalidFormat) {
			ctx = context.Background()
			data, err = (&pmetric.ProtoUnmarshaler{}).UnmarshalMetrics(body)
		}
	}
	if err != nil {
		return context.Background(), nil, err
	}
	return ctx, newDeliveryRequest(data, e.exporter), nil
}

// Collector calls Unref after the queue consumer completes, including when its
// retry budget expires outside the exporter pusher. Queue batching is disabled;
// the upstream batch processor owns batching before these requests are enqueued.
type deliveryReferenceCounter struct{}

func (deliveryReferenceCounter) Ref(request xexporterhelper.Request) {
	switch data := request.(*deliveryRequest).original.(type) {
	case plog.Logs:
		pref.RefLogs(data)
	case ptrace.Traces:
		pref.RefTraces(data)
	case pmetric.Metrics:
		pref.RefMetrics(data)
	}
}

func (deliveryReferenceCounter) Unref(request xexporterhelper.Request) {
	r := request.(*deliveryRequest)
	r.finish()
	switch data := r.original.(type) {
	case plog.Logs:
		pref.UnrefLogs(data)
	case ptrace.Traces:
		pref.UnrefTraces(data)
	case pmetric.Metrics:
		pref.UnrefMetrics(data)
	}
}

type deliveryContextKey struct{}

func requestFromData(ctx context.Context, data any, exporter *dbExporter) xexporterhelper.Request {
	r := newDeliveryRequest(data, exporter)
	if result, ok := ctx.Value(deliveryContextKey{}).(**deliveryRequest); ok {
		*result = r
	}
	return r
}

// Without a queue the caller owns completion. With a queue, its reference
// counter owns completion after asynchronous export or persisted replay.
func consumeWithDelivery(ctx context.Context, exp *dbExporter, consume func(context.Context) error) error {
	var request *deliveryRequest
	err := consume(context.WithValue(ctx, deliveryContextKey{}, &request))
	if exp.queueEnabled && err != nil && request != nil {
		exp.statuses.update(exp.signal, func(s *SignalRuntimeStatus) { s.EnqueueFailedRecords += uint64(request.ItemsCount()) })
	}
	if !exp.queueEnabled && request != nil {
		request.finish()
	}
	return err
}

func (r *deliveryRequest) MergeSplit(context.Context, int, exporterhelper.RequestSizerType, xexporterhelper.Request) ([]xexporterhelper.Request, error) {
	return nil, errors.New("sending_queue.batch is not supported; use the batch processor")
}
