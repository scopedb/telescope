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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	scopedb "github.com/scopedb/goscopedb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestExporterConsumesLogsThroughAppend(t *testing.T) {
	var mu sync.Mutex
	var rows []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeTableDescription(t, w, "vendor_otel_logs_test", []string{"message"})
		case http.MethodPost:
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
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithStatus(statuses).CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
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

func TestExporterFailsStartWhenMappedColumnIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTableDescription(t, w, "vendor_otel_logs_test", []string{"other"})
	}))
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithStatus(statuses).CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	err = exp.Start(context.Background(), componenttest.NewNopHost())
	require.Error(t, err)
	assert.ErrorContains(t, err, "missing mapped columns: message")
	assert.False(t, statuses.Snapshot().Signals[signalLogs].Ready)
}

func TestExporterStartsWhenDestinationIsTemporarilyUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"message":   "temporary outage",
			"retryable": true,
		}))
	}))
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithStatus(statuses).CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	status := statuses.Snapshot().Signals[signalLogs]
	assert.True(t, status.Ready)
	assert.False(t, status.DestinationVerified)
	assert.Contains(t, status.LastError, "temporary outage")
}

func TestExporterStartsWhenDestinationConnectionIsRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	server.Close()

	cfg := testExporterConfig(endpoint)
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithStatus(statuses).CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	status := statuses.Snapshot().Signals[signalLogs]
	assert.True(t, status.Ready)
	assert.False(t, status.DestinationVerified)
	assert.Contains(t, status.LastError, "connection refused")
}

func TestExporterDropsCompleteScopeDBRowRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeTableDescription(t, w, "vendor_otel_logs_test", []string{"message"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"message":      "invalid row",
			"append_state": "rejected",
			"retryable":    false,
			"row_errors": []map[string]any{{
				"row_index": 0,
				"column":    "message",
				"message":   "wrong type",
			}},
		}))
	}))
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithStatus(statuses).CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("hello")
	require.NoError(t, exp.ConsumeLogs(context.Background(), logs))
	status := statuses.Snapshot().Signals[signalLogs]
	assert.Zero(t, status.ConfirmedWrittenRecords)
	assert.Equal(t, uint64(1), status.PermanentFailedRecords)
	assert.Contains(t, status.LastError, "column=message")
}

func TestExporterRetriesGoodRowsOnceAfterCompleteScopeDBRowErrors(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	attempt := 0
	var retried []map[string]any
	client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		attempt++
		if attempt == 1 {
			return scopedb.AppendRowsResult{}, &scopedb.Error{
				Kind:    scopedb.ErrorKindAppendRowsFailed,
				Message: "invalid row",
				AppendDetails: &scopedb.AppendErrorDetails{
					AppendState: scopedb.AppendStateRejected,
					RowErrors: []scopedb.AppendRowError{{
						RowIndex: 1,
						Column:   "message",
						Message:  "wrong type",
					}},
				},
			}
		}
		for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
			var row map[string]any
			require.NoError(t, json.Unmarshal(line, &row))
			retried = append(retried, row)
		}
		return scopedb.AppendRowsResult{
			AppendState:     scopedb.AppendStateCommitted,
			NumRowsInserted: int64(bytes.Count(body, []byte{'\n'})),
		}, nil
	}

	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, message := range []string{"before", "invalid", "after"} {
		records.AppendEmpty().Body().SetStr(message)
	}
	statuses := NewStatusRegistry()
	exp := &dbExporter{cfg: cfg, client: client, statuses: statuses}

	require.NoError(t, exp.pushLogs(context.Background(), logs))
	assert.Equal(t, 2, attempt)
	assert.Equal(t, []map[string]any{{"message": "before"}, {"message": "after"}}, retried)
	status := statuses.Snapshot().Signals[signalLogs]
	assert.Equal(t, uint64(2), status.ConfirmedWrittenRecords)
	assert.Equal(t, uint64(1), status.PermanentFailedRecords)
	assert.Contains(t, status.LastError, "column=message")
}

