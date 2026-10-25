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

package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

// Set TELESCOPE_TEST_OUTAGE=15m for the wall-clock outage acceptance run.
func TestPersistentQueueRecoversUnderSharedRateLimit(t *testing.T) {
	outage := 100 * time.Millisecond
	if raw := os.Getenv("TELESCOPE_TEST_OUTAGE"); raw != "" {
		var err error
		outage, err = time.ParseDuration(raw)
		require.NoError(t, err)
	}
	// Accumulate three minutes at 2,400 records/second before recovery.
	const batches = 54
	const batchRows = 8000
	const total = batches * batchRows
	var mu sync.Mutex
	available := false
	seen := make([]bool, total)
	committed := 0
	duplicates := 0
	throttled := 0
	attempts := 0
	window := time.Now()
	// Other writers occupy 16 of a shared 20 requests/second budget.
	used := 16
	notBefore := time.Time{}
	var timingError bool
	failed := make(chan struct{}, 1)
	drained := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method, "runtime startup must not query the catalog")
		body, err := decodeRecoveryAppendBody(r)
		require.NoError(t, err)
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		attempts++
		if now.Before(notBefore) {
			timingError = true
		}
		w.Header().Set("Content-Type", "application/json")
		if !available {
			select {
			case failed <- struct{}{}:
			default:
			}
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"message":"temporary outage","retryable":true,"details":{"status":"rejected"}}`)
			return
		}
		if now.Sub(window) >= time.Second {
			window = now
			used = 16
		}
		if used == 20 {
			throttled++
			notBefore = now.Add(time.Second)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"message":"shared rate limit","retryable":true,"details":{"status":"rejected"}}`)
			return
		}
		used++
		lines := bytes.Split(bytes.TrimSpace(body), []byte{'\n'})
		require.LessOrEqual(t, len(lines), batchRows)
		require.LessOrEqual(t, len(body), 4<<20)
		for _, line := range lines {
			var row struct {
				Value string `json:"value"`
			}
			require.NoError(t, json.Unmarshal(line, &row))
			index, err := strconv.Atoi(row.Value)
			require.NoError(t, err)
			require.True(t, index >= 0 && index < total)
			if seen[index] {
				duplicates++
			} else {
				seen[index] = true
				committed++
			}
		}
		fmt.Fprintf(w, `{"status":"committed","num_rows_inserted":%d}`, len(lines))
		if committed == total {
			select {
			case <-drained:
			default:
				close(drained)
			}
		}
	}))
	defer backend.Close()
	address := freeTCPAddress(t)
	config := recoveryCollectorConfig(backend.URL, address, t.TempDir(), "logs")
	config = strings.Replace(config, "queue_size: 1048576", "queue_size: 67108864", 1)
	config = strings.Replace(config, "initial_interval: 1s", "initial_interval: 1ms", 1)
	config = strings.Replace(config, "max_interval: 1s", "max_interval: 1ms", 1)
	config = strings.Replace(config, "compression: zstd", "compression: zstd\n    append_batch:\n      max_rows: 8000\n      max_bytes: 4194304", 1)
	config = strings.Replace(config, "create_directory: true", "create_directory: true\n    fsync: true", 1)
	runtime := startRecoveryCollector(t, config)
	defer runtime.stop(t)
	for batch := 0; batch < batches; batch++ {
		logs := plog.NewLogs()
		records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
		records.EnsureCapacity(batchRows)
		for row := 0; row < batchRows; row++ {
			records.AppendEmpty().Body().SetStr(strconv.Itoa(batch*batchRows + row))
		}
		body, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+address+"/v1/logs", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-protobuf")
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		require.NoError(t, err)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	select {
	case <-failed:
	case <-time.After(5 * time.Second):
		t.Fatal("outage was not exercised")
	}
	runtime.stop(t)
	runtime = startRecoveryCollector(t, config)
	defer runtime.stop(t)
	timer := time.NewTimer(outage)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test cancelled during outage")
	}
	mu.Lock()
	available = true
	mu.Unlock()
	select {
	case <-drained:
	case <-time.After(30 * time.Second):
		t.Fatal("queue did not drain after recovery")
	}
	runtime.stop(t)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, total, committed)
	require.Zero(t, duplicates)
	require.Positive(t, throttled)
	require.False(t, timingError, "Retry-After was ignored")
	t.Logf("outage=%s rows=%d committed=%d duplicates=%d throttles=%d attempts=%d", outage, total, committed, duplicates, throttled, attempts)
}
