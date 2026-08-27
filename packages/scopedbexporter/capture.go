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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

var (
	ErrCaptureInProgress = errors.New("capture already in progress")
	ErrNoCapturedData    = errors.New("capture completed without data")
)

// DefaultCaptureRegistry joins Telescope's embedded exporter with its
// operational capture endpoint.
var DefaultCaptureRegistry = NewCaptureRegistry()

type CapturedSample struct {
	Signal  string
	Records int
	Payload []byte
}

// CaptureRegistry retains telemetry only while a bounded capture request is
// active. The exporter hot path pays a single atomic load when it is idle.
type CaptureRegistry struct {
	active   atomic.Bool
	mu       sync.Mutex
	sessions map[string]*captureSession
}

type captureSession struct {
	signal  string
	limit   int
	records int
	done    chan struct{}
	logs    plog.Logs
	traces  ptrace.Traces
	metrics pmetric.Metrics
}

func NewCaptureRegistry() *CaptureRegistry {
	return &CaptureRegistry{sessions: make(map[string]*captureSession, 3)}
}

func (r *CaptureRegistry) Capture(
	ctx context.Context,
	signal string,
	limit int,
	timeout time.Duration,
) (CapturedSample, error) {
	if signal != signalLogs && signal != signalTraces && signal != signalMetrics {
		return CapturedSample{}, fmt.Errorf("unsupported capture signal %q", signal)
	}
	if limit <= 0 {
		return CapturedSample{}, errors.New("capture limit must be greater than zero")
	}
	if timeout <= 0 {
		return CapturedSample{}, errors.New("capture timeout must be greater than zero")
	}
	if r == nil {
		return CapturedSample{}, errors.New("capture registry is unavailable")
	}

	session := newCaptureSession(signal, limit)
	r.mu.Lock()
	if r.sessions[signal] != nil {
		r.mu.Unlock()
		return CapturedSample{}, fmt.Errorf("%w for %s", ErrCaptureInProgress, signal)
	}
	r.sessions[signal] = session
	r.active.Store(true)
	r.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	select {
	case <-session.done:
	case <-timer.C:
	case <-ctx.Done():
		waitErr = ctx.Err()
	}

	r.mu.Lock()
	r.finishLocked(session)
	records := session.records
	r.mu.Unlock()
	if waitErr != nil {
		return CapturedSample{}, waitErr
	}
	if records == 0 {
		return CapturedSample{}, fmt.Errorf("%w for %s", ErrNoCapturedData, signal)
	}

	payload, err := session.marshalJSON()
	if err != nil {
		return CapturedSample{}, fmt.Errorf("encode captured %s OTLP: %w", signal, err)
	}
	return CapturedSample{Signal: signal, Records: records, Payload: payload}, nil
}

func newCaptureSession(signal string, limit int) *captureSession {
	return &captureSession{
		signal:  signal,
		limit:   limit,
		done:    make(chan struct{}),
		logs:    plog.NewLogs(),
		traces:  ptrace.NewTraces(),
		metrics: pmetric.NewMetrics(),
	}
}

func (s *captureSession) marshalJSON() ([]byte, error) {
	switch s.signal {
	case signalLogs:
		return plogotlp.NewExportRequestFromLogs(s.logs).MarshalJSON()
	case signalTraces:
		return ptraceotlp.NewExportRequestFromTraces(s.traces).MarshalJSON()
	case signalMetrics:
		return pmetricotlp.NewExportRequestFromMetrics(s.metrics).MarshalJSON()
	default:
		return nil, fmt.Errorf("unsupported signal %q", s.signal)
	}
}

func (r *CaptureRegistry) ObserveLogs(logs plog.Logs) {
	r.observe(signalLogs, func(session *captureSession, remaining int) int {
		return copyLogsPrefix(logs, session.logs, remaining)
	})
}

func (r *CaptureRegistry) ObserveTraces(traces ptrace.Traces) {
	r.observe(signalTraces, func(session *captureSession, remaining int) int {
		return copyTracesPrefix(traces, session.traces, remaining)
	})
}

func (r *CaptureRegistry) ObserveMetrics(metrics pmetric.Metrics) {
	r.observe(signalMetrics, func(session *captureSession, remaining int) int {
		return copyMetricsPrefix(metrics, session.metrics, remaining)
	})
}

