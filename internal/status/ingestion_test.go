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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

type fakeExporterStatusReader struct {
	snapshot scopedbexporter.StatusSnapshot
}

func (r fakeExporterStatusReader) Snapshot() scopedbexporter.StatusSnapshot {
	return r.snapshot
}

type fakeCollectorMetricsReader struct {
	snapshot collectorMetricsSnapshot
	err      error
	endpoint string
}

func (r fakeCollectorMetricsReader) Read(context.Context) (collectorMetricsSnapshot, error) {
	return r.snapshot, r.err
}

func (r fakeCollectorMetricsReader) Endpoint() string {
	return r.endpoint
}

func TestGetIngestionStatus(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	service := newService("test")
	service.now = func() time.Time { return now }
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"logs": {
				Signal:                 "logs",
				Ready:                  true,
				DestinationVerified:    true,
				Table:                  "scopedb.otel.logs",
				QueueEnabled:           true,
				QueueCapacity:          5000,
				QueueUnit:              "bytes",
				LastWriteAttempt:       now.Add(-time.Second),
				LastWriteSuccess:       now.Add(-time.Second),
				LastProbeIDs:           []string{"probe-1", "probe-2"},
				LastProbeSuccess:       now.Add(-time.Second),
				PermanentFailedRecords: 2,
				PermanentExportRecords: 1,
			},
			"traces": {
				Signal:              "traces",
				Ready:               true,
				DestinationVerified: true,
				Table:               "scopedb.otel.traces",
				QueueEnabled:        true,
				QueueCapacity:       5000,
			},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{
		endpoint: "http://collector:8888/metrics",
		snapshot: collectorMetricsSnapshot{Signals: map[string]collectorSignalMetrics{
			"logs":   {Received: 10, Written: 10, ExportFailed: 3, EnqueueFailed: 1, QueueCapacity: 5000},
			"traces": {Received: 4, QueueSize: 2, QueueCapacity: 5000},
		}},
	}

	server := newServer(service)
	request := httptest.NewRequest(http.MethodGet, "/v1/ingestion/status", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response IngestionStatusResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "ready", response.State)
	assert.Equal(t, now, response.GeneratedAt)
	assert.True(t, response.InternalTelemetry.Available)
	require.Len(t, response.Signals, 2)
	assert.Equal(t, "ready", response.Signals[0].State)
	assert.True(t, response.Signals[0].DestinationVerified)
	assert.Equal(t, []string{"probe-1", "probe-2"}, response.Signals[0].LastProbeIDs)
	assert.Equal(t, uint64(10), response.Signals[0].Written)
	assert.Equal(t, uint64(5), response.Signals[0].Dropped)
	assert.Equal(t, uint64(2), response.Signals[0].RetryExhausted)
	assert.Equal(t, uint64(2), response.Signals[0].PermanentRejected)
	assert.Equal(t, "ready", response.Signals[1].State)
	assert.True(t, response.Signals[1].Queue.Observed)
	assert.Equal(t, int64(2), response.Signals[1].Queue.Size)
	assert.Equal(t, int64(5000), response.Signals[1].Queue.Capacity)
	assert.Equal(t, "bytes", response.Signals[0].Queue.Unit)
	assert.Empty(t, response.Signals[0].InvalidItemsByReason)
}

func TestIngestionStatusReportsUnavailableInternalTelemetry(t *testing.T) {
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"logs": {Signal: "logs", Ready: true, DestinationVerified: true},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{
		endpoint: "http://collector:8888/metrics",
		err:      errors.New("connection refused"),
	}

	response := service.IngestionStatus(context.Background())

	assert.Equal(t, "degraded", response.State)
	assert.False(t, response.InternalTelemetry.Available)
	assert.Equal(t, "connection refused", response.InternalTelemetry.Error)
	assert.Equal(t, "degraded", response.Signals[0].State)
}

