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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
)

func TestUnknownChunkIsNotReplayedWithRetryableSuffix(t *testing.T) {
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.AppendBatch.MaxRows = 1
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	statuses := NewStatusRegistry()
	exp, err := newDBExporter(cfg, exportertest.NewNopSettings(typeStr), signalLogs, statuses)
	require.NoError(t, err)
	defer exp.client.Close()
	var calls []string
	exp.client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		var row map[string]string
		require.NoError(t, json.Unmarshal(body, &row))
		calls = append(calls, row["message"])
		switch len(calls) {
		case 2:
			return scopedb.AppendRowsResult{}, &scopedb.Error{Message: "connection lost", AppendDetails: &scopedb.AppendErrorDetails{Status: scopedb.AppendStateUnknown}}
		case 3:
			return scopedb.AppendRowsResult{}, &scopedb.Error{Message: "busy", Retryable: true, AppendDetails: &scopedb.AppendErrorDetails{Status: scopedb.AppendStateRejected}}
		default:
			return scopedb.AppendRowsResult{Status: scopedb.AppendStateCommitted, NumRowsInserted: 1}, nil
		}
	}
	logs := plog.NewLogs()
	records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, value := range []string{"committed", "unknown", "rejected", "unsent"} {
		records.AppendEmpty().Body().SetStr(value)
	}
	err = exp.pushLogs(t.Context(), logs)
	require.Error(t, err)
	require.False(t, consumererror.IsPermanent(err))
	var partial consumererror.Logs
	require.ErrorAs(t, err, &partial)
	require.Equal(t, 2, partial.Data().LogRecordCount())
	require.NoError(t, exp.pushLogs(t.Context(), partial.Data()))
	require.Equal(t, []string{"committed", "unknown", "rejected", "rejected", "unsent"}, calls)
	state := statuses.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(3), state.ConfirmedWrittenRecords)
	require.Equal(t, uint64(1), state.UnknownRecords)
	require.Zero(t, state.PermanentFailedRecords)
}

func TestWriteTimeoutAndConnectionLossAreTerminalUnknown(t *testing.T) {
	for _, mode := range []string{"timeout", "disconnect", "unstructured_503"} {
		t.Run(mode, func(t *testing.T) {
			var attempts atomic.Int32
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				switch mode {
				case "timeout":
					select {
					case <-r.Context().Done():
					case <-release:
					}
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
				case "unstructured_503":
					http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			defer close(release)
			cfg := testExporterConfig(server.URL)
			cfg.Timeout.Timeout = time.Second
			cfg.RetryOnFailure.Enabled = true
			cfg.RetryOnFailure.InitialInterval = time.Millisecond
			cfg.RetryOnFailure.MaxInterval = time.Millisecond
			cfg.RetryOnFailure.MaxElapsedTime = 0
			statuses := NewStatusRegistry()
			exp, err := NewFactoryWithStatus(statuses).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
			require.NoError(t, err)
			require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
			defer exp.Shutdown(context.Background())
			logs := plog.NewLogs()
			logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("message")
			err = exp.ConsumeLogs(t.Context(), logs)
			require.Error(t, err)
			require.Equal(t, int32(1), attempts.Load())
			state := statuses.Snapshot().Signals[signalLogs]
			require.Equal(t, uint64(1), state.UnknownRecords)
			require.Zero(t, state.ConfirmedWrittenRecords)
			require.Zero(t, state.PermanentFailedRecords)
		})
	}
}

func TestCollectorRespectsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	var first time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			first = time.Now()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"message":"busy","retryable":true,"details":{"status":"rejected"}}`)
			return
		}
		assert.GreaterOrEqual(t, time.Since(first), time.Second)
		writeAppendCommitted(t, w, 1)
	}))
	defer server.Close()
	cfg := testExporterConfig(server.URL)
	cfg.RetryOnFailure.Enabled = true
	cfg.RetryOnFailure.InitialInterval = time.Millisecond
	cfg.RetryOnFailure.MaxInterval = time.Millisecond
	cfg.RetryOnFailure.MaxElapsedTime = 5 * time.Second
	statuses := NewStatusRegistry()
	exp, err := NewFactoryWithStatus(statuses).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	defer exp.Shutdown(context.Background())
	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("message")
	require.NoError(t, exp.ConsumeLogs(t.Context(), logs))
	require.Equal(t, int32(2), attempts.Load())
	require.Equal(t, uint64(1), statuses.Snapshot().Signals[signalLogs].ConfirmedWrittenRecords)
}

func TestAppendBatchBoundsMappedPayload(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	cfg.AppendBatch = AppendBatchConfig{MaxRows: 2, MaxBytes: 64}
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()
	var sent []string
	client.appendFn = func(_ context.Context, _ *scopedb.Table, body []byte) (scopedb.AppendRowsResult, error) {
		require.LessOrEqual(t, len(body), 64)
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		require.LessOrEqual(t, len(lines), 2)
		for _, line := range lines {
			var row map[string]string
			require.NoError(t, json.Unmarshal([]byte(line), &row))
			sent = append(sent, row["message"])
		}
		return scopedb.AppendRowsResult{Status: scopedb.AppendStateCommitted, NumRowsInserted: int64(len(lines))}, nil
	}
	outcome := client.send(t.Context(), signalLogs, &IngestPayload{Records: []Record{{"message": "a"}, {"message": strings.Repeat("x", 80)}, {"message": strings.Repeat("y", 35)}, {"message": "b"}, {"message": "c"}}})
	require.NoError(t, outcome.err)
	require.Len(t, outcome.rejected, 1)
	require.Equal(t, []string{"a", strings.Repeat("y", 35), "b", "c"}, sent)
}

func TestExporterTimeoutOverridesSDKDefault(t *testing.T) {
	// Inspect the HTTP context so the SDK deadline regression needs no 30-second wait.
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Timeout.Timeout = 60 * time.Second
	cfg.SendingQueue = configoptional.None[exporterhelper.QueueBatchConfig]()
	exp, err := newDBExporter(cfg, exportertest.NewNopSettings(typeStr), signalLogs, NewStatusRegistry())
	require.NoError(t, err)
	defer exp.client.Close()
	exp.client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		require.Greater(t, time.Until(deadline), 50*time.Second)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"committed","num_rows_inserted":1}`))}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), cfg.Timeout.Timeout)
	defer cancel()
	require.NoError(t, exp.client.Send(ctx, signalLogs, &IngestPayload{Records: []Record{{"message": "ok"}}}))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
