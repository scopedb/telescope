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
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestExporterConsumesLogsThroughAppend(t *testing.T) {
	for _, compression := range []string{"zstd", "gzip"} {
		t.Run(compression, func(t *testing.T) {
			var mu sync.Mutex
			var rows []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPost:
					require.Equal(t, "/v1/catalog/databases/scopedb/schemas/public/tables/vendor_otel_logs_test/ingest", r.URL.Path)
					require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
					require.Equal(t, compression, r.Header.Get("Content-Encoding"))
					body, err := decodeSDKRequestBody(r)
					require.NoError(t, err)
					var row map[string]any
					require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &row))
					mu.Lock()
					rows = append(rows, row)
					mu.Unlock()
					writeAppendCommitted(t, w, 1)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))
			defer server.Close()

			cfg := testExporterConfig(server.URL)
			cfg.Compression = compression
			cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
			statuses := NewStatusRegistry()
			exp, err := NewFactoryWithRegistries(statuses, nil).CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
			require.NoError(t, err)
			require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
			defer exp.Shutdown(context.Background())

			logs := plog.NewLogs()
			resourceLogs := logs.ResourceLogs().AppendEmpty()
			resourceLogs.Resource().Attributes().PutStr(ProbeAttribute, "probe-1")
			record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
			record.Body().SetStr("hello")
			record.Attributes().PutStr("not.selected", "discarded")
			require.NoError(t, exp.ConsumeLogs(context.Background(), logs))

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, rows, 1)
			assert.Equal(t, map[string]any{"message": "hello"}, rows[0])
			assert.Equal(t, []string{"probe-1"}, statuses.Snapshot().Signals[signalLogs].LastProbeIDs)
		})
	}
}

func TestExporterCapturesLogsBeforeTheSendingQueue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeTableDescription(t, w, "vendor_otel_logs_test", []string{"message"})
			return
		}
		writeAppendCommitted(t, w, 1)
	}))
	defer server.Close()

	captures := NewCaptureRegistry()
	statuses := NewStatusRegistry()
	captured := make(chan captureResult, 1)
	ready := make(chan struct{})
	go func() {
		sample, err := captures.CaptureWithReady(context.Background(), signalLogs, 1, time.Second, ready)
		captured <- captureResult{sample: sample, err: err}
	}()
	waitCaptureReady(t, ready)

	cfg := testExporterConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	exp, err := NewFactoryWithRegistries(statuses, captures).CreateLogs(
		context.Background(),
		exportertest.NewNopSettings(typeStr),
		cfg,
	)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("hello")
	require.NoError(t, exp.ConsumeLogs(context.Background(), logs))

	result := <-captured
	require.NoError(t, result.err)
	request := plogotlp.NewExportRequest()
	require.NoError(t, request.UnmarshalJSON(result.sample.Payload))
	assert.Equal(t, 1, request.Logs().LogRecordCount())
}

func TestExporterStartsWithoutContactingDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("startup must not contact ScopeDB")
	}))
	defer server.Close()
	cfg := testExporterConfig(server.URL)
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(statuses, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())
	status := statuses.Snapshot().Signals[signalLogs]
	assert.True(t, status.Ready)
	assert.False(t, status.DestinationVerified)
	assert.Empty(t, status.LastError)
}

