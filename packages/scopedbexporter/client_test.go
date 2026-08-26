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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	scopedb "github.com/scopedb/goscopedb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exportertest"
)

func TestClientSendAppendsMappedNDJSON(t *testing.T) {
	var received []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/databases/scopedb/schemas/public/tables/vendor_otel_logs_test/rows", r.URL.Path)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		assert.Equal(t, "zstd", r.Header.Get("Content-Encoding"))
		body, err := decodeSDKRequestBody(r)
		require.NoError(t, err)
		for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
			var row map[string]any
			require.NoError(t, json.Unmarshal(line, &row))
			received = append(received, row)
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(scopedb.AppendRowsResult{
			AppendState:     scopedb.AppendStateCommitted,
			NumRowsInserted: int64(len(received)),
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = map[string]string{
		"message": "log.message",
		"service": `resource.attributes["service.name"]`,
	}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	err = client.Send(context.Background(), signalLogs, &IngestPayload{Records: []Record{{
		"message":    "hello",
		"trace_id":   "not-selected",
		"attributes": map[string]any{"order.id": "not-selected"},
		"resource":   map[string]any{"service.name": "checkout", "host.name": "not-selected"},
	}}})
	require.NoError(t, err)
	require.Len(t, received, 1)
	assert.Equal(t, map[string]any{"message": "hello", "service": "checkout"}, received[0])
}

func TestClientSendUsesConfiguredGzipCompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
		body, err := decodeSDKRequestBody(r)
		require.NoError(t, err)
		assert.JSONEq(t, `{"message":"hello"}`, strings.TrimSpace(string(body)))
		writeAppendCommitted(t, w, 1)
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Compression = "gzip"
	cfg.Mappings.Logs = map[string]string{"message": "log.message"}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	require.NoError(t, client.Send(context.Background(), signalLogs, &IngestPayload{
		Records: []Record{{"message": "hello"}},
	}))
}

func TestClientSendRoutesAllSignals(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = map[string]string{"message": "log.message"}
	cfg.Mappings.Traces = map[string]string{"name": "span.name"}
	cfg.Mappings.Metrics = map[string]string{"name": "metric.name"}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	var tableNames []string
	var rows []map[string]any
	client.appendFn = func(_ context.Context, table *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		tableNames = append(tableNames, table.Name)
		var row map[string]any
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &row))
		rows = append(rows, row)
		return scopedb.AppendRowsResult{AppendState: scopedb.AppendStateCommitted, NumRowsInserted: 1}, nil
	}

	require.NoError(t, client.Send(context.Background(), signalLogs, &IngestPayload{Records: []Record{{"message": "log"}}}))
	require.NoError(t, client.Send(context.Background(), signalTraces, &IngestPayload{Records: []Record{{"name": "span"}}}))
	require.NoError(t, client.Send(context.Background(), signalMetrics, &IngestPayload{Records: []Record{{"metric_name": "metric"}}}))

	assert.Equal(t, []string{"vendor_otel_logs_test", "vendor_otel_traces_test", "vendor_otel_metrics_test"}, tableNames)
	assert.Equal(t, []map[string]any{{"message": "log"}, {"name": "span"}, {"name": "metric"}}, rows)
}

func TestClientValidateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/databases/scopedb/schemas/public/tables/vendor_otel_logs_test", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"database":     "scopedb",
			"schema":       "public",
			"name":         "vendor_otel_logs_test",
			"columns":      []map[string]any{{"name": "message", "data_type": "string"}},
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct_on":  map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = map[string]string{"message": "log.message"}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	require.NoError(t, client.ValidateDestination(context.Background(), signalLogs))
	cfg.Mappings.Logs["service"] = `resource.attributes["service.name"]`
	client, err = NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()
	err = client.ValidateDestination(context.Background(), signalLogs)
	require.Error(t, err)
	assert.ErrorContains(t, err, "missing mapped columns: service")
}

func TestClientValidateDestinationRejectsKnownTypeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"database": "scopedb",
			"schema":   "public",
			"name":     "vendor_otel_logs_test",
			"columns": []map[string]any{
				{"name": "severity", "data_type": "string"},
				{"name": "tenant", "data_type": "string"},
			},
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct_on":  map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = map[string]string{
		"severity": "log.severity_number",
		"tenant":   `resource.attributes["tenant.id"]`,
	}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	err = client.ValidateDestination(context.Background(), signalLogs)
	require.Error(t, err)
	assert.ErrorContains(t, err, "severity (log.severity_number produces int, table has string)")
	assert.NotContains(t, err.Error(), "tenant")
}

