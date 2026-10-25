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

	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exportertest"
)

func TestClientSendAppendsMappedNDJSON(t *testing.T) {
	var received []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/catalog/databases/scopedb/schemas/public/tables/vendor_otel_logs_test/ingest", r.URL.Path)
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
			Status:          scopedb.AppendStateCommitted,
			NumRowsInserted: int64(len(received)),
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{
		"message": "log.message",
		"service": `resource.attributes["service.name"]`,
	})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	outcome := client.send(context.Background(), signalLogs, &ingestPayload{Records: []telemetryRecord{{
		"message":    "hello",
		"trace_id":   "not-selected",
		"attributes": map[string]any{"order.id": "not-selected"},
		"resource":   map[string]any{"service.name": "checkout", "host.name": "not-selected"},
	}}})
	requireCommitted(t, outcome)
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
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	requireCommitted(t, client.send(context.Background(), signalLogs, &ingestPayload{
		Records: []telemetryRecord{{"message": "hello"}},
	}))
}

func TestClientSendRoutesAllSignals(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	cfg.Mappings.Traces = shorthandMapping(map[string]string{"name": "span.name"})
	cfg.Mappings.Metrics = shorthandMapping(map[string]string{"name": "metric.name"})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	var tableNames []string
	var rows []map[string]any
	client.appendFn = func(_ context.Context, table *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		tableNames = append(tableNames, table.Name)
		var row map[string]any
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &row))
		rows = append(rows, row)
		return scopedb.AppendRowsResult{Status: scopedb.AppendStateCommitted, NumRowsInserted: 1}, nil
	}

	requireCommitted(t, client.send(context.Background(), signalLogs, &ingestPayload{Records: []telemetryRecord{{"message": "log"}}}))
	requireCommitted(t, client.send(context.Background(), signalTraces, &ingestPayload{Records: []telemetryRecord{{"name": "span"}}}))
	requireCommitted(t, client.send(context.Background(), signalMetrics, &ingestPayload{Records: []telemetryRecord{{"metric_name": "metric"}}}))

	assert.Equal(t, []string{"vendor_otel_logs_test", "vendor_otel_traces_test", "vendor_otel_metrics_test"}, tableNames)
	assert.Equal(t, []map[string]any{{"message": "log"}, {"name": "span"}, {"name": "metric"}}, rows)
}

func TestClientValidateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/catalog/databases/scopedb/schemas/public/tables/vendor_otel_logs_test", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"1"`)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name":         "vendor_otel_logs_test",
			"columns":      []map[string]any{{"name": "message", "data_type": "string"}},
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct":     map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	_, validationErr := client.inspectDestination(context.Background(), signalLogs)

	require.NoError(t, validationErr)
	validation, err := client.inspectDestination(context.Background(), signalLogs)
	require.NoError(t, err)
	require.Len(t, validation.Columns, 1)
	assert.Equal(t, "string", validation.Columns[0].TargetType)
	assert.Equal(t, MappingCompatible, validation.Columns[0].Compatibility)
	cfg.Mappings.Logs["service"] = MappingRule{Source: `resource.attributes["service.name"]`}
	client, err = newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()
	_, err = client.inspectDestination(context.Background(), signalLogs)
	require.Error(t, err)
	assert.ErrorContains(t, err, "missing mapped columns: service")
}

