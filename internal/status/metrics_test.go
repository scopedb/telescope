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
	"github.com/prometheus/common/model"
	"github.com/scopedb/telescope/scopedbexporter"
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
				UnknownReplayAttempts:   2,
				UnknownReplayedRecords:  1,
				PermanentFailedRecords:  2,
				RetryExhaustedRecords:   2,
				EnqueueFailedRecords:    1,
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
				QueueUnit:     "items",
			},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{snapshot: collectorMetricsSnapshot{
		Signals: map[string]collectorSignalMetrics{
			"logs":   {Received: 10, QueueSize: 5, QueueCapacity: 5000},
			"traces": {Received: 4, QueueSize: 2, QueueCapacity: 5000},
		},
	}}
	service.queueStorage = fakeQueueStorageReader{allocatedBytes: 8192}

	recorder := httptest.NewRecorder()
	newServer(service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, string(expfmt.NewFormat(expfmt.TypeTextPlain)), recorder.Header().Get("Content-Type"))
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(recorder.Body)
	require.NoError(t, err)
	values := map[string]float64{}
	for name, family := range families {
		for _, metric := range family.Metric {
			signal, reason := labelValue(metric, "signal"), labelValue(metric, "reason")
			if signal != "" && name != "telescope_ingestion_invalid_items_total" {
				require.Equal(t, "scopedb.otel."+signal, labelValue(metric, "table"))
			}
			values[name+"/"+signal+"/"+reason] = metric.GetCounter().GetValue() + metric.GetGauge().GetValue()
		}
	}
	for key, want := range map[string]float64{
		"telescope_ingestion_received_total/logs/":                             10,
		"telescope_ingestion_written_total/logs/":                              7,
		"telescope_ingestion_unknown_replay_attempts_total/logs/":              2,
		"telescope_ingestion_unknown_replayed_records_total/logs/":             1,
		"telescope_ingestion_dropped_total/logs/retry_exhausted":               2,
		"telescope_ingestion_dropped_total/logs/enqueue_failed":                1,
		"telescope_ingestion_dropped_total/logs/permanent_rejected":            2,
		"telescope_ingestion_invalid_items_total/logs/unsupported_metric_type": 2,
		"telescope_ingestion_queue_bytes/logs/":                                5,
		"telescope_ingestion_queue_capacity_bytes/logs/":                       5000,
		"telescope_ingestion_queue_utilization/traces/":                        0.0004,
		"telescope_ingestion_last_success_timestamp_seconds/logs/":             123,
		"telescope_queue_storage_allocated_bytes//":                            8192,
	} {
		assert.Equal(t, want, values[key], key)
	}
	assert.NotContains(t, values, "telescope_ingestion_queue_bytes/traces/")
	assert.NotContains(t, values, "telescope_ingestion_queue_capacity_bytes/traces/")
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
