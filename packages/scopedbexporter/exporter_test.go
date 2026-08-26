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

	scopedb "github.com/scopedb/goscopedb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/plog"
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
	cfg.Mappings.Logs = map[string]string{"message": "log.message"}
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

func TestExporterFailsStartWhenMappedColumnIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTableDescription(t, w, "vendor_otel_logs_test", []string{"other"})
	}))
	defer server.Close()

	cfg := testExporterConfig(server.URL)
	cfg.Mappings.Logs = map[string]string{"message": "log.message"}
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

func TestExporterClassifiesRejectedAppendAsPermanent(t *testing.T) {
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
	cfg.Mappings.Logs = map[string]string{"message": "log.message"}
	exp, err := NewFactory().CreateLogs(context.Background(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(context.Background(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())

	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("hello")
	err = exp.ConsumeLogs(context.Background(), logs)
	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
	assert.ErrorContains(t, err, "column=message")
}

func TestExporterReturnsOnlyUncommittedSuffixForRetry(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = map[string]string{"body": "log.body"}
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

	exp := &dbExporter{cfg: cfg, client: client, statuses: NewStatusRegistry()}
	err = exp.pushLogs(context.Background(), logs)
	require.Error(t, err)
	var partial consumererror.Logs
	require.ErrorAs(t, err, &partial)
	assert.Equal(t, 1, partial.Data().LogRecordCount())
	body := partial.Data().ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Str()
	assert.Len(t, body, 5*1024*1024)
	assert.Equal(t, byte('b'), body[0])
}

func TestExporterReturnsOnlyUncommittedSuffixForPermanentFailure(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Mappings.Logs = map[string]string{"body": "log.body"}
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
	statuses := NewStatusRegistry()
	exp := &dbExporter{cfg: cfg, client: client, statuses: statuses}

	err = exp.pushLogs(context.Background(), logs)
	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
	var partial consumererror.Logs
	require.ErrorAs(t, err, &partial)
	assert.Equal(t, 1, partial.Data().LogRecordCount())
	assert.Equal(t, uint64(1), statuses.Snapshot().Signals[signalLogs].PermanentFailedRecords)
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
