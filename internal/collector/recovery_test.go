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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestPersistentQueueRecoversAfterCollectorRestart(t *testing.T) {
	for _, signal := range []string{"logs", "traces", "metrics"} {
		t.Run(signal, func(t *testing.T) {
			testPersistentQueueRecoversAfterCollectorRestart(t, signal)
		})
	}
}

func testPersistentQueueRecoversAfterCollectorRestart(t *testing.T, signal string) {
	var backendAvailable atomic.Bool
	var catalogAvailable atomic.Bool
	catalogAvailable.Store(true)
	failedAttempt := make(chan struct{}, 1)
	committed := make(chan map[string]any, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !catalogAvailable.Load() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"message":   "catalog unavailable",
					"retryable": true,
				}))
				return
			}
			writeRecoveryTableDescription(t, w, r)
		case http.MethodPost:
			if !backendAvailable.Load() {
				select {
				case failedAttempt <- struct{}{}:
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"message":      "test outage",
					"append_state": "unknown",
					"retryable":    true,
				}))
				return
			}
			body, err := decodeRecoveryAppendBody(r)
			require.NoError(t, err)
			var row map[string]any
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &row))
			select {
			case committed <- row:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"append_state":      "committed",
				"num_rows_inserted": 1,
			}))
		default:
			t.Fatalf("unexpected ScopeDB request method %s", r.Method)
		}
	}))
	defer backend.Close()

	queueDir := filepath.Join(t.TempDir(), "queue")
	otlpAddress := freeTCPAddress(t)
	config := recoveryCollectorConfig(backend.URL, otlpAddress, queueDir, signal)
	value := signal + " survives restart"

	first := startRecoveryCollector(t, config)
	defer first.stop(t)
	require.NoError(t, sendRecoverySignal(otlpAddress, signal, value))
	select {
	case <-failedAttempt:
	case <-time.After(5 * time.Second):
		t.Fatal("ScopeDB append was not attempted during outage")
	}
	first.stop(t)
	require.True(t, directoryHasFiles(t, queueDir), "persistent queue directory should retain state")

	catalogAvailable.Store(false)
	second := startRecoveryCollector(t, config)
	defer second.stop(t)
	backendAvailable.Store(true)
	catalogAvailable.Store(true)
	select {
	case row := <-committed:
		assert.Equal(t, map[string]any{"value": value}, row)
	case <-time.After(10 * time.Second):
		t.Fatal("queued OTLP record was not appended after restart and backend recovery")
	}
}

func TestByteSizedQueueRefusesItemOverCapacity(t *testing.T) {
	for _, signal := range []string{"logs", "traces", "metrics"} {
		t.Run(signal, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeRecoveryTableDescription(t, w, r)
					return
				}
				t.Errorf("append should not be attempted when the item exceeds queue capacity")
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer backend.Close()

			address := freeTCPAddress(t)
			config := strings.Replace(
				recoveryCollectorConfig(backend.URL, address, filepath.Join(t.TempDir(), "queue"), signal),
				"queue_size: 1048576",
				"queue_size: 1",
				1,
			)
			runtime := startRecoveryCollector(t, config)
			defer runtime.stop(t)

			err := sendRecoverySignal(address, signal, signal+" larger than one byte")
			require.Error(t, err)
			assert.ErrorContains(t, err, "503 Service Unavailable")
		})
	}
}

type recoveryCollector struct {
	collector *otelcol.Collector
	done      chan error
	once      sync.Once
	stopped   chan struct{}
	stopErr   error
}

func startRecoveryCollector(t *testing.T, config string) *recoveryCollector {
	t.Helper()
	instance, err := otelcol.NewCollector(Settings("yaml:"+config, "test"))
	require.NoError(t, err)
	runtime := &recoveryCollector{
		collector: instance,
		done:      make(chan error, 1),
		stopped:   make(chan struct{}),
	}
	go func() {
		runtime.done <- instance.Run(context.Background())
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-runtime.done:
			t.Fatalf("collector stopped before becoming ready: %v", err)
		case <-deadline.C:
			t.Fatal("collector did not become ready")
		case <-ticker.C:
			if instance.GetState() == otelcol.StateRunning {
				return runtime
			}
		}
	}
}