func (r *CaptureRegistry) observe(signal string, appendPrefix func(*captureSession, int) int) {
	if r == nil || !r.active.Load() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[signal]
	if session == nil {
		return
	}
	remaining := session.limit - session.records
	if remaining <= 0 {
		r.finishLocked(session)
		return
	}
	session.records += appendPrefix(session, remaining)
	if session.records >= session.limit {
		r.finishLocked(session)
	}
}

func (r *CaptureRegistry) finishLocked(session *captureSession) {
	if r.sessions[session.signal] != session {
		return
	}
	delete(r.sessions, session.signal)
	r.active.Store(len(r.sessions) != 0)
	close(session.done)
}

func copyLogsPrefix(source plog.Logs, destination plog.Logs, limit int) int {
	copied := 0
	resources := source.ResourceLogs()
	for resourceIndex := 0; resourceIndex < resources.Len() && copied < limit; resourceIndex++ {
		sourceResource := resources.At(resourceIndex)
		var destinationResource plog.ResourceLogs
		resourceAdded := false
		scopes := sourceResource.ScopeLogs()
		for scopeIndex := 0; scopeIndex < scopes.Len() && copied < limit; scopeIndex++ {
			sourceScope := scopes.At(scopeIndex)
			records := sourceScope.LogRecords()
			if records.Len() == 0 {
				continue
			}
			if !resourceAdded {
				destinationResource = destination.ResourceLogs().AppendEmpty()
				sourceResource.Resource().CopyTo(destinationResource.Resource())
				destinationResource.SetSchemaUrl(sourceResource.SchemaUrl())
				resourceAdded = true
			}
			destinationScope := destinationResource.ScopeLogs().AppendEmpty()
			sourceScope.Scope().CopyTo(destinationScope.Scope())
			destinationScope.SetSchemaUrl(sourceScope.SchemaUrl())
			for recordIndex := 0; recordIndex < records.Len() && copied < limit; recordIndex++ {
				records.At(recordIndex).CopyTo(destinationScope.LogRecords().AppendEmpty())
				copied++
			}
		}
	}
	return copied
}

func copyTracesPrefix(source ptrace.Traces, destination ptrace.Traces, limit int) int {
	copied := 0
	resources := source.ResourceSpans()
	for resourceIndex := 0; resourceIndex < resources.Len() && copied < limit; resourceIndex++ {
		sourceResource := resources.At(resourceIndex)
		var destinationResource ptrace.ResourceSpans
		resourceAdded := false
		scopes := sourceResource.ScopeSpans()
		for scopeIndex := 0; scopeIndex < scopes.Len() && copied < limit; scopeIndex++ {
			sourceScope := scopes.At(scopeIndex)
			spans := sourceScope.Spans()
			if spans.Len() == 0 {
				continue
			}
			if !resourceAdded {
				destinationResource = destination.ResourceSpans().AppendEmpty()
				sourceResource.Resource().CopyTo(destinationResource.Resource())
				destinationResource.SetSchemaUrl(sourceResource.SchemaUrl())
				resourceAdded = true
			}
			destinationScope := destinationResource.ScopeSpans().AppendEmpty()
			sourceScope.Scope().CopyTo(destinationScope.Scope())
			destinationScope.SetSchemaUrl(sourceScope.SchemaUrl())
			for spanIndex := 0; spanIndex < spans.Len() && copied < limit; spanIndex++ {
				spans.At(spanIndex).CopyTo(destinationScope.Spans().AppendEmpty())
				copied++
			}
		}
	}
	return copied
}

