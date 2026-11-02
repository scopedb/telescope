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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

func replayTestConfig(endpoint string) *Config {
	cfg := testExporterConfig(endpoint)
	cfg.RetryOnFailure.Enabled = true
	cfg.RetryOnFailure.InitialInterval = time.Millisecond
	cfg.RetryOnFailure.MaxInterval = time.Millisecond
	cfg.RetryOnFailure.MaxElapsedTime = 0
	cfg.AmbiguousRetry = AmbiguousRetryConfig{Enabled: true, MaxRetries: 2, MaxElapsedTime: time.Minute}
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	return cfg
}

func TestAmbiguousReplayPreservesFinalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		statuses                     []int
		bodies                       []string
		calls                        int32
		written, ambiguous, replayed uint64
	}{
		{"recover", []int{503, 200}, []string{"unavailable", `{"num_rows_inserted":1}`}, 2, 1, 0, 1},
		{"explicit server refusal", []int{503, 200}, []string{`{"error":{"code":"service_unavailable","message":"do not retry","retryable":false}}`, `{"num_rows_inserted":1}`}, 1, 0, 1, 0},
		{"refusal after ambiguous", []int{503, 503, 200}, []string{"unavailable", `{"error":{"code":"service_unavailable","message":"do not retry","retryable":false}}`, `{"num_rows_inserted":1}`}, 2, 0, 1, 1},
		{"reject after ambiguous", []int{503, 400}, []string{"unavailable", `{"error":{"code":"bad_request","message":"rejected"}}`}, 2, 0, 1, 1},
		{"exhausted", []int{503}, []string{"unavailable"}, 3, 0, 1, 1},
		{"malformed acknowledgement", []int{200, 200}, []string{"{}", `{"num_rows_inserted":1}`}, 2, 1, 0, 1},
		{"attempt timeout", []int{0, 200}, []string{"", `{"num_rows_inserted":1}`}, 1, 0, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var bodies []string
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := decodeSDKRequestBody(r)
				require.NoError(t, err)
				mu.Lock()
				bodies = append(bodies, string(body))
				mu.Unlock()
				i := min(int(calls.Add(1))-1, len(tc.statuses)-1)
				if tc.statuses[i] == 0 {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(tc.statuses[i])
				_, _ = io.WriteString(w, tc.bodies[i])
			}))
			t.Cleanup(server.Close)
			cfg := replayTestConfig(server.URL)
			cfg.Timeout.Timeout = 100 * time.Millisecond
			exp, registry := startTestLogsExporter(t, cfg)
			err := exp.ConsumeLogs(t.Context(), testLogs("same encoded record"))
			if tc.ambiguous > 0 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.calls, calls.Load())
			mu.Lock()
			for _, body := range bodies {
				require.Equal(t, bodies[0], body)
			}
			mu.Unlock()
			s := registry.Snapshot().Signals[signalLogs]
			require.Equal(t, tc.written, s.ConfirmedWrittenRecords)
			require.Equal(t, tc.ambiguous, s.AmbiguousRecords)
			require.Equal(t, uint64(tc.calls-1), s.AmbiguousReplayAttempts)
			require.Equal(t, tc.replayed, s.AmbiguousReplayedRecords)
			require.Zero(t, s.PermanentFailedRecords)
			require.Zero(t, s.RetryExhaustedRecords)
		})
	}
}

