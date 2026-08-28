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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPrometheusMetrics(t *testing.T) {
	lastSuccess := time.Unix(123, 0).UTC()
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"logs": {
				Signal:                  "logs",
				Ready:                   true,
				DestinationVerified:     true,
				Table:                   "scopedb.otel.logs",
				QueueEnabled:            true,
				QueueCapacity:           5000,
				QueueUnit:               "bytes",
				LastWriteSuccess:        lastSuccess,
				ConfirmedWrittenRecords: 7,
				PermanentFailedRecords:  2,
				PermanentExportRecords:  1,
				InvalidItemsByReason: map[string]uint64{
					"unsupported_number_value_type": 1,
					"unsupported_metric_type":       2,
				},
			},
			"traces": {
				Signal:        "traces",
				Ready:         true,
				Table:         "scopedb.otel.traces",
				QueueEnabled:  true,
				QueueCapacity: 5000,
				QueueUnit:     "bytes",
			},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{snapshot: collectorMetricsSnapshot{
		Signals: map[string]collectorSignalMetrics{
			"logs":   {Received: 10, ExportFailed: 3, EnqueueFailed: 1, QueueSize: 5, QueueCapacity: 5000},
			"traces": {Received: 4, QueueSize: 2, QueueCapacity: 5000},
		},
	}}
	service.queueStorage = fakeQueueStorageReader{allocatedBytes: 8192}

	recorder := httptest.NewRecorder()
	newServer(service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, string(expfmt.NewFormat(expfmt.TypeTextPlain)), recorder.Header().Get("Content-Type"))
	assert.Equal(t, `# HELP telescope_ingestion_received_total Telemetry items accepted by Telescope, in each signal's native unit.
# TYPE telescope_ingestion_received_total counter
telescope_ingestion_received_total{signal="logs",table="scopedb.otel.logs"} 10
telescope_ingestion_received_total{signal="traces",table="scopedb.otel.traces"} 4
# HELP telescope_ingestion_written_total Telemetry items confirmed written to ScopeDB, in each signal's native unit.
# TYPE telescope_ingestion_written_total counter
telescope_ingestion_written_total{signal="logs",table="scopedb.otel.logs"} 7
telescope_ingestion_written_total{signal="traces",table="scopedb.otel.traces"} 0
# HELP telescope_ingestion_dropped_total Telemetry items ultimately dropped, partitioned by final reason.
# TYPE telescope_ingestion_dropped_total counter
telescope_ingestion_dropped_total{signal="logs",table="scopedb.otel.logs",reason="retry_exhausted"} 2
telescope_ingestion_dropped_total{signal="logs",table="scopedb.otel.logs",reason="enqueue_failed"} 1
telescope_ingestion_dropped_total{signal="logs",table="scopedb.otel.logs",reason="permanent_rejected"} 2
telescope_ingestion_dropped_total{signal="traces",table="scopedb.otel.traces",reason="retry_exhausted"} 0
telescope_ingestion_dropped_total{signal="traces",table="scopedb.otel.traces",reason="enqueue_failed"} 0
telescope_ingestion_dropped_total{signal="traces",table="scopedb.otel.traces",reason="permanent_rejected"} 0
# HELP telescope_ingestion_invalid_items_total Invalid OpenTelemetry items rejected locally before ScopeDB append, partitioned by reason.
# TYPE telescope_ingestion_invalid_items_total counter
telescope_ingestion_invalid_items_total{signal="logs",reason="unsupported_metric_type"} 2
telescope_ingestion_invalid_items_total{signal="logs",reason="unsupported_number_value_type"} 1
# HELP telescope_ingestion_queue_bytes Logical serialized bytes currently retained in the exporter queue.
# TYPE telescope_ingestion_queue_bytes gauge
telescope_ingestion_queue_bytes{signal="logs",table="scopedb.otel.logs"} 5
telescope_ingestion_queue_bytes{signal="traces",table="scopedb.otel.traces"} 2
# HELP telescope_ingestion_queue_capacity_bytes Configured logical byte capacity of the exporter queue.
# TYPE telescope_ingestion_queue_capacity_bytes gauge
telescope_ingestion_queue_capacity_bytes{signal="logs",table="scopedb.otel.logs"} 5000
telescope_ingestion_queue_capacity_bytes{signal="traces",table="scopedb.otel.traces"} 5000
# HELP telescope_ingestion_last_success_timestamp_seconds Unix timestamp of the latest ScopeDB append success, or zero before the first success.
# TYPE telescope_ingestion_last_success_timestamp_seconds gauge
telescope_ingestion_last_success_timestamp_seconds{signal="logs",table="scopedb.otel.logs"} 123
telescope_ingestion_last_success_timestamp_seconds{signal="traces",table="scopedb.otel.traces"} 0
# HELP telescope_ingestion_destination_verified Whether the ScopeDB destination has been verified by validation or a successful append.
# TYPE telescope_ingestion_destination_verified gauge
telescope_ingestion_destination_verified{signal="logs",table="scopedb.otel.logs"} 1
telescope_ingestion_destination_verified{signal="traces",table="scopedb.otel.traces"} 0
# HELP telescope_queue_storage_allocated_bytes Filesystem blocks currently allocated to files in the Telescope queue directory.
# TYPE telescope_queue_storage_allocated_bytes gauge
telescope_queue_storage_allocated_bytes 8192
`, recorder.Body.String())
}

func TestPrometheusMetricsOmitUnavailableQueueStorage(t *testing.T) {
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"logs": {Signal: "logs", Ready: true, Table: "logs"},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{}
	service.queueStorage = fakeQueueStorageReader{err: assert.AnError}

	recorder := httptest.NewRecorder()
	newServer(service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "telescope_queue_storage_allocated_bytes")
}

func TestPrometheusMetricsFailWhenInternalTelemetryIsUnavailable(t *testing.T) {
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"logs": {Signal: "logs", Ready: true, Table: "logs"},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{err: assert.AnError}
	service.queueStorage = fakeQueueStorageReader{allocatedBytes: 8192}

	recorder := httptest.NewRecorder()
	newServer(service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "telescope_ingestion_received_total")
}
