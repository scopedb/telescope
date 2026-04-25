/*
 * Copyright 2026 ScopeDB contributors
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
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScopeDBRowsProjectsTimestampColumns(t *testing.T) {
	payload := &IngestPayload{
		SchemaVersion: "v1",
		Signal:        signalLogs,
		Env:           "demo",
		Records: []Record{
			{
				"timestamp_unix_nano":          "1713835425123456789",
				"observed_timestamp_unix_nano": "1713835426123456789",
				"severity_number":              int64(17),
				"resource": map[string]any{
					"service.name":        "collector-a",
					"service.version":     "1.2.3",
					"service.instance.id": "collector-a-1",
					"k8s.pod.name":        "collector-a-pod",
					"k8s.namespace.name":  "payments",
					"k8s.cluster.name":    "prod-us-east",
					"container.name":      "collector",
					"host.ip":             []any{"10.0.0.10", "127.0.0.1"},
					"host.name":           "collector-a-node",
				},
			},
			{
				"start_time_unix_nano": "1713835427123456789",
				"end_time_unix_nano":   "1713835428123456789",
			},
			{
				"start_timestamp_unix_nano": "1713835429123456789",
			},
		},
	}

	rows := payload.scopeDBRows()
	if assert.Len(t, rows, 3) {
		assert.NotEmpty(t, rows[0]["row_id"])
		assert.Len(t, rows[0]["row_id"], 16)
		assert.Len(t, rows[1]["row_id"], 16)
		assert.NotEqual(t, rows[0]["row_id"], rows[1]["row_id"])
		assert.Equal(t, rows[0]["row_id"].(string)[:8], rows[1]["row_id"].(string)[:8])
		assert.Equal(t, "2024-04-23T01:23:45.123456789Z", rows[0]["record_timestamp"])
		assert.Equal(t, "2024-04-23T01:23:46.123456789Z", rows[0]["observed_timestamp"])
		assert.Equal(t, int64(17), rows[0]["severity_number"])
		assert.Equal(t, "collector-a", rows[0]["service"])
		assert.Equal(t, "1.2.3", rows[0]["version"])
		assert.Equal(t, "collector-a-1", rows[0]["instance_id"])
		assert.Equal(t, "collector-a-pod", rows[0]["k8s_pod"])
		assert.Equal(t, "payments", rows[0]["k8s_namespace"])
		assert.Equal(t, "prod-us-east", rows[0]["k8s_cluster"])
		assert.Equal(t, "collector", rows[0]["container_name"])
		assert.Equal(t, "10.0.0.10", rows[0]["host_ip"])
		assert.Equal(t, "collector-a-node", rows[0]["host"])
		assert.Equal(t, "2024-04-23T01:23:47.123456789Z", rows[1]["start_timestamp"])
		assert.Equal(t, "2024-04-23T01:23:48.123456789Z", rows[1]["end_timestamp"])
		assert.Equal(t, "2024-04-23T01:23:49.123456789Z", rows[2]["start_timestamp"])
	}
}

func TestDeriveRowIDEncodesIngestIDAndOrdinal(t *testing.T) {
	rowID := deriveRowID(0x01020304, 0x0000000a)

	assert.Equal(t, "010203040000000a", rowID)
	_, err := hex.DecodeString(rowID)
	assert.NoError(t, err)
}

func TestUnixNanoStringToRFC3339(t *testing.T) {
	assert.Equal(t, "2024-04-23T01:23:45.123456789Z", unixNanoStringToRFC3339("1713835425123456789"))
	assert.Equal(t, "", unixNanoStringToRFC3339(""))
	assert.Equal(t, "", unixNanoStringToRFC3339("not-a-number"))
}

func TestScopeDBRowsProjectsTraceSchemaColumns(t *testing.T) {
	payload := &IngestPayload{
		SchemaVersion: "v1",
		Signal:        signalTraces,
		Env:           "demo",
		Records: []Record{
			{
				"name":                 "GET /checkout",
				"kind":                 "server",
				"status_code":          "error",
				"duration_ns":          int64(1000),
				"start_time_unix_nano": "1713835425123456789",
				"end_time_unix_nano":   "1713835426123456789",
			},
		},
	}

	rows := payload.scopeDBRows()
	if assert.Len(t, rows, 1) {
		assert.Equal(t, "GET /checkout", rows[0]["span_name"])
		assert.Equal(t, "server", rows[0]["span_kind"])
		assert.Equal(t, "error", rows[0]["status_code"])
		assert.Equal(t, int64(1000), rows[0]["duration_ns"])
	}
}

func TestScopeDBRowsProjectsMetricSchemaColumns(t *testing.T) {
	payload := &IngestPayload{
		SchemaVersion: "v1",
		Signal:        signalMetrics,
		Env:           "demo",
		Records: []Record{
			{
				"metric_name":               "cpu.utilization",
				"type":                      "gauge",
				"unit":                      "1",
				"temporality":               "delta",
				"number_value":              0.75,
				"timestamp_unix_nano":       "1713835425123456789",
				"start_timestamp_unix_nano": "1713835424123456789",
			},
		},
	}

	rows := payload.scopeDBRows()
	if assert.Len(t, rows, 1) {
		assert.Equal(t, "gauge", rows[0]["metric_type"])
		assert.Equal(t, "1", rows[0]["unit"])
		assert.Equal(t, "delta", rows[0]["temporality"])
		assert.Equal(t, 0.75, rows[0]["number_value"])
	}
}

func TestScopeDBRowsProjectsLogMessage(t *testing.T) {
	payload := &IngestPayload{
		SchemaVersion: "v1",
		Signal:        signalLogs,
		Env:           "demo",
		Records: []Record{
			{
				"message": "hello world",
				"scope":   map[string]any{"name": "go"},
				"attributes": map[string]any{
					"exception.type":    "PaymentTimeoutError",
					"exception.message": "payment provider timed out",
				},
			},
		},
	}

	rows := payload.scopeDBRows()
	if assert.Len(t, rows, 1) {
		assert.Equal(t, "hello world", rows[0]["message"])
		assert.Equal(t, "go", rows[0]["source"])
		assert.Equal(t, "PaymentTimeoutError", rows[0]["exception_type"])
		assert.Equal(t, "payment provider timed out", rows[0]["exception_message"])
	}
}

func TestScopeDBRowsProjectsTraceAttributeFacets(t *testing.T) {
	payload := &IngestPayload{
		SchemaVersion: "v1",
		Signal:        signalTraces,
		Env:           "demo",
		Records: []Record{
			{
				"name": "GET /checkout",
				"attributes": map[string]any{
					"http.request.method":       "GET",
					"http.response.status_code": int64(504),
					"url.path":                  "/checkout",
					"http.route":                "/checkout/{cart_id}",
					"peer.service":              "payments",
					"db.system.name":            "postgresql",
					"db.operation.name":         "SELECT",
					"rpc.method":                "Charge",
					"error.type":                "PaymentTimeoutError",
				},
			},
		},
	}

	rows := payload.scopeDBRows()
	if assert.Len(t, rows, 1) {
		assert.Equal(t, "GET", rows[0]["http_method"])
		assert.Equal(t, int64(504), rows[0]["http_status_code"])
		assert.Equal(t, "/checkout", rows[0]["url_path"])
		assert.Equal(t, "/checkout/{cart_id}", rows[0]["http_route"])
		assert.Equal(t, "payments", rows[0]["peer_service"])
		assert.Equal(t, "postgresql", rows[0]["db_system"])
		assert.Equal(t, "SELECT", rows[0]["db_operation"])
		assert.Equal(t, "Charge", rows[0]["rpc_method"])
		assert.Equal(t, "PaymentTimeoutError", rows[0]["error_type"])
	}
}

func TestScopeDBRowsProjectsMetricDistribution(t *testing.T) {
	payload := &IngestPayload{
		SchemaVersion: "v1",
		Signal:        signalMetrics,
		Env:           "demo",
		Records: []Record{
			{
				"metric_name": "request.duration",
				"type":        "histogram",
				"distribution": map[string]any{
					"count": uint64(3),
				},
			},
		},
	}

	rows := payload.scopeDBRows()
	if assert.Len(t, rows, 1) {
		assert.Equal(t, map[string]any{"count": uint64(3)}, rows[0]["distribution"])
	}
}