func TestAmbiguousReplayKeepsCommittedChunksOutOfLaterRetries(t *testing.T) {
	attempts := map[string]int{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeSDKRequestBody(r)
		require.NoError(t, err)
		var row map[string]string
		require.NoError(t, json.Unmarshal(body, &row))
		mu.Lock()
		defer mu.Unlock()
		value := row["message"]
		attempts[value]++
		switch {
		case value == "ambiguous" && attempts[value] <= 2:
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "unavailable")
		case value == "suffix" && attempts[value] == 1:
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"code":"too_many_requests","message":"busy"}}`)
		default:
			writeAppendCommitted(t, w, 1)
		}
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	cfg.AppendBatch.MaxRows = 1
	exp, registry := startTestLogsExporter(t, cfg)
	require.NoError(t, exp.ConsumeLogs(t.Context(), testLogs("committed", "ambiguous", "suffix")))

	mu.Lock()
	require.Equal(t, map[string]int{"committed": 1, "ambiguous": 3, "suffix": 2}, attempts)
	mu.Unlock()
	state := registry.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(3), state.ConfirmedWrittenRecords)
	require.Equal(t, uint64(2), state.AmbiguousReplayAttempts)
	require.Equal(t, uint64(1), state.AmbiguousReplayedRecords)
	require.Zero(t, state.AmbiguousRecords)
}

func TestAmbiguousReplayBudgetSharesAllChunks(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			writeAppendCommitted(t, w, 1)
			return
		}
		w.WriteHeader(503)
		_, _ = io.WriteString(w, "unavailable")
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	cfg.AppendBatch.MaxRows = 1
	cfg.AmbiguousRetry.MaxRetries = 1
	exp, registry := startTestLogsExporter(t, cfg)
	require.Error(t, exp.ConsumeLogs(t.Context(), testLogs("first", "second")))
	require.Equal(t, int32(3), calls.Load())
	state := registry.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(1), state.ConfirmedWrittenRecords)
	require.Equal(t, uint64(1), state.AmbiguousRecords)
	require.Equal(t, uint64(1), state.AmbiguousReplayAttempts)
}

func TestOrdinaryRetryExhaustionKeepsAmbiguousSeparate(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "memory"}[queued], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(503)
				_, _ = io.WriteString(w, "unavailable")
			}))
			t.Cleanup(server.Close)
			cfg := replayTestConfig(server.URL)
			cfg.AppendBatch.MaxRows = 1
			cfg.RetryOnFailure.MaxElapsedTime = time.Second
			if queued {
				cfg.SendingQueue = configoptional.Some(exporterhelper.NewDefaultQueueConfig())
			}
			exp, registry := startTestLogsExporter(t, cfg)
			err := exp.ConsumeLogs(t.Context(), testLogs("ambiguous", "unsent"))
			if queued {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Eventually(t, func() bool {
				s := registry.Snapshot().Signals[signalLogs]
				return s.AmbiguousRecords == 1 && s.RetryExhaustedRecords == 1
			}, time.Second, time.Millisecond)
			require.Equal(t, int32(1), calls.Load())
			state := registry.Snapshot().Signals[signalLogs]
			require.Equal(t, uint64(1), state.AmbiguousRecords)
			require.Equal(t, uint64(1), state.RetryExhaustedRecords)
			require.Zero(t, state.AmbiguousReplayAttempts)
			require.Zero(t, state.PermanentFailedRecords)
		})
	}
}

func TestAmbiguousReplayHonorsRetryAfterAndElapsedLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(503)
		_, _ = io.WriteString(w, "unavailable")
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	cfg.AmbiguousRetry.MaxElapsedTime = 20 * time.Millisecond
	exp, registry := startTestLogsExporter(t, cfg)
	require.Error(t, exp.ConsumeLogs(t.Context(), testLogs("expires during backoff")))
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, uint64(1), registry.Snapshot().Signals[signalLogs].AmbiguousRecords)
}

func TestAmbiguousReplayStopsAtCallerCancellationOrDeadline(t *testing.T) {
	for _, ending := range []string{"cancel", "deadline"} {
		t.Run(ending, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls atomic.Int32
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				cfg := replayTestConfig("https://scopedb.invalid")
				cfg.Timeout.Timeout = 2 * time.Second
				exp, registry := startTestLogsExporter(t, cfg)
				exp.(*capturingLogsExporter).exp.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls.Add(1)
					if ending == "cancel" {
						cancel()
					}
					<-r.Context().Done()
					return nil, r.Context().Err()
				})
				require.ErrorIs(t, exp.ConsumeLogs(ctx, testLogs("interrupted")), ctx.Err())
				require.Equal(t, int32(1), calls.Load())
				state := registry.Snapshot().Signals[signalLogs]
				require.Zero(t, state.AmbiguousReplayAttempts)
				require.Equal(t, uint64(1), state.AmbiguousRecords)
			})
		})
	}
}

func TestAmbiguousReplayBoundsAnInFlightAttempt(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "unavailable")
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	cfg.AmbiguousRetry.MaxElapsedTime = 200 * time.Millisecond
	exp, registry := startTestLogsExporter(t, cfg)
	require.Error(t, exp.ConsumeLogs(t.Context(), testLogs("bounded attempt")))
	require.Equal(t, int32(2), calls.Load())
	state := registry.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(1), state.AmbiguousRecords)
	require.Equal(t, uint64(1), state.AmbiguousReplayAttempts)
	require.Zero(t, state.RetryExhaustedRecords)
}

func TestAmbiguousReplayUsesBudgetRemainingAfterRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := replayTestConfig("https://scopedb.invalid")
		cfg.AmbiguousRetry.MaxElapsedTime = 1500 * time.Millisecond
		exp, registry := startTestLogsExporter(t, cfg)
		calls := 0
		exp.(*capturingLogsExporter).exp.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Retry-After": []string{"1"}}, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"num_rows_inserted":1}`))}, nil
		})
		started := time.Now()
		require.NoError(t, exp.ConsumeLogs(t.Context(), testLogs("retry after one second")))
		require.Equal(t, 2, calls)
		require.GreaterOrEqual(t, time.Since(started), time.Second)
		require.Less(t, time.Since(started), cfg.AmbiguousRetry.MaxElapsedTime)
		s := registry.Snapshot().Signals[signalLogs]
		require.Equal(t, uint64(1), s.ConfirmedWrittenRecords)
		require.Zero(t, s.AmbiguousRecords)
	})
}
