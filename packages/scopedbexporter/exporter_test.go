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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

type exporterTableInitStatementRequest struct {
	Statement string `json:"statement"`
	Format    string `json:"format"`
}

type exporterTableInitResultSetMetadata struct {
	Fields  []map[string]any `json:"fields"`
	NumRows uint64           `json:"num_rows"`
}

type exporterTableInitResultSet struct {
	Metadata *exporterTableInitResultSetMetadata `json:"metadata,omitempty"`
	Format   string                              `json:"format,omitempty"`
	Rows     json.RawMessage                     `json:"rows,omitempty"`
}

type exporterTableInitStatementResponse struct {
	StatementID string                      `json:"statement_id"`
	Status      string                      `json:"status"`
	Message     string                      `json:"message,omitempty"`
	ResultSet   *exporterTableInitResultSet `json:"result_set,omitempty"`
}

func TestExporterConsumeLogs(t *testing.T) {
	payloads, server := newCaptureServer(t)
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	exp, err := NewFactory().CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	err = exp.ConsumeLogs(context.Background(), buildTestLogs())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(payloads.All()) == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, signalLogs, payloads.All()[0].Rows[0]["signal"])
}

func TestExporterConsumeTraces(t *testing.T) {
	payloads, server := newCaptureServer(t)
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	exp, err := NewFactory().CreateTraces(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	err = exp.ConsumeTraces(context.Background(), buildTestTraces())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(payloads.All()) == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, signalTraces, payloads.All()[0].Rows[0]["signal"])
}

func TestExporterConsumeMetrics(t *testing.T) {
	payloads, server := newCaptureServer(t)
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	exp, err := NewFactory().CreateMetrics(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	err = exp.ConsumeMetrics(context.Background(), buildTestMetrics())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(payloads.All()) == 1
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, signalMetrics, payloads.All()[0].Rows[0]["signal"])
}

func TestExporterStartEnsuresTableWhenEnabled(t *testing.T) {
	var sawCreateTable bool
	payloads := &payloadStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/statements":
			sawCreateTable = true
			var request exporterTableInitStatementRequest
			body, err := decodeExporterRequestBody(r)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &request))
			assert.True(t,
				strings.HasPrefix(request.Statement, "CREATE DATABASE IF NOT EXISTS") ||
					strings.HasPrefix(request.Statement, "CREATE SCHEMA IF NOT EXISTS") ||
					strings.HasPrefix(request.Statement, "CREATE TABLE IF NOT EXISTS") ||
					strings.HasPrefix(request.Statement, "CREATE RANGE INDEX IF NOT EXISTS") ||
					strings.HasPrefix(request.Statement, "CREATE POINT INDEX IF NOT EXISTS") ||
					strings.HasPrefix(request.Statement, "CREATE SEARCH INDEX IF NOT EXISTS") ||
					strings.HasPrefix(request.Statement, "CREATE PATTERN INDEX IF NOT EXISTS"),
			)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(exporterTableInitStatementResponse{
				StatementID: "33333333-3333-3333-3333-333333333333",
				Status:      "finished",
				ResultSet: &exporterTableInitResultSet{
					Metadata: &exporterTableInitResultSetMetadata{
						Fields:  []map[string]any{},
						NumRows: 0,
					},
					Format: "json",
					Rows:   json.RawMessage(`[]`),
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == defaultPath:
			var request scopeDBIngestRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			rows := splitRows(t, request.Data.Rows)
			payloads.Add(receivedIngestRequest{
				Type:      request.Type,
				Statement: request.Statement,
				Rows:      rows,
			})
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	cfg.CreateTablesIfNotExist = true

	exp, err := NewFactory().CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	err = exp.ConsumeLogs(context.Background(), buildTestLogs())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(payloads.All()) == 1
	}, time.Second, 10*time.Millisecond)
	assert.True(t, sawCreateTable)
}

type payloadStore struct {
	mu       sync.Mutex
	payloads []receivedIngestRequest
}

type receivedIngestRequest struct {
	Type      string
	Statement string
	Rows      []map[string]any
}

func (s *payloadStore) Add(payload receivedIngestRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payloads = append(s.payloads, payload)
}

func (s *payloadStore) All() []receivedIngestRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]receivedIngestRequest, len(s.payloads))
	copy(out, s.payloads)
	return out
}

func newCaptureServer(t *testing.T) (*payloadStore, *httptest.Server) {
	t.Helper()

	store := &payloadStore{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeExporterRequestBody(r)
		require.NoError(t, err)
		var request scopeDBIngestRequest
		require.NoError(t, json.Unmarshal(body, &request))
		rows := splitRows(t, request.Data.Rows)
		store.Add(receivedIngestRequest{
			Type:      request.Type,
			Statement: request.Statement,
			Rows:      rows,
		})
		w.WriteHeader(http.StatusOK)
	}))

	return store, server
}

func testExporterConfig(endpoint string) *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = endpoint
	cfg.APIKey = configopaque.String("test-api-key")
	cfg.Compression = "none"
	cfg.Tables = TableRoutingConfig{
		Logs:    "public.vendor_otel_logs_test",
		Traces:  "public.vendor_otel_traces_test",
		Metrics: "public.vendor_otel_metrics_test",
	}
	cfg.Timeout.Timeout = 0
	cfg.RetryOnFailure.Enabled = false
	cfg.SendingQueue = configoptional.None[exporterhelper.QueueBatchConfig]()
	return cfg
}

func buildTestLogs() plog.Logs {
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	scopeLogs := resourceLogs.ScopeLogs().AppendEmpty()
	record := scopeLogs.LogRecords().AppendEmpty()
	record.Body().SetStr("hello")
	return logs
}

func buildTestTraces() ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	span := scopeSpans.Spans().AppendEmpty()
	span.SetName("test-span")
	span.SetTraceID(pcommon.TraceID([16]byte{1}))
	span.SetSpanID(pcommon.SpanID([8]byte{2}))
	return traces
}

func buildTestMetrics() pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()
	metric := scopeMetrics.Metrics().AppendEmpty()
	metric.SetName("cpu.utilization")
	point := metric.SetEmptyGauge().DataPoints().AppendEmpty()
	point.SetDoubleValue(0.5)
	return metrics
}

func splitRows(t *testing.T, raw string) []map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(raw), "\n")
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row))
		rows = append(rows, row)
	}
	return rows
}

func decodeExporterRequestBody(r *http.Request) ([]byte, error) {
	compressedBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "":
		return compressedBody, nil
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(compressedBody))
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		return io.ReadAll(gr)
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(compressedBody))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	default:
		return nil, io.ErrUnexpectedEOF
	}
}
