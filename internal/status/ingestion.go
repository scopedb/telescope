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

package status

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/scopedb/telescope/scopedbexporter"
)

const defaultInternalMetricsURL = "http://127.0.0.1:8888/metrics"

var ingestionSignals = [...]string{"logs", "traces", "metrics"}

type exporterStatusReader interface {
	Snapshot() scopedbexporter.StatusSnapshot
}

type collectorMetricsReader interface {
	Read(context.Context) (collectorMetricsSnapshot, error)
	Endpoint() string
}

type collectorMetricsSnapshot struct {
	Signals map[string]collectorSignalMetrics
}

type collectorSignalMetrics struct {
	Received        uint64
	ReceiverFailed  uint64
	ReceiverRefused uint64
	QueueSize       int64
	QueueCapacity   int64
}

type prometheusCollectorMetricsReader struct {
	url    string
	client *http.Client
}

func newPrometheusCollectorMetricsReader() *prometheusCollectorMetricsReader {
	url := strings.TrimSpace(os.Getenv("TELESCOPE_INTERNAL_METRICS_URL"))
	if url == "" {
		url = defaultInternalMetricsURL
	}
	return &prometheusCollectorMetricsReader{
		url:    url,
		client: &http.Client{Timeout: 2 * time.Second},
	}
}

func (r *prometheusCollectorMetricsReader) Endpoint() string {
	if r == nil {
		return ""
	}
	return r.url
}

