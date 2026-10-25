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
	"sync"
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

func TestUnconfirmedAppendsAreTerminalByDefault(t *testing.T) {
	for _, mode := range []string{"timeout", "disconnect", "unstructured_503", "missing_acknowledgement", "wrong_row_count"} {
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
				case "missing_acknowledgement":
					fmt.Fprint(w, "{}")
				case "wrong_row_count":
					writeAppendCommitted(t, w, 0)
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
			exp, err := NewFactoryWithRegistries(statuses, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
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
	for name, responseBody := range map[string]string{
		"structured":   `{"message":"busy","retryable":true,"details":{"status":"rejected"}}`,
		"unstructured": "too many requests",
	} {
		t.Run(name, func(t *testing.T) {

			var attempts atomic.Int32
			var first time.Time
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					first = time.Now()
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusTooManyRequests)
					fmt.Fprint(w, responseBody)
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
			exp, err := NewFactoryWithRegistries(statuses, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
			require.NoError(t, err)
			require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
			defer exp.Shutdown(context.Background())
			logs := plog.NewLogs()
			logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("message")
			require.NoError(t, exp.ConsumeLogs(t.Context(), logs))
			require.Equal(t, int32(2), attempts.Load())
			require.Equal(t, uint64(1), statuses.Snapshot().Signals[signalLogs].ConfirmedWrittenRecords)
		})
	}
}

func TestMemoryQueueDrainsAtShutdownWithoutRetrying(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			var calls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					close(entered)
					<-release
				}
				if success {
					writeAppendCommitted(t, w, 1)
					return
				}
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"details":{"status":"rejected"}}`)
			}))
			defer func() { once.Do(func() { close(release) }); backend.Close() }()
			cfg := testExporterConfig(backend.URL)
			queue := exporterhelper.NewDefaultQueueConfig()
			queue.NumConsumers = 1
			cfg.SendingQueue = configoptional.Some(queue)
			cfg.RetryOnFailure.Enabled = true
			cfg.RetryOnFailure.InitialInterval = time.Hour
			cfg.RetryOnFailure.MaxInterval = time.Hour
			cfg.RetryOnFailure.MaxElapsedTime = 0
			statuses := NewStatusRegistry()
			exp, err := NewFactoryWithRegistries(statuses, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
			require.NoError(t, err)
			require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
			for range 3 {
				logs := plog.NewLogs()
				logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("queued")
				require.NoError(t, exp.ConsumeLogs(t.Context(), logs))
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("no append started")
			}
			done := make(chan error, 1)
			go func() { done <- exp.Shutdown(context.Background()) }()
			require.Eventually(t, exp.(*capturingLogsExporter).exp.stopping.Load, time.Second, time.Millisecond)
			once.Do(func() { close(release) })
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("queue did not drain")
			}
			require.Equal(t, int32(3), calls.Load())
			state := statuses.Snapshot().Signals[signalLogs]
			if success {
				require.Equal(t, uint64(3), state.ConfirmedWrittenRecords)
			} else {
				require.Equal(t, uint64(3), state.RetryExhaustedRecords)
			}
			require.Zero(t, state.UnknownRecords)
		})
	}
}

func TestExporterTimeoutOverridesSDKDefault(t *testing.T) {
	// Inspect the HTTP context so the SDK deadline regression needs no 30-second wait.
	cfg := testExporterConfig("https://scopedb.invalid")
	cfg.Timeout.Timeout = time.Minute
	exp, _ := startTestLogsExporter(t, cfg)
	exp.(*capturingLogsExporter).exp.client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		require.True(t, ok)
		require.Greater(t, time.Until(deadline), 50*time.Second)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"committed","num_rows_inserted":1}`))}, nil
	})
	require.NoError(t, exp.ConsumeLogs(t.Context(), testLogs("ok")))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestPartialDeliveryFinalOutcomes(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, tc := range []struct {
			name                                  string
			status                                int
			written, unknown, rejected, exhausted uint64
		}{
			{"committed", 200, 3, 1, 1, 0},
			{"rejected", 400, 1, 1, 3, 0},
			{"exhausted", 429, 1, 1, 1, 2},
			{"unknown", 503, 2, 2, 1, 0},
		} {
			t.Run(fmt.Sprintf("%s/queued=%v", tc.name, queued), func(t *testing.T) {
				var mu sync.Mutex
				attempts := map[int]int{}
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := decodeSDKRequestBody(r)
					require.NoError(t, err)
					var row struct {
						Value int `json:"value"`
					}
					require.NoError(t, json.Unmarshal(body, &row))
					mu.Lock()
					defer mu.Unlock()
					attempts[row.Value]++
					switch row.Value {
					case 2:
						http.Error(w, "unknown", http.StatusServiceUnavailable)
						return
					case 3:
						if attempts[3] == 1 {
							http.Error(w, "busy", http.StatusTooManyRequests)
							return
						}
						if tc.status != 200 {
							w.Header().Set("Retry-After", "10")
							w.WriteHeader(tc.status)
							if tc.status == 400 {
								fmt.Fprint(w, `{"details":{"status":"rejected"}}`)
							}
							return
						}
					}
					writeAppendCommitted(t, w, 1)
				}))
				t.Cleanup(backend.Close)
				cfg := testExporterConfig(backend.URL)
				cfg.AppendBatch.MaxRows = 1
				cfg.Mappings.Logs = MappingConfig{"value": {Source: "log.message", Cast: "int"}}
				cfg.RetryOnFailure.Enabled = true
				cfg.RetryOnFailure.InitialInterval = time.Millisecond
				cfg.RetryOnFailure.MaxInterval = time.Millisecond
				cfg.RetryOnFailure.MaxElapsedTime = time.Second
				if queued {
					cfg.SendingQueue = configoptional.Some(exporterhelper.NewDefaultQueueConfig())
				}
				exp, statuses := startTestLogsExporter(t, cfg)
				err := exp.ConsumeLogs(t.Context(), testLogs("1", "2", "invalid", "3", "4"))
				if queued {
					require.NoError(t, err)
				} else if tc.status == 400 || tc.status == 429 {
					require.Error(t, err)
				}
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					s := statuses.Snapshot().Signals[signalLogs]
					assert.Equal(c, tc.written, s.ConfirmedWrittenRecords)
					assert.Equal(c, tc.unknown, s.UnknownRecords)
					assert.Equal(c, tc.rejected, s.PermanentFailedRecords)
					assert.Equal(c, tc.exhausted, s.RetryExhaustedRecords)
					assert.Equal(c, uint64(1), s.InvalidItemsByReason[mappingReasonCastFailed])
				}, time.Second, time.Millisecond)
				mu.Lock()
				defer mu.Unlock()
				require.Equal(t, 1, attempts[1])
				require.Equal(t, 1, attempts[2])
				require.Equal(t, 2, attempts[3])
				require.LessOrEqual(t, attempts[4], 1)
			})
		}
	}
}