func TestClientValidateDestinationRejectsKnownTypeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"1"`)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name": "vendor_otel_logs_test",
			"columns": []map[string]any{
				{"name": "severity", "data_type": "string"},
				{"name": "tenant", "data_type": "string"},
			},
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct":     map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{
		"severity": "log.severity_number",
		"tenant":   `resource.attributes["tenant.id"]`,
	})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	validation, err := client.inspectDestination(context.Background(), signalLogs)
	require.Error(t, err)
	assert.ErrorContains(t, err, "severity (log.severity_number produces int, table has string)")
	assert.NotContains(t, err.Error(), "tenant")
	require.Len(t, validation.Columns, 2)
	assert.Equal(t, MappingIncompatible, validation.Columns[0].Compatibility)
	assert.Equal(t, MappingRuntimeDependent, validation.Columns[1].Compatibility)
}

func TestClientValidateDestinationUsesCastOutputType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTableDescription(t, w, "vendor_otel_logs_test", []string{"severity"})
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = MappingConfig{
		"severity": {Source: "log.severity_number", Cast: "string"},
	}
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	validation, err := client.inspectDestination(context.Background(), signalLogs)
	require.NoError(t, err)
	require.Len(t, validation.Columns, 1)
	assert.Equal(t, MappingCompatible, validation.Columns[0].Compatibility)
	assert.Equal(t, "string", validation.Columns[0].OutputType)
	assert.False(t, validation.Columns[0].RuntimeDependent)
}

func TestClientSendReportsRetryFromFailedChunk(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"body": "log.body"})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
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
					Status: scopedb.AppendStateRejected,
				},
			}
		}
		return scopedb.AppendRowsResult{
			Status:          scopedb.AppendStateCommitted,
			NumRowsInserted: int64(bytes.Count(body, []byte{'\n'})),
		}, nil
	}

	outcome := client.send(context.Background(), signalLogs, &ingestPayload{Records: []telemetryRecord{
		{"body": strings.Repeat("a", 5*1024*1024)},
		{"body": strings.Repeat("b", 5*1024*1024)},
	}})
	require.Error(t, outcome.err)
	assert.False(t, consumererror.IsPermanent(outcome.err))
	assert.Equal(t, []int{0}, outcome.committed)
	assert.Equal(t, []int{1}, outcome.uncommitted)
}

func TestClientSendDoesNotRetryUnconfirmedAppendResult(t *testing.T) {
	tests := []struct {
		name   string
		result scopedb.AppendRowsResult
	}{
		{
			name:   "unknown outcome",
			result: scopedb.AppendRowsResult{Status: scopedb.AppendStateUnknown},
		},
		{
			name:   "committed row count mismatch",
			result: scopedb.AppendRowsResult{Status: scopedb.AppendStateCommitted, NumRowsInserted: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testClientConfig("https://scopedb.invalid")
			cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
			client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
			require.NoError(t, err)
			defer client.Close()
			client.appendFn = func(context.Context, *scopedb.Table, []byte) (scopedb.AppendRowsResult, error) {
				return tt.result, nil
			}

			outcome := client.send(context.Background(), signalLogs, &ingestPayload{Records: []telemetryRecord{{"message": "hello"}}})
			require.NoError(t, outcome.err)
			require.Len(t, outcome.unknown, 1)
			assert.Empty(t, outcome.uncommitted)
			assert.Empty(t, outcome.committed)
		})
	}
}

func TestClientSendCommitsValidRowsAroundPermanentEncodingFailure(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"body": "log.body"})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	var appended []map[string]any
	client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
			var row map[string]any
			require.NoError(t, json.Unmarshal(line, &row))
			appended = append(appended, row)
		}
		return scopedb.AppendRowsResult{
			Status:          scopedb.AppendStateCommitted,
			NumRowsInserted: int64(bytes.Count(body, []byte{'\n'})),
		}, nil
	}

	outcome := client.send(context.Background(), signalLogs, &ingestPayload{Records: []telemetryRecord{
		{"body": "before"},
		{"body": math.NaN()},
		{"body": "after"},
	}})
	require.NoError(t, outcome.err)
	require.Len(t, outcome.rejected, 1)
	assert.Equal(t, []int{0, 2}, outcome.committed)
	assert.Equal(t, []map[string]any{{"body": "before"}, {"body": "after"}}, appended)
	assert.ErrorContains(t, outcome.rejected[0].err, "mapped row 1")
}

func TestClientSendRejectsInvalidCastBeforeAppend(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = MappingConfig{
		"attempt": {Source: `log.attributes["attempt"]`, Cast: "int"},
	}
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	appendCalls := 0
	client.appendFn = func(context.Context, *scopedb.Table, []byte) (scopedb.AppendRowsResult, error) {
		appendCalls++
		return scopedb.AppendRowsResult{}, nil
	}
	outcome := client.send(context.Background(), signalLogs, &ingestPayload{Records: []telemetryRecord{{
		"attributes": map[string]any{"attempt": "second"},
	}}})
	require.NoError(t, outcome.err)
	require.Len(t, outcome.rejected, 1)
	assert.Equal(t, 0, appendCalls)
	reason, ok := mappingErrorReason(outcome.rejected[0].err)
	assert.True(t, ok)
	assert.Equal(t, mappingReasonCastFailed, reason)
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
					Status:    scopedb.AppendStateRejected,
					RowErrors: []scopedb.AppendRowError{{RowIndex: 3, Column: "ts", Message: "invalid timestamp"}},
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
					Status: scopedb.AppendStateRejected,
				},
			},
			contains: "Throttle",
		},
		{
			name:      "unknown is terminal",
			permanent: true,
			err: &scopedb.Error{
				Kind:    scopedb.ErrorKindAppendRowsFailed,
				Message: "outcome unknown",
				AppendDetails: &scopedb.AppendErrorDetails{
					Status: scopedb.AppendStateUnknown,
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

func TestCompleteRejectedRowsRequiresAnExactRowSet(t *testing.T) {
	tests := []struct {
		name      string
		retryable bool
		truncated bool
		rows      []scopedb.AppendRowError
		wantRows  []scopedb.AppendRowError
		wantOK    bool
	}{
		{
			name:     "complete",
			rows:     []scopedb.AppendRowError{{RowIndex: 1}, {RowIndex: 0}},
			wantRows: []scopedb.AppendRowError{{RowIndex: 0}, {RowIndex: 1}},
			wantOK:   true,
		},
		{name: "retryable", retryable: true, rows: []scopedb.AppendRowError{{RowIndex: 0}}},
		{name: "truncated", truncated: true, rows: []scopedb.AppendRowError{{RowIndex: 0}}},
		{name: "missing rows"},
		{
			name:     "multiple errors for one row",
			rows:     []scopedb.AppendRowError{{RowIndex: 0}, {RowIndex: 0}},
			wantRows: []scopedb.AppendRowError{{RowIndex: 0}},
			wantOK:   true,
		},
		{name: "out of range", rows: []scopedb.AppendRowError{{RowIndex: 2}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &scopedb.Error{
				Retryable: tt.retryable,
				AppendDetails: &scopedb.AppendErrorDetails{
					Status:             scopedb.AppendStateRejected,
					RowErrors:          tt.rows,
					RowErrorsTruncated: tt.truncated,
				},
			}
			rows, ok := completeRejectedRows(err, 2)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantRows, rows)
		})
	}
}

func testClientConfig(endpoint string) *Config {
	cfg := validTestConfig()
	cfg.Endpoint = endpoint
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

func requireCommitted(t *testing.T, outcome sendOutcome) {
	t.Helper()
	require.NoError(t, outcome.err)
	require.NotEmpty(t, outcome.committed)
	require.Empty(t, outcome.rejected)
	require.Empty(t, outcome.unknown)
	require.Empty(t, outcome.uncommitted)
}