func (r *prometheusCollectorMetricsReader) Read(ctx context.Context) (collectorMetricsSnapshot, error) {
	snapshot := collectorMetricsSnapshot{Signals: make(map[string]collectorSignalMetrics, len(ingestionSignals))}
	if r == nil || r.client == nil || strings.TrimSpace(r.url) == "" {
		return snapshot, fmt.Errorf("collector internal metrics endpoint is not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return snapshot, fmt.Errorf("build collector metrics request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return snapshot, fmt.Errorf("read collector internal metrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return snapshot, fmt.Errorf("read collector internal metrics: unexpected status %s", resp.Status)
	}

	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return snapshot, fmt.Errorf("parse collector internal metrics: %w", err)
	}

	for signal, unit := range map[string]string{"logs": "log_records", "traces": "spans", "metrics": "metric_points"} {
		snapshot.Signals[signal] = collectorSignalMetrics{
			Received:        uint64(metricValue(families, "otelcol_receiver_accepted_"+unit, receiverComponent)),
			ReceiverFailed:  uint64(metricValue(families, "otelcol_receiver_failed_"+unit, receiverComponent)),
			ReceiverRefused: uint64(metricValue(families, "otelcol_receiver_refused_"+unit, receiverComponent)),
			QueueSize:       int64(metricValue(families, "otelcol_exporter_queue_size", queueForSignal(signal))),
			QueueCapacity:   int64(metricValue(families, "otelcol_exporter_queue_capacity", queueForSignal(signal))),
		}
	}
	return snapshot, nil
}

type metricFilter func(*dto.Metric) bool

func receiverComponent(metric *dto.Metric) bool {
	return componentLabelMatches(metric, "receiver", "otlp")
}

func scopeDBExporterComponent(metric *dto.Metric) bool {
	return componentLabelMatches(metric, "exporter", "scopedb")
}

func queueForSignal(signal string) metricFilter {
	return func(metric *dto.Metric) bool {
		return scopeDBExporterComponent(metric) && labelValue(metric, "data_type") == signal
	}
}

func componentLabelMatches(metric *dto.Metric, label string, component string) bool {
	value := labelValue(metric, label)
	return value == component || strings.HasPrefix(value, component+"/")
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func metricValue(families map[string]*dto.MetricFamily, name string, filter metricFilter) float64 {
	family := families[name]
	if family == nil {
		family = families[name+"_total"]
	}
	if family == nil {
		return 0
	}
	var value float64
	for _, metric := range family.GetMetric() {
		if filter != nil && !filter(metric) {
			continue
		}
		switch {
		case metric.Counter != nil:
			value += metric.GetCounter().GetValue()
		case metric.Gauge != nil:
			value += metric.GetGauge().GetValue()
		case metric.Untyped != nil:
			value += metric.GetUntyped().GetValue()
		}
	}
	return max(0, value)
}

func (s *service) IngestionStatus(ctx context.Context) IngestionStatusResponse {
	runtime := s.ingestionRuntime.Snapshot()
	metrics, metricsErr := s.ingestionMetrics.Read(ctx)
	metricsAvailable := metricsErr == nil
	var storageBytes int64
	var storageErr error
	if s.queueStorageType != "memory" {
		storageBytes, storageErr = s.queueStorage.AllocatedBytes()
	}
	response := IngestionStatusResponse{
		Version:      s.version,
		ConfigDigest: s.configDigest,
		GeneratedAt:  s.now(),
		Listeners:    s.listeners,
		InternalTelemetry: IngestionInternalTelemetry{
			Available: metricsAvailable,
			Endpoint:  s.ingestionMetrics.Endpoint(),
		},
		QueueStorage: IngestionQueueStorage{
			Type:           s.queueStorageType,
			Available:      storageErr == nil && s.queueStorageType != "memory",
			AllocatedBytes: storageBytes,
		},
		Signals: make([]IngestionSignalStatus, 0, len(runtime.Signals)),
	}
	if metricsErr != nil {
		response.InternalTelemetry.Error = metricsErr.Error()
	}
	if storageErr != nil {
		response.QueueStorage.Error = storageErr.Error()
	}

	for _, signal := range configuredSignalNames(runtime) {
		runtimeStatus := runtime.Signals[signal]
		metricStatus := metrics.Signals[signal]
		queueCapacity := runtimeStatus.QueueCapacity
		if metricsAvailable && metricStatus.QueueCapacity > 0 {
			queueCapacity = metricStatus.QueueCapacity
		}
		signalStatus := IngestionSignalStatus{
			Signal:                 signal,
			Ready:                  runtimeStatus.Ready,
			DestinationVerified:    runtimeStatus.DestinationVerified,
			Table:                  runtimeStatus.Table,
			Received:               metricStatus.Received,
			ReceiverFailed:         metricStatus.ReceiverFailed,
			ReceiverRefused:        metricStatus.ReceiverRefused,
			Written:                runtimeStatus.ConfirmedWrittenRecords,
			Unknown:                runtimeStatus.UnknownRecords,
			UnknownReplayAttempts:  runtimeStatus.UnknownReplayAttempts,
			UnknownReplayedRecords: runtimeStatus.UnknownReplayedRecords,
			Dropped:                runtimeStatus.RetryExhaustedRecords + runtimeStatus.EnqueueFailedRecords + runtimeStatus.PermanentFailedRecords,
			RetryExhausted:         runtimeStatus.RetryExhaustedRecords,
			EnqueueFailed:          runtimeStatus.EnqueueFailedRecords,
			PermanentRejected:      runtimeStatus.PermanentFailedRecords,
			InvalidItemsByReason:   runtimeStatus.InvalidItemsByReason,
			Queue: IngestionQueueStatus{
				Enabled:  runtimeStatus.QueueEnabled,
				Observed: metricsAvailable,
				Size:     metricStatus.QueueSize,
				Capacity: queueCapacity,
				Unit:     runtimeStatus.QueueUnit,
			},
			LastError:    runtimeStatus.LastError,
			LastProbeIDs: runtimeStatus.LastProbeIDs,
		}
		if signalStatus.InvalidItemsByReason == nil {
			signalStatus.InvalidItemsByReason = map[string]uint64{}
		}
		if !runtimeStatus.LastWriteAttempt.IsZero() {
			signalStatus.LastWriteAttempt = &runtimeStatus.LastWriteAttempt
			signalStatus.LastWriteDurationMS = runtimeStatus.LastWriteDuration.Milliseconds()
		}
		if !runtimeStatus.LastWriteSuccess.IsZero() {
			signalStatus.LastWriteSuccess = &runtimeStatus.LastWriteSuccess
		}
		if !runtimeStatus.LastWriteFailure.IsZero() {
			signalStatus.LastWriteFailure = &runtimeStatus.LastWriteFailure
		}
		if !runtimeStatus.LastProbeSuccess.IsZero() {
			signalStatus.LastProbeSuccess = &runtimeStatus.LastProbeSuccess
		}
		signalStatus.State = signalIngestionState(runtimeStatus, metricStatus, metricsAvailable)
		response.Signals = append(response.Signals, signalStatus)
	}
	response.State = overallIngestionState(response.Signals)
	return response
}

func configuredSignalNames(snapshot scopedbexporter.StatusSnapshot) []string {
	names := make([]string, 0, len(snapshot.Signals))
	for _, signal := range ingestionSignals {
		if _, ok := snapshot.Signals[signal]; ok {
			names = append(names, signal)
		}
	}
	return names
}

func listenerAddress(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func signalIngestionState(runtime scopedbexporter.SignalRuntimeStatus, metrics collectorSignalMetrics, metricsAvailable bool) string {
	if runtime.Signal == "" {
		return "starting"
	}
	if !runtime.Ready {
		if runtime.LastError != "" {
			return "degraded"
		}
		return "starting"
	}
	if !metricsAvailable {
		return "degraded"
	}
	if runtime.QueueEnabled && metrics.QueueCapacity > 0 && metrics.QueueSize >= metrics.QueueCapacity {
		return "refusing"
	}
	if runtime.LastWriteFailure.After(runtime.LastWriteSuccess) {
		return "degraded"
	}
	if !runtime.DestinationVerified {
		return "degraded"
	}
	return "ready"
}

func overallIngestionState(signals []IngestionSignalStatus) string {
	priority := map[string]int{
		"starting": 0,
		"ready":    1,
		"degraded": 2,
		"refusing": 3,
	}
	state := "starting"
	for _, signal := range signals {
		if priority[signal.State] > priority[state] {
			state = signal.State
		}
	}
	return state
}
