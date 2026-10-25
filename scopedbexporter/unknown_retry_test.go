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

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/plog"
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

func replayTestLogs(values ...string) plog.Logs {
	logs := plog.NewLogs()
	rows := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for _, value := range values {
		rows.AppendEmpty().Body().SetStr(value)
	}
	return logs
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
				decoder, err := zstd.NewReader(r.Body)
				require.NoError(t, err)
				body, err := io.ReadAll(decoder)
				decoder.Close()
				require.NoError(t, err)
				mu.Lock()
				bodies = append(bodies, string(body))
				mu.Unlock()
				i := min(int(calls.Add(1))-1, len(tc.statuses)-1)
				w.WriteHeader(tc.statuses[i])
				_, _ = io.WriteString(w, tc.bodies[i])
			}))
			defer server.Close()
			cfg := replayTestConfig(server.URL)
			registry := NewStatusRegistry()
			exp, err := NewFactoryWithRegistries(registry, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
			require.NoError(t, err)
			require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
			err = exp.ConsumeLogs(t.Context(), replayTestLogs("same encoded record"))
			if tc.unknown > 0 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, exp.Shutdown(context.Background()))
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
		reader, err := zstd.NewReader(r.Body)
		require.NoError(t, err)
		defer reader.Close()
		body, err := io.ReadAll(reader)
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
	defer server.Close()
	cfg := replayTestConfig(server.URL)
	cfg.AppendBatch.MaxRows = 1
	registry := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(registry, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	require.NoError(t, exp.ConsumeLogs(t.Context(), replayTestLogs("committed", "unknown", "suffix")))
	require.NoError(t, exp.Shutdown(context.Background()))
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
	defer server.Close()
	cfg := replayTestConfig(server.URL)
	cfg.AppendBatch.MaxRows = 1
	cfg.UnknownRetry.MaxRetries = 1
	registry := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(registry, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	require.Error(t, exp.ConsumeLogs(t.Context(), replayTestLogs("first", "second")))
	require.NoError(t, exp.Shutdown(context.Background()))
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
			defer server.Close()
			cfg := replayTestConfig(server.URL)
			cfg.AppendBatch.MaxRows = 1
			cfg.RetryOnFailure.MaxElapsedTime = time.Second
			if queued {
				cfg.SendingQueue = configoptional.Some(exporterhelper.NewDefaultQueueConfig())
			}
			registry := NewStatusRegistry()
			exp, err := NewFactoryWithRegistries(registry, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
			require.NoError(t, err)
			require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
			err = exp.ConsumeLogs(t.Context(), replayTestLogs("unknown", "unsent"))
			if queued {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Eventually(t, func() bool { return registry.Snapshot().Signals[signalLogs].UnknownRecords == 1 }, time.Second, time.Millisecond)
			require.NoError(t, exp.Shutdown(context.Background()))
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
	defer server.Close()
	cfg := replayTestConfig(server.URL)
	cfg.UnknownRetry.MaxElapsedTime = 20 * time.Millisecond
	registry := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(registry, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	require.Error(t, exp.ConsumeLogs(t.Context(), replayTestLogs("expires during backoff")))
	require.NoError(t, exp.Shutdown(context.Background()))
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
	defer server.Close()
	cfg := replayTestConfig(server.URL)
	registry := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(registry, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	require.Error(t, exp.ConsumeLogs(ctx, replayTestLogs(strings.Repeat("cancelled", 100))))
	require.NoError(t, exp.Shutdown(context.Background()))
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
	defer server.Close()
	cfg := replayTestConfig(server.URL)
	cfg.UnknownRetry.MaxElapsedTime = 200 * time.Millisecond
	registry := NewStatusRegistry()
	exp, err := NewFactoryWithRegistries(registry, nil).CreateLogs(t.Context(), exportertest.NewNopSettings(typeStr), cfg)
	require.NoError(t, err)
	require.NoError(t, exp.Start(t.Context(), componenttest.NewNopHost()))
	require.Error(t, exp.ConsumeLogs(t.Context(), replayTestLogs("bounded attempt")))
	require.NoError(t, exp.Shutdown(context.Background()))
	require.Equal(t, int32(2), calls.Load())
	state := registry.Snapshot().Signals[signalLogs]
	require.Equal(t, uint64(1), state.UnknownRecords)
	require.Equal(t, uint64(1), state.UnknownReplayAttempts)
	require.Zero(t, state.RetryExhaustedRecords)
}
