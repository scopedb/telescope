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
	cfg.UnknownRetry = UnknownRetryConfig{Enabled: true, MaxRetries: 2, MaxElapsedTime: time.Minute}
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	return cfg
}

func TestUnknownReplayPreservesFinalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		statuses                   []int
		bodies                     []string
		calls                      int32
		written, unknown, replayed uint64
	}{
		{"recover", []int{503, 200}, []string{"unavailable", `{"status":"committed","num_rows_inserted":1}`}, 2, 1, 0, 1},
		{"reject after unknown", []int{503, 400}, []string{"unavailable", `{"details":{"status":"rejected","row_errors":[{"row_index":0,"message":"invalid"}]}}`}, 2, 0, 1, 1},
		{"exhausted", []int{503}, []string{"unavailable"}, 3, 0, 1, 1},
		{"malformed acknowledgement", []int{200, 200}, []string{"{}", `{"status":"committed","num_rows_inserted":1}`}, 2, 1, 0, 1},
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
				w.WriteHeader(tc.statuses[i])
				_, _ = io.WriteString(w, tc.bodies[i])
			}))
			t.Cleanup(server.Close)
			cfg := replayTestConfig(server.URL)
			exp, registry := startTestLogsExporter(t, cfg)
			err := exp.ConsumeLogs(t.Context(), testLogs("same encoded record"))
			if tc.unknown > 0 {
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
			require.Equal(t, tc.unknown, s.UnknownRecords)
			require.Equal(t, uint64(tc.calls-1), s.UnknownReplayAttempts)
			require.Equal(t, tc.replayed, s.UnknownReplayedRecords)
			require.Zero(t, s.PermanentFailedRecords)
			require.Zero(t, s.RetryExhaustedRecords)
		})
	}
}

func TestUnknownReplayKeepsCommittedChunksOutOfLaterRetries(t *testing.T) {
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
		case value == "unknown" && attempts[value] <= 2:
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "unavailable")
		case value == "suffix" && attempts[value] == 1:
			w.WriteHeader(429)
			_, _ = io.WriteString(w, "busy")
		default:
			writeAppendCommitted(t, w, 1)
		}
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	cfg.AppendBatch.MaxRows = 1
	exp, registry := startTestLogsExporter(t, cfg)
	require.NoError(t, exp.ConsumeLogs(t.Context(), testLogs("committed", "unknown", "suffix")))

	mu.Lock()
	require.Equal(t, map[string]int{"committed": 1, "unknown": 3, "suffix": 2}, attempts)
	mu.Unlock()
	state := registry.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(3), state.ConfirmedWrittenRecords)
	require.Equal(t, uint64(2), state.UnknownReplayAttempts)
	require.Equal(t, uint64(1), state.UnknownReplayedRecords)
	require.Zero(t, state.UnknownRecords)
}

func TestUnknownReplayBudgetSharesAllChunks(t *testing.T) {
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
	cfg.UnknownRetry.MaxRetries = 1
	exp, registry := startTestLogsExporter(t, cfg)
	require.Error(t, exp.ConsumeLogs(t.Context(), testLogs("first", "second")))
	require.Equal(t, int32(3), calls.Load())
	state := registry.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(1), state.ConfirmedWrittenRecords)
	require.Equal(t, uint64(1), state.UnknownRecords)
	require.Equal(t, uint64(1), state.UnknownReplayAttempts)
}

func TestOrdinaryRetryExhaustionKeepsUnknownSeparate(t *testing.T) {
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
			err := exp.ConsumeLogs(t.Context(), testLogs("unknown", "unsent"))
			if queued {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Eventually(t, func() bool {
				s := registry.Snapshot().Signals[signalLogs]
				return s.UnknownRecords == 1 && s.RetryExhaustedRecords == 1
			}, time.Second, time.Millisecond)
			require.Equal(t, int32(1), calls.Load())
			state := registry.Snapshot().Signals[signalLogs]
			require.Equal(t, uint64(1), state.UnknownRecords)
			require.Equal(t, uint64(1), state.RetryExhaustedRecords)
			require.Zero(t, state.UnknownReplayAttempts)
			require.Zero(t, state.PermanentFailedRecords)
		})
	}
}

func TestUnknownReplayHonorsRetryAfterAndElapsedLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(503)
		_, _ = io.WriteString(w, "unavailable")
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	cfg.UnknownRetry.MaxElapsedTime = 20 * time.Millisecond
	exp, registry := startTestLogsExporter(t, cfg)
	require.Error(t, exp.ConsumeLogs(t.Context(), testLogs("expires during backoff")))
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, uint64(1), registry.Snapshot().Signals[signalLogs].UnknownRecords)
}

func TestUnknownReplayDoesNotRetryClientCancellation(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	cfg := replayTestConfig(server.URL)
	exp, registry := startTestLogsExporter(t, cfg)
	require.Error(t, exp.ConsumeLogs(ctx, testLogs(strings.Repeat("cancelled", 100))))
	require.Equal(t, int32(1), calls.Load())
	require.Zero(t, registry.Snapshot().Signals[signalLogs].UnknownReplayAttempts)
	require.Equal(t, uint64(1), registry.Snapshot().Signals[signalLogs].UnknownRecords)
}

func TestUnknownReplayBoundsAnInFlightAttempt(t *testing.T) {
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
	cfg.UnknownRetry.MaxElapsedTime = 200 * time.Millisecond
	exp, registry := startTestLogsExporter(t, cfg)
	require.Error(t, exp.ConsumeLogs(t.Context(), testLogs("bounded attempt")))
	require.Equal(t, int32(2), calls.Load())
	state := registry.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(1), state.UnknownRecords)
	require.Equal(t, uint64(1), state.UnknownReplayAttempts)
	require.Zero(t, state.RetryExhaustedRecords)
}