func TestExporterDoesNotSplitTruncatedScopeDBRowErrors(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	attempts := 0
	client.appendFn = func(context.Context, *scopedb.Table, []byte) (scopedb.AppendRowsResult, error) {
		attempts++
		return scopedb.AppendRowsResult{}, &scopedb.Error{
			Kind:    scopedb.ErrorKindAppendRowsFailed,
			Message: "invalid rows",
			AppendDetails: &scopedb.AppendErrorDetails{
				AppendState:        scopedb.AppendStateRejected,
				RowErrors:          []scopedb.AppendRowError{{RowIndex: 1, Column: "message", Message: "wrong type"}},
				RowErrorsTruncated: true,
			},
		}
	}

	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, message := range []string{"one", "two", "three"} {
		records.AppendEmpty().Body().SetStr(message)
	}
	statuses := NewStatusRegistry()
	exp := &dbExporter{cfg: cfg, client: client, statuses: statuses}

	err = exp.pushLogs(context.Background(), logs)
	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
	assert.Equal(t, 1, attempts)
	status := statuses.Snapshot().Signals[signalLogs]
	assert.Zero(t, status.ConfirmedWrittenRecords)
	assert.Equal(t, uint64(3), status.PermanentFailedRecords)
}

func TestExporterReturnsOnlyUncommittedSuffixForRetry(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"body": "log.body"})
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	attempt := 0
	client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		attempt++
		if attempt == 2 {
			return scopedb.AppendRowsResult{}, errors.New("temporary append failure")
		}
		return scopedb.AppendRowsResult{
			AppendState:     scopedb.AppendStateCommitted,
			NumRowsInserted: int64(bytes.Count(body, []byte{'\n'})),
		}, nil
	}

	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	records.AppendEmpty().Body().SetStr(strings.Repeat("a", 5*1024*1024))
	records.AppendEmpty().Body().SetStr(strings.Repeat("b", 5*1024*1024))
	records.AppendEmpty().Body().SetStr(strings.Repeat("c", 5*1024*1024))

	exp := &dbExporter{cfg: cfg, client: client, statuses: NewStatusRegistry()}
	err = exp.pushLogs(context.Background(), logs)
	require.Error(t, err)
	var partial consumererror.Logs
	require.ErrorAs(t, err, &partial)
	assert.Equal(t, 2, partial.Data().LogRecordCount())
	partialRecords := partial.Data().ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	assert.Equal(t, byte('b'), partialRecords.At(0).Body().Str()[0])
	assert.Equal(t, byte('c'), partialRecords.At(1).Body().Str()[0])
}