func TestReadinessDoesNotDependOnScopeDBAvailability(t *testing.T) {
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"traces": {
				Signal:              "traces",
				Ready:               true,
				DestinationVerified: false,
				LastError:           "ScopeDB is temporarily unavailable",
			},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{snapshot: collectorMetricsSnapshot{
		Signals: map[string]collectorSignalMetrics{"traces": {}},
	}}

	server := newServer(service)
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response HealthResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "ready", response.Status)
}

func TestPrometheusCollectorMetricsReader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`
# TYPE otelcol_receiver_accepted_log_records counter
otelcol_receiver_accepted_log_records{receiver="otlp",transport="grpc"} 4
otelcol_receiver_accepted_log_records{receiver="otlp/custom",transport="http"} 6
otelcol_receiver_accepted_log_records{receiver="other"} 100
# TYPE otelcol_receiver_refused_log_records counter
otelcol_receiver_refused_log_records{receiver="otlp"} 2
# TYPE otelcol_exporter_sent_log_records counter
otelcol_exporter_sent_log_records{exporter="scopedb"} 8
otelcol_exporter_sent_log_records{exporter="debug"} 100
# TYPE otelcol_exporter_send_failed_log_records counter
otelcol_exporter_send_failed_log_records{exporter="scopedb"} 3
# TYPE otelcol_exporter_enqueue_failed_log_records counter
otelcol_exporter_enqueue_failed_log_records{exporter="scopedb"} 1
# TYPE otelcol_exporter_queue_size gauge
otelcol_exporter_queue_size{exporter="scopedb",data_type="logs"} 5
otelcol_exporter_queue_size{exporter="scopedb",data_type="traces"} 7
# TYPE otelcol_exporter_queue_capacity gauge
otelcol_exporter_queue_capacity{exporter="scopedb",data_type="logs"} 5000
`))
	}))
	defer server.Close()
	reader := &prometheusCollectorMetricsReader{url: server.URL, client: server.Client()}

	snapshot, err := reader.Read(context.Background())

	require.NoError(t, err)
	logs := snapshot.Signals["logs"]
	assert.Equal(t, uint64(10), logs.Received)
	assert.Equal(t, uint64(2), logs.ReceiverRefused)
	assert.Equal(t, uint64(8), logs.Written)
	assert.Equal(t, uint64(3), logs.ExportFailed)
	assert.Equal(t, uint64(1), logs.EnqueueFailed)
	assert.Equal(t, int64(5), logs.QueueSize)
	assert.Equal(t, int64(5000), logs.QueueCapacity)
	assert.Equal(t, int64(7), snapshot.Signals["traces"].QueueSize)
}

func TestSignalDropCountsSeparatesFinalFailureReasons(t *testing.T) {
	runtime := scopedbexporter.SignalRuntimeStatus{
		PermanentFailedRecords: 5,
		PermanentExportRecords: 3,
	}
	metrics := collectorSignalMetrics{ExportFailed: 7, EnqueueFailed: 1}

	dropped, retryExhausted := signalDropCounts(runtime, metrics)

	assert.Equal(t, uint64(10), dropped)
	assert.Equal(t, uint64(4), retryExhausted)
}

func TestSignalIngestionState(t *testing.T) {
	runtime := scopedbexporter.SignalRuntimeStatus{Signal: "logs", Ready: true, DestinationVerified: true, QueueEnabled: true}
	tests := []struct {
		name      string
		runtime   scopedbexporter.SignalRuntimeStatus
		metrics   collectorSignalMetrics
		available bool
		want      string
	}{
		{name: "idle", runtime: runtime, available: true, want: "ready"},
		{name: "queued", runtime: runtime, metrics: collectorSignalMetrics{Received: 1, QueueSize: 1}, available: true, want: "ready"},
		{name: "full", runtime: runtime, metrics: collectorSignalMetrics{Received: 1, QueueSize: 5, QueueCapacity: 5}, available: true, want: "refusing"},
		{name: "destination unverified", runtime: scopedbexporter.SignalRuntimeStatus{Signal: "logs", Ready: true}, available: true, want: "degraded"},
		{name: "telemetry unavailable", runtime: runtime, available: false, want: "degraded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, signalIngestionState(tt.runtime, tt.metrics, tt.available))
		})
	}
}