func TestRejectedRowsAreIsolatedOnlyWithCompleteDetails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		indexes   []uint64
		truncated bool
		permanent bool
		want      []string
	}{
		{"one invalid row", []uint64{1}, false, false, []string{"before", "after"}},
		{"duplicate row errors", []uint64{2, 0, 2}, false, false, []string{"invalid"}},
		{"all rejected", []uint64{0, 1, 2}, false, false, nil},
		{"truncated", []uint64{1}, true, true, nil},
		{"out of range", []uint64{3}, false, true, nil},
		{"missing", nil, false, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			var accepted []string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					details := scopedb.AppendErrorDetails{Status: scopedb.AppendStateRejected, RowErrorsTruncated: tc.truncated}
					for _, index := range tc.indexes {
						details.RowErrors = append(details.RowErrors, scopedb.AppendRowError{RowIndex: index, Column: "message", Message: "wrong type"})
					}
					w.WriteHeader(422)
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"details": details}))
					return
				}
				body, err := decodeSDKRequestBody(r)
				require.NoError(t, err)
				for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
					var row map[string]string
					require.NoError(t, json.Unmarshal([]byte(line), &row))
					accepted = append(accepted, row["message"])
				}
				writeAppendCommitted(t, w, int64(len(accepted)))
			}))
			t.Cleanup(backend.Close)
			exp, statuses := startTestLogsExporter(t, testExporterConfig(backend.URL))
			err := exp.ConsumeLogs(t.Context(), testLogs("before", "invalid", "after"))
			if tc.permanent {
				require.True(t, consumererror.IsPermanent(err), "%v", err)
			} else {
				require.NoError(t, err)
			}
			backend.Close()
			require.Equal(t, tc.want, accepted)
			if len(tc.want) > 0 {
				require.Equal(t, 2, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			s := statuses.Snapshot().Signals[signalLogs]
			require.Equal(t, uint64(len(tc.want)), s.ConfirmedWrittenRecords)
			require.Equal(t, uint64(3-len(tc.want)), s.PermanentFailedRecords)
			require.Zero(t, s.UnknownRecords)
		})
	}
}

func TestAppendBatchBoundsMappedPayload(t *testing.T) {
	var sent []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSDKRequestBody(r)
		require.NoError(t, err)
		require.LessOrEqual(t, len(body), 64)
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		require.LessOrEqual(t, len(lines), 2)
		for _, line := range lines {
			var row map[string]string
			require.NoError(t, json.Unmarshal([]byte(line), &row))
			sent = append(sent, row["message"])
		}
		writeAppendCommitted(t, w, int64(len(lines)))
	}))
	t.Cleanup(backend.Close)
	cfg := testExporterConfig(backend.URL)
	cfg.AppendBatch = AppendBatchConfig{MaxRows: 2, MaxBytes: 64}
	exp, statuses := startTestLogsExporter(t, cfg)
	require.NoError(t, exp.ConsumeLogs(t.Context(), testLogs("a", strings.Repeat("x", 80), strings.Repeat("y", 35), "b", "c")))
	backend.Close()
	require.Equal(t, []string{"a", strings.Repeat("y", 35), "b", "c"}, sent)
	require.Equal(t, uint64(1), statuses.Snapshot().Signals[signalLogs].InvalidItemsByReason[mappingReasonRowTooLarge])
}