func (r *recoveryCollector) stop(t *testing.T) {
	t.Helper()
	r.once.Do(func() {
		r.collector.Shutdown()
		select {
		case r.stopErr = <-r.done:
		case <-time.After(5 * time.Second):
			r.stopErr = fmt.Errorf("collector did not stop")
		}
		close(r.stopped)
	})
	<-r.stopped
	require.NoError(t, r.stopErr)
}

func recoveryCollectorConfig(scopeDBEndpoint string, otlpAddress string, queueDir string, signal string) string {
	return fmt.Sprintf(`
extensions:
  file_storage:
    directory: %q
    create_directory: true
receivers:
  otlp:
    protocols:
      http:
        endpoint: %q
exporters:
  scopedb:
    endpoint: %q
    api_key: test-key
    compression: zstd
    timeout: 1s
    tables:
      logs: public.recovery_logs
      traces: public.recovery_traces
      metrics: public.recovery_metrics
    mappings:
      logs:
        value: log.message
      traces:
        value: span.name
      metrics:
        value: metric.name
    sending_queue:
      enabled: true
      storage: file_storage
      sizer: bytes
      queue_size: 1048576
      num_consumers: 1
    retry_on_failure:
      enabled: true
      initial_interval: 1s
      max_interval: 1s
      max_elapsed_time: 0s
service:
  extensions: [file_storage]
  telemetry:
    logs:
      level: fatal
    metrics:
      level: none
  pipelines:
    %s:
      receivers: [otlp]
      exporters: [scopedb]
`, queueDir, otlpAddress, scopeDBEndpoint, signal)
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().String()
}

func sendRecoverySignal(address string, signal string, value string) error {
	payload, err := marshalRecoverySignal(signal, value)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/v1/"+signal, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("OTLP HTTP returned %s: %s", response.Status, body)
	}
	return nil
}

func marshalRecoverySignal(signal string, value string) ([]byte, error) {
	now := pcommon.NewTimestampFromTime(time.Now())
	switch signal {
	case "logs":
		logs := plog.NewLogs()
		record := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.SetTimestamp(now)
		record.Body().SetStr(value)
		return plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	case "traces":
		traces := ptrace.NewTraces()
		span := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		span.SetName(value)
		span.SetStartTimestamp(now)
		span.SetEndTimestamp(now + 1)
		return ptraceotlp.NewExportRequestFromTraces(traces).MarshalProto()
	case "metrics":
		metrics := pmetric.NewMetrics()
		metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		metric.SetName(value)
		point := metric.SetEmptyGauge().DataPoints().AppendEmpty()
		point.SetTimestamp(now)
		point.SetIntValue(1)
		return pmetricotlp.NewExportRequestFromMetrics(metrics).MarshalProto()
	default:
		return nil, fmt.Errorf("unsupported recovery signal %q", signal)
	}
}

func writeRecoveryTableDescription(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	name := strings.TrimPrefix(r.URL.Path, "/v1/databases/scopedb/schemas/public/tables/")
	require.Contains(t, []string{"recovery_logs", "recovery_traces", "recovery_metrics"}, name)
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"database":     "scopedb",
		"schema":       "public",
		"name":         name,
		"columns":      []map[string]any{{"name": "value", "data_type": "string"}},
		"partition_by": []string{},
		"cluster_by":   []string{},
		"distinct_on":  map[string]any{"on": []string{}, "by": []string{}},
	}))
}

func decodeRecoveryAppendBody(r *http.Request) ([]byte, error) {
	if r.Header.Get("Content-Encoding") != "zstd" {
		return nil, fmt.Errorf("unexpected content encoding %q", r.Header.Get("Content-Encoding"))
	}
	decoder, err := zstd.NewReader(r.Body)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	return io.ReadAll(decoder)
}

func directoryHasFiles(t *testing.T, directory string) bool {
	t.Helper()
	found := false
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != directory && !entry.IsDir() {
			found = true
		}
		return nil
	}))
	return found
}