func TestExporterReturnsOnlyUncommittedSuffixForPermanentFailure(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"body": "log.body"})
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	attempt := 0
	client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		attempt++
		if attempt == 2 {
			return scopedb.AppendRowsResult{}, &scopedb.Error{
				Kind:    scopedb.ErrorKindAppendRowsFailed,
				Message: "invalid row",
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

	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	records.AppendEmpty().Body().SetStr(strings.Repeat("a", 5*1024*1024))
	records.AppendEmpty().Body().SetStr(strings.Repeat("b", 5*1024*1024))
	records.AppendEmpty().Body().SetStr(strings.Repeat("c", 5*1024*1024))
	statuses := NewStatusRegistry()
	exp := &dbExporter{cfg: cfg, client: client, statuses: statuses}

	err = exp.pushLogs(context.Background(), logs)
	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
	var partial consumererror.Logs
	require.ErrorAs(t, err, &partial)
	assert.Equal(t, 2, partial.Data().LogRecordCount())
	status := statuses.Snapshot().Signals[signalLogs]
	assert.Equal(t, uint64(1), status.ConfirmedWrittenRecords)
	assert.Equal(t, uint64(2), status.PermanentFailedRecords)
	assert.Equal(t, uint64(3), status.PermanentExportRecords)
}

func TestExporterReportsMappingCastFailure(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = MappingConfig{
		"attempt": {Source: `log.body["attempt"]`, Cast: "int"},
	}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()

	appendCalls := 0
	client.appendFn = func(context.Context, *scopedb.Table, []byte) (scopedb.AppendRowsResult, error) {
		appendCalls++
		return scopedb.AppendRowsResult{}, nil
	}
	logs := plog.NewLogs()
	record := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.Body().SetEmptyMap().PutStr("attempt", "second")
	statuses := NewStatusRegistry()
	exp := &dbExporter{cfg: cfg, client: client, statuses: statuses}

	require.NoError(t, exp.pushLogs(context.Background(), logs))
	assert.Zero(t, appendCalls)
	status := statuses.Snapshot().Signals[signalLogs]
	assert.Equal(t, uint64(1), status.PermanentFailedRecords)
	assert.Equal(t, uint64(1), status.InvalidItemsByReason[mappingReasonCastFailed])
}

func TestExporterDropsOnlyInvalidMappedLogRecord(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = MappingConfig{
		"attempt": {Source: `log.body["attempt"]`, Cast: "int"},
	}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
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
			AppendState:     scopedb.AppendStateCommitted,
			NumRowsInserted: int64(bytes.Count(body, []byte{'\n'})),
		}, nil
	}

	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, attempt := range []string{"1", "invalid", "3"} {
		records.AppendEmpty().Body().SetEmptyMap().PutStr("attempt", attempt)
	}
	statuses := NewStatusRegistry()
	exp := &dbExporter{cfg: cfg, client: client, statuses: statuses}

	require.NoError(t, exp.pushLogs(context.Background(), logs))
	assert.Equal(t, []map[string]any{{"attempt": float64(1)}, {"attempt": float64(3)}}, appended)
	status := statuses.Snapshot().Signals[signalLogs]
	assert.Equal(t, uint64(2), status.ConfirmedWrittenRecords)
	assert.Equal(t, uint64(1), status.PermanentFailedRecords)
	assert.Equal(t, uint64(1), status.InvalidItemsByReason[mappingReasonCastFailed])
}

func TestExporterRetrySubsetExcludesLocallyRejectedRecords(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = MappingConfig{
		"attempt": {Source: `log.body["attempt"]`, Cast: "int"},
	}
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()
	client.appendFn = func(context.Context, *scopedb.Table, []byte) (scopedb.AppendRowsResult, error) {
		return scopedb.AppendRowsResult{}, errors.New("temporary append failure")
	}

	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, attempt := range []string{"1", "invalid", "3"} {
		records.AppendEmpty().Body().SetEmptyMap().PutStr("attempt", attempt)
	}
	statuses := NewStatusRegistry()
	exp := &dbExporter{cfg: cfg, client: client, statuses: statuses}

	err = exp.pushLogs(context.Background(), logs)
	require.Error(t, err)
	assert.False(t, consumererror.IsPermanent(err))
	var partial consumererror.Logs
	require.ErrorAs(t, err, &partial)
	require.Equal(t, 2, partial.Data().LogRecordCount())
	partialRecords := partial.Data().ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	firstAttempt, found := partialRecords.At(0).Body().Map().Get("attempt")
	require.True(t, found)
	lastAttempt, found := partialRecords.At(1).Body().Map().Get("attempt")
	require.True(t, found)
	assert.Equal(t, "1", firstAttempt.Str())
	assert.Equal(t, "3", lastAttempt.Str())
	status := statuses.Snapshot().Signals[signalLogs]
	assert.Zero(t, status.ConfirmedWrittenRecords)
	assert.Equal(t, uint64(1), status.PermanentFailedRecords)
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
			for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
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
	exp, err := NewFactoryWithStatus(statuses).CreateMetrics(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
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

	mu.Lock()
	require.Equal(t, []map[string]any{{"value": "valid.metric"}}, rows)
	mu.Unlock()
	status := statuses.Snapshot().Signals[signalMetrics]
	assert.Equal(t, uint64(2), status.PermanentFailedRecords)
	assert.Zero(t, status.PermanentExportRecords)
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
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"database":     "scopedb",
		"schema":       "public",
		"name":         name,
		"columns":      items,
		"partition_by": []string{},
		"cluster_by":   []string{},
		"distinct_on":  map[string]any{"on": []string{}, "by": []string{}},
	}))
}

func writeAppendCommitted(t *testing.T, w http.ResponseWriter, rows int64) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"append_state":      "committed",
		"num_rows_inserted": rows,
	}))
}
