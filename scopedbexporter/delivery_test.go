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
	"testing/synctest"
	"time"

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
	for _, mode := range []string{"timeout", "disconnect", "unstructured_503", "unstructured_429", "missing_acknowledgement", "wrong_row_count"} {
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
				case "unstructured_429":
					http.Error(w, "busy", http.StatusTooManyRequests)
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
			require.Equal(t, uint64(1), state.AmbiguousRecords)
			require.Zero(t, state.ConfirmedWrittenRecords)
			require.Zero(t, state.PermanentFailedRecords)
		})
	}
}

func TestCollectorRespectsRetryAfter(t *testing.T) {
	for name, responseBody := range map[string]string{
		"structured": `{"code":"too_many_requests","message":"busy","retryable":true,"details":{"status":"rejected"}}`,
		"modern":     `{"error":{"code":"too_many_requests","message":"busy"}}`,
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
				w.WriteHeader(429)
				_, _ = io.WriteString(w, `{"error":{"code":"too_many_requests","message":"busy"}}`)
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
			require.Zero(t, state.AmbiguousRecords)
		})
	}
}

func TestEachAppendGetsItsOwnTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{time.Minute, 0} {
		t.Run(timeout.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := testExporterConfig("https://scopedb.invalid")
				cfg.Timeout.Timeout = timeout
				cfg.AppendBatch.MaxRows = 1
				exp, statuses := startTestLogsExporter(t, cfg)
				exp.(*capturingLogsExporter).exp.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					// Each chunk exceeds the SDK default; together they exceed one minute.
					select {
					case <-r.Context().Done():
						return nil, r.Context().Err()
					case <-time.After(40 * time.Second):
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"status":"committed","num_rows_inserted":1}`))}, nil
					}
				})
				require.NoError(t, exp.ConsumeLogs(t.Context(), testLogs("first", "second")))
				require.Equal(t, uint64(2), statuses.Snapshot().Signals[signalLogs].ConfirmedWrittenRecords)
			})
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestPartialDeliveryFinalOutcomes(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, tc := range []struct {
			name                                    string
			status                                  int
			written, ambiguous, rejected, exhausted uint64
		}{
			{"succeeded", 200, 3, 1, 1, 0},
			{"failed", 400, 2, 1, 2, 0},
			{"exhausted", 429, 1, 1, 1, 2},
			{"ambiguous", 503, 2, 2, 1, 0},
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
							w.WriteHeader(http.StatusTooManyRequests)
							fmt.Fprint(w, `{"error":{"code":"too_many_requests","message":"busy"}}`)
							return
						}
						if tc.status != 200 {
							w.Header().Set("Retry-After", "10")
							w.WriteHeader(tc.status)
							if tc.status == 400 {
								fmt.Fprint(w, `{"error":{"code":"bad_request","message":"rejected"}}`)
							} else if tc.status == 429 {
								fmt.Fprint(w, `{"error":{"code":"too_many_requests","message":"busy"}}`)
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
					assert.Equal(c, tc.ambiguous, s.AmbiguousRecords)
					assert.Equal(c, tc.rejected, s.PermanentFailedRecords)
					assert.Equal(c, tc.exhausted, s.RetryExhaustedRecords)
					assert.Equal(c, uint64(1), s.InvalidItemsByReason[mappingReasonCastFailed])
				}, time.Second, time.Millisecond)
				mu.Lock()
				defer mu.Unlock()
				require.Equal(t, 1, attempts[1])
				require.Equal(t, 1, attempts[2])
				require.Equal(t, 2, attempts[3])
				if tc.status == 429 {
					require.Zero(t, attempts[4])
				} else {
					require.Equal(t, 1, attempts[4])
				}
			})
		}
	}
}

func TestPermanentFailureSettlesWholeBatchAndContinues(t *testing.T) {
	for name, body := range map[string]string{
		"current API":            `{"error":{"code":"bad_request","message":"invalid batch"}}`,
		"legacy row diagnostics": `{"code":"invalid_request","message":"invalid batch","details":{"status":"rejected","row_errors":[{"row_index":1,"message":"invalid row"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var batches []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request, err := decodeSDKRequestBody(r)
				require.NoError(t, err)
				batches = append(batches, string(request))
				if len(batches) == 1 {
					w.WriteHeader(400)
					fmt.Fprint(w, body)
					return
				}
				writeAppendCommitted(t, w, 2)
			}))
			t.Cleanup(server.Close)
			cfg := replayTestConfig(server.URL)
			cfg.AppendBatch.MaxRows = 2
			exp, statuses := startTestLogsExporter(t, cfg)
			err := exp.ConsumeLogs(t.Context(), testLogs("first", "invalid", "later", "last"))
			require.True(t, consumererror.IsPermanent(err), "%v", err)
			server.Close()
			require.Equal(t, []string{"{\"message\":\"first\"}\n{\"message\":\"invalid\"}\n", "{\"message\":\"later\"}\n{\"message\":\"last\"}\n"}, batches)
			s := statuses.Snapshot().Signals[signalLogs]
			require.Equal(t, uint64(2), s.ConfirmedWrittenRecords)
			require.Equal(t, uint64(2), s.PermanentFailedRecords)
			require.Zero(t, s.AmbiguousRecords)
			require.Zero(t, s.AmbiguousReplayAttempts)
		})
	}
}

func TestDeliveryRetainsBatchAcrossSafeAndAmbiguousRetries(t *testing.T) {
	var batches []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSDKRequestBody(r)
		require.NoError(t, err)
		batches = append(batches, string(body))
		switch len(batches) {
		case 1, 3:
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"code":"too_many_requests","message":"busy"}}`)
		case 2:
			http.Error(w, "lost acknowledgement", http.StatusServiceUnavailable)
		case 4:
			writeAppendCommitted(t, w, 2)
		default:
			writeAppendCommitted(t, w, 1)
		}
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	cfg.AppendBatch.MaxRows = 2
	cfg.Mappings.Logs = MappingConfig{"value": {Source: "log.message", Cast: "int"}}
	exp, statuses := startTestLogsExporter(t, cfg)
	require.NoError(t, exp.ConsumeLogs(t.Context(), testLogs("1", "invalid", "2", "3")))
	server.Close()
	require.Len(t, batches, 5)
	for _, body := range batches[:4] {
		require.Equal(t, "{\"value\":1}\n{\"value\":2}\n", body)
	}
	require.Equal(t, "{\"value\":3}\n", batches[4])
	s := statuses.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(3), s.ConfirmedWrittenRecords)
	require.Equal(t, uint64(1), s.PermanentFailedRecords)
	require.Equal(t, uint64(1), s.InvalidItemsByReason[mappingReasonCastFailed])
	require.Equal(t, uint64(2), s.AmbiguousReplayAttempts)
	require.Equal(t, uint64(2), s.AmbiguousReplayedRecords)
	require.Zero(t, s.AmbiguousRecords)
	require.Zero(t, s.RetryExhaustedRecords)
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