func TestMetricExporterDropsOnlyTheInvalidMetric(t *testing.T) {
	var mu sync.Mutex
	var rows []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeTableDescription(t, w, "vendor_otel_metrics_test", []string{"value"})
		case http.MethodPost:
			body, err := decodeSDKRequestBody(r)
			require.NoError(t, err)
			for line := range bytes.SplitSeq(bytes.TrimSpace(body), []byte{'\n'}) {
				var row map[string]any
				require.NoError(t, json.Unmarshal(line, &row))
				mu.Lock()
				rows = append(rows, row)
				mu.Unlock()
			}
			writeAppendCommitted(t, w, int64(len(bytes.Split(bytes.TrimSpace(body), []byte{'\n'}))))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	cfg.Mappings.Metrics = shorthandMapping(map[string]string{"value": "metric.name"})
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(statuses, nil).CreateMetrics(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	metrics := pmetric.NewMetrics()
	metricSlice := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics()
	valid := metricSlice.AppendEmpty()
	valid.SetName("valid.metric")
	valid.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(1)
	invalid := metricSlice.AppendEmpty()
	invalid.SetName("invalid.metric")
	invalidPoints := invalid.SetEmptyGauge().DataPoints()
	invalidPoints.AppendEmpty().SetIntValue(2)
	invalidPoints.AppendEmpty()

	require.NoError(t, exp.ConsumeMetrics(context.Background(), metrics))
	require.Equal(t, 3, metrics.DataPointCount(), "consume must not mutate its input")

	mu.Lock()
	require.Equal(t, []map[string]any{{"value": "valid.metric"}}, rows)
	mu.Unlock()
	status := statuses.Snapshot().Signals[signalMetrics]
	assert.Equal(t, uint64(2), status.PermanentFailedRecords)
	assert.Zero(t, status.RetryExhaustedRecords)
	assert.Equal(t, uint64(1), status.InvalidItemsByReason[mappingReasonUnsupportedNumberValueType])
}

func testExporterConfig(endpoint string) *Config {
	cfg := testClientConfig(endpoint)
	cfg.Timeout.Timeout = 0
	cfg.RetryOnFailure.Enabled = false
	cfg.SendingQueue = configoptional.None[exporterhelper.QueueBatchConfig]()
	return cfg
}

func writeTableDescription(t *testing.T, w http.ResponseWriter, name string, columns []string) {
	t.Helper()
	items := make([]map[string]any, 0, len(columns))
	for _, column := range columns {
		items = append(items, map[string]any{"name": column, "data_type": "any"})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", `"1"`)
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"name":         name,
		"columns":      items,
		"partition_by": []string{},
		"cluster_by":   []string{},
		"distinct":     map[string]any{"on": []string{}, "by": []string{}},
	}))
}

func writeAppendCommitted(t *testing.T, w http.ResponseWriter, rows int64) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"status":            "committed",
		"num_rows_inserted": rows,
	}))
}

func TestDefaultFactoryIsolatesNamedExporters(t *testing.T) {
	factory := NewFactory()
	create := func(factory exporter.Factory, table string) *capturingLogsExporter {
		cfg := testExporterConfig("https://scopedb.invalid")
		cfg.Tables.Logs = table
		exp, err := factory.CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
		require.NoError(t, err)
		require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
		t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })
		return exp.(*capturingLogsExporter)
	}
	first := create(factory, "first")
	second := create(factory, "second")
	third := create(NewFactory(), "third")
	first.exp.statuses.recordWrite(signalLogs, 3, time.Now(), nil, false)
	for _, entry := range []struct {
		exp     *capturingLogsExporter
		table   string
		written uint64
	}{
		{first, "first", 3}, {second, "second", 0}, {third, "third", 0},
	} {
		snapshot := entry.exp.exp.statuses.Snapshot().Signals[signalLogs]
		require.Equal(t, entry.table, snapshot.Table)
		require.Equal(t, entry.written, snapshot.ConfirmedWrittenRecords)
	}
}

func startTestLogsExporter(t *testing.T, cfg *Config) (exporter.Logs, *StatusRegistry) {
	t.Helper()
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(statuses, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })
	return exp, statuses
}

func testLogs(values ...string) plog.Logs {
	logs := plog.NewLogs()
	rows := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, value := range values {
		rows.AppendEmpty().Body().SetStr(value)
	}
	return logs
}

func TestExporterIsolatesUnencodableRecords(t *testing.T) {
	var body []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		body, err = decodeSDKRequestBody(r)
		require.NoError(t, err)
		writeAppendCommitted(t, w, 2)
	}))
	t.Cleanup(backend.Close)
	cfg := testExporterConfig(backend.URL)
	cfg.Mappings.Logs = MappingConfig{"body": {Source: "log.body"}}
	exp, statuses := startTestLogsExporter(t, cfg)
	logs := testLogs("before", "invalid", "after")
	logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(1).Body().SetDouble(math.NaN())
	require.NoError(t, exp.ConsumeLogs(t.Context(), logs))
	backend.Close()
	require.Equal(t, "{\"body\":\"before\"}\n{\"body\":\"after\"}\n", string(body))
	s := statuses.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(2), s.ConfirmedWrittenRecords)
	require.Equal(t, uint64(1), s.InvalidItemsByReason[mappingReasonEncodingFailed])
}