func TestClientSendReportsRetryFromFailedChunk(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = map[string]string{"body": "log.body"}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	attempt := 0
	client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		attempt++
		if attempt == 2 {
			return scopedb.AppendRowsResult{}, &scopedb.Error{
				Kind:      scopedb.ErrorKindAppendRowsFailed,
				Message:   "temporary",
				Retryable: true,
				AppendDetails: &scopedb.AppendErrorDetails{
					AppendState: scopedb.AppendStateRejected,
				},
			}
		}
		return scopedb.AppendRowsResult{
			AppendState:     scopedb.AppendStateCommitted,
			NumRowsInserted: int64(bytes.Count(body, []byte{'\n'})),
		}, nil
	}

	err = client.Send(context.Background(), signalLogs, &IngestPayload{Records: []Record{
		{"body": strings.Repeat("a", 5*1024*1024)},
		{"body": strings.Repeat("b", 5*1024*1024)},
	}})
	require.Error(t, err)
	assert.False(t, consumererror.IsPermanent(err))
	var delivery *deliveryError
	require.ErrorAs(t, err, &delivery)
	assert.Equal(t, 1, delivery.retryFrom)
}

func TestClientSendRetriesUnconfirmedAppendResult(t *testing.T) {
	tests := []struct {
		name   string
		result scopedb.AppendRowsResult
	}{
		{
			name:   "unknown outcome",
			result: scopedb.AppendRowsResult{AppendState: scopedb.AppendStateUnknown},
		},
		{
			name:   "committed row count mismatch",
			result: scopedb.AppendRowsResult{AppendState: scopedb.AppendStateCommitted, NumRowsInserted: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testClientConfig("https://scopedb.invalid")
			cfg.Mappings.Logs = map[string]string{"message": "log.message"}
			client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
			require.NoError(t, err)
			defer client.Close()
			client.appendFn = func(context.Context, *scopedb.Table, []byte) (scopedb.AppendRowsResult, error) {
				return tt.result, nil
			}

			err = client.Send(context.Background(), signalLogs, &IngestPayload{Records: []Record{{"message": "hello"}}})
			require.Error(t, err)
			assert.False(t, consumererror.IsPermanent(err))
			var delivery *deliveryError
			require.ErrorAs(t, err, &delivery)
			assert.Equal(t, 0, delivery.retryFrom)
		})
	}
}

func TestClientSendCommitsBufferedPrefixBeforePermanentEncodingFailure(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = map[string]string{"body": "log.body"}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	appendCalls := 0
	client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		appendCalls++
		return scopedb.AppendRowsResult{
			AppendState:     scopedb.AppendStateCommitted,
			NumRowsInserted: int64(bytes.Count(body, []byte{'\n'})),
		}, nil
	}

	err = client.Send(context.Background(), signalLogs, &IngestPayload{Records: []Record{
		{"body": "valid"},
		{"body": math.NaN()},
	}})
	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
	assert.Equal(t, 1, appendCalls)
	var delivery *deliveryError
	require.ErrorAs(t, err, &delivery)
	assert.Equal(t, 1, delivery.retryFrom)
}

func TestClassifyAppendError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		permanent bool
		contains  string
	}{
		{
			name: "permanent rejection",
			err: &scopedb.Error{
				Kind:      scopedb.ErrorKindAppendRowsFailed,
				Message:   "invalid row",
				RequestID: "req-1",
				AppendDetails: &scopedb.AppendErrorDetails{
					AppendState: scopedb.AppendStateRejected,
					RowErrors:   []scopedb.AppendRowError{{RowIndex: 3, Column: "ts", Message: "invalid timestamp"}},
				},
			},
			permanent: true,
			contains:  "row=3 column=ts reason=invalid timestamp",
		},
		{
			name: "retryable rejection",
			err: &scopedb.Error{
				Kind:       scopedb.ErrorKindAppendRowsFailed,
				Message:    "busy",
				Retryable:  true,
				RetryAfter: time.Second,
				AppendDetails: &scopedb.AppendErrorDetails{
					AppendState: scopedb.AppendStateRejected,
				},
			},
			contains: "Throttle",
		},
		{
			name: "unknown is retried by gateway",
			err: &scopedb.Error{
				Kind:    scopedb.ErrorKindAppendRowsFailed,
				Message: "outcome unknown",
				AppendDetails: &scopedb.AppendErrorDetails{
					AppendState: scopedb.AppendStateUnknown,
				},
			},
			contains: "state=unknown",
		},
		{
			name: "deterministic client error",
			err: &scopedb.Error{
				Kind:    scopedb.ErrorKindConfigInvalid,
				Message: "invalid append configuration",
			},
			permanent: true,
			contains:  "invalid append configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classified := classifyAppendError(tt.err)
			assert.Equal(t, tt.permanent, consumererror.IsPermanent(classified))
			assert.ErrorContains(t, classified, tt.contains)
		})
	}
}

func testClientConfig(endpoint string) *Config {
	cfg := createDefaultConfig().(*Config)
	_, cfg.Mappings = StarterIngestionConfig().exporterConfig()
	cfg.Endpoint = endpoint
	cfg.APIKey = configopaque.String("test-api-key")
	cfg.Tables = TableRoutingConfig{
		Logs:    "public.vendor_otel_logs_test",
		Traces:  "public.vendor_otel_traces_test",
		Metrics: "public.vendor_otel_metrics_test",
	}
	cfg.Timeout.Timeout = 0
	return cfg
}

func decodeSDKRequestBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	switch r.Header.Get("Content-Encoding") {
	case "":
		return body, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	case "zstd":
		reader, err := zstd.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	default:
		return nil, errors.New("unsupported content encoding")
	}
}
