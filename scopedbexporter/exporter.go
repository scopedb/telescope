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
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

type dbExporter struct {
	meter        metric.Meter
	exporterID   string
	stopping     atomic.Bool
	persistent   bool
	queueEnabled bool
	cfg          *Config
	client       *appendClient
	logger       *zap.Logger
	signal       string
	statuses     *StatusRegistry
}

func newDBExporter(cfg *Config, set exporter.Settings, signal string, statuses *StatusRegistry) (*dbExporter, error) {
	if _, enabled := cfg.signal(signal); !enabled {
		return nil, fmt.Errorf("%s signal is not configured for the scopedb exporter", signal)
	}
	client, err := newClient(cfg, set.Logger)
	if err != nil {
		return nil, err
	}
	if statuses == nil {
		statuses = NewStatusRegistry()
	}
	statuses.configure(signal, cfg)

	exp := &dbExporter{
		persistent:   cfg.SendingQueue.HasValue() && cfg.SendingQueue.Get().StorageID != nil,
		queueEnabled: cfg.SendingQueue.HasValue(),
		cfg:          cfg,
		client:       client,
		logger:       set.Logger.Named("scopedbexporter"),
		signal:       signal,
		statuses:     statuses,
	}
	if set.MeterProvider != nil {
		exp.meter = set.MeterProvider.Meter("github.com/scopedb/telescope/scopedbexporter")
	}
	exp.exporterID = set.ID.String()
	return exp, nil
}

func (e *dbExporter) start(_ context.Context, _ component.Host) error {
	if e.meter != nil {
		if err := e.registerDeliveryMetrics(); err != nil {
			return fmt.Errorf("register delivery metrics: %w", err)
		}
	}
	signalConfig, _ := e.cfg.signal(e.signal)
	e.logger.Info(
		"Starting scopedb exporter",
		zap.String("endpoint", e.cfg.Endpoint),
		zap.String("signal", e.signal),
		zap.String("table", signalConfig.Table),
		zap.String("compression", e.cfg.compressionMode()),
	)

	// Destination checks belong to `telescope validate`. Runtime admission must
	// not depend on the downstream service being available during a restart.
	e.statuses.update(e.signal, func(status *SignalRuntimeStatus) { status.Ready = true })
	return nil
}

func (e *dbExporter) shutdown(_ context.Context) error {
	e.statuses.markStopped(e.signal)
	e.client.Close()
	s := e.statuses.Snapshot().Signals[e.signal]
	log := e.logger.Info
	if s.UnknownRecords+s.PermanentFailedRecords+s.RetryExhaustedRecords+s.EnqueueFailedRecords > 0 {
		log = e.logger.Warn
	}
	log("ScopeDB delivery summary",
		zap.String("signal", e.signal), zap.Uint64("written_records", s.ConfirmedWrittenRecords),
		zap.Uint64("unknown_records", s.UnknownRecords), zap.Uint64("permanent_rejected_records", s.PermanentFailedRecords),
		zap.Uint64("retry_exhausted_records", s.RetryExhaustedRecords), zap.Uint64("enqueue_failed_records", s.EnqueueFailedRecords),
		zap.Bool("persistent_queue", e.persistent))
	return nil
}

func (e *dbExporter) recordUnknown(records int, started time.Time, err error) {
	e.statuses.recordUnknown(e.signal, records, started, err)
	if e.logger != nil {
		e.logger.Warn("ScopeDB append outcome is unknown; these records will not be retried",
			zap.String("signal", e.signal), zap.Int("unknown_records", records), zap.Error(err))
	}
}

func pushSignal[T any](
	request *deliveryRequest,
	ctx context.Context,
	data T,
	records int,
	mapData func(T) (*ingestPayload, error),
	wrapSubset func(error, []int) error,
) error {
	exporter := request.exporter
	signal := exporter.signal
	started := time.Now().UTC()
	attemptCtx := ctx
	if timeout := exporter.cfg.Timeout.Timeout; timeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	payload, err := mapData(data)
	if err != nil {
		permanentErr := consumererror.NewPermanent(err)
		exporter.statuses.recordWrite(signal, records, started, permanentErr, true)
		return wrapSubset(permanentErr, recordIndexes(0, records))
	}
	outcome := exporter.client.send(ctx, attemptCtx, signal, payload, &request.replay)
	if len(outcome.committed) > 0 {
		exporter.statuses.recordWrite(signal, len(outcome.committed), started, nil, false)
		exporter.statuses.recordProbeSuccess(signal, probeIDsFromRecords(payload, outcome.committed))
	}
	if len(outcome.rejected) > 0 {
		for _, failure := range outcome.rejected {
			exporter.statuses.recordWrite(
				signal,
				1,
				started,
				consumererror.NewPermanent(failure.err),
				true,
			)
		}
		if exporter.logger != nil {
			exporter.logger.Warn(
				"Dropped invalid records while preserving the rest of the batch",
				zap.String("signal", signal),
				zap.Int("rejected_records", len(outcome.rejected)),
				zap.Error(outcome.rejected[0].err),
			)
		}
	}
	if len(outcome.unknown) > 0 {
		exporter.recordUnknown(len(outcome.unknown), started, outcome.unknown[0].err)
	}
	if outcome.err == nil {
		if len(outcome.unknown) > 0 {
			indexes := make([]int, 0, len(outcome.unknown))
			for _, failure := range outcome.unknown {
				indexes = append(indexes, failure.index)
			}
			return wrapSubset(consumererror.NewPermanent(outcome.unknown[0].err), indexes)
		}
		return nil
	}
	err = wrapSubset(outcome.err, outcome.uncommitted)
	exporter.statuses.recordWrite(signal, len(outcome.uncommitted), started, err, consumererror.IsPermanent(err))
	return err
}