func copyMetricsPrefix(source pmetric.Metrics, destination pmetric.Metrics, limit int) int {
	copied := 0
	resources := source.ResourceMetrics()
	for resourceIndex := 0; resourceIndex < resources.Len() && copied < limit; resourceIndex++ {
		sourceResource := resources.At(resourceIndex)
		var destinationResource pmetric.ResourceMetrics
		resourceAdded := false
		scopes := sourceResource.ScopeMetrics()
		for scopeIndex := 0; scopeIndex < scopes.Len() && copied < limit; scopeIndex++ {
			sourceScope := scopes.At(scopeIndex)
			var destinationScope pmetric.ScopeMetrics
			scopeAdded := false
			metrics := sourceScope.Metrics()
			for metricIndex := 0; metricIndex < metrics.Len() && copied < limit; metricIndex++ {
				sourceMetric := metrics.At(metricIndex)
				if metricDataPointCount(sourceMetric) == 0 {
					continue
				}
				if !resourceAdded {
					destinationResource = destination.ResourceMetrics().AppendEmpty()
					sourceResource.Resource().CopyTo(destinationResource.Resource())
					destinationResource.SetSchemaUrl(sourceResource.SchemaUrl())
					resourceAdded = true
				}
				if !scopeAdded {
					destinationScope = destinationResource.ScopeMetrics().AppendEmpty()
					sourceScope.Scope().CopyTo(destinationScope.Scope())
					destinationScope.SetSchemaUrl(sourceScope.SchemaUrl())
					scopeAdded = true
				}
				destinationMetric := destinationScope.Metrics().AppendEmpty()
				copied += copyMetricPrefix(sourceMetric, destinationMetric, limit-copied)
			}
		}
	}
	return copied
}

func copyMetricPrefix(source pmetric.Metric, destination pmetric.Metric, limit int) int {
	destination.SetName(source.Name())
	destination.SetDescription(source.Description())
	destination.SetUnit(source.Unit())
	source.Metadata().CopyTo(destination.Metadata())

	switch source.Type() {
	case pmetric.MetricTypeGauge:
		return copyNumberDataPoints(source.Gauge().DataPoints(), destination.SetEmptyGauge().DataPoints(), limit)
	case pmetric.MetricTypeSum:
		sourceSum := source.Sum()
		destinationSum := destination.SetEmptySum()
		destinationSum.SetAggregationTemporality(sourceSum.AggregationTemporality())
		destinationSum.SetIsMonotonic(sourceSum.IsMonotonic())
		return copyNumberDataPoints(sourceSum.DataPoints(), destinationSum.DataPoints(), limit)
	case pmetric.MetricTypeHistogram:
		sourceHistogram := source.Histogram()
		destinationHistogram := destination.SetEmptyHistogram()
		destinationHistogram.SetAggregationTemporality(sourceHistogram.AggregationTemporality())
		return copyHistogramDataPoints(sourceHistogram.DataPoints(), destinationHistogram.DataPoints(), limit)
	case pmetric.MetricTypeExponentialHistogram:
		sourceHistogram := source.ExponentialHistogram()
		destinationHistogram := destination.SetEmptyExponentialHistogram()
		destinationHistogram.SetAggregationTemporality(sourceHistogram.AggregationTemporality())
		return copyExponentialHistogramDataPoints(sourceHistogram.DataPoints(), destinationHistogram.DataPoints(), limit)
	case pmetric.MetricTypeSummary:
		return copySummaryDataPoints(source.Summary().DataPoints(), destination.SetEmptySummary().DataPoints(), limit)
	default:
		return 0
	}
}

func copyNumberDataPoints(source pmetric.NumberDataPointSlice, destination pmetric.NumberDataPointSlice, limit int) int {
	copied := min(source.Len(), limit)
	for index := 0; index < copied; index++ {
		source.At(index).CopyTo(destination.AppendEmpty())
	}
	return copied
}

func copyHistogramDataPoints(source pmetric.HistogramDataPointSlice, destination pmetric.HistogramDataPointSlice, limit int) int {
	copied := min(source.Len(), limit)
	for index := 0; index < copied; index++ {
		source.At(index).CopyTo(destination.AppendEmpty())
	}
	return copied
}

func copyExponentialHistogramDataPoints(source pmetric.ExponentialHistogramDataPointSlice, destination pmetric.ExponentialHistogramDataPointSlice, limit int) int {
	copied := min(source.Len(), limit)
	for index := 0; index < copied; index++ {
		source.At(index).CopyTo(destination.AppendEmpty())
	}
	return copied
}

func copySummaryDataPoints(source pmetric.SummaryDataPointSlice, destination pmetric.SummaryDataPointSlice, limit int) int {
	copied := min(source.Len(), limit)
	for index := 0; index < copied; index++ {
		source.At(index).CopyTo(destination.AppendEmpty())
	}
	return copied
}
