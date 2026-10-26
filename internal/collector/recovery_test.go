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
	"compress/gzip"
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
	"github.com/scopedb/telescope/scopedbexporter"
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

func TestPersistentQueueV1FixtureRemainsReadable(t *testing.T) {
	queueDir := restoreQueueFixture(t, filepath.Join("testdata", "queue", "v1"))
	committed := make(chan map[string]any, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeRecoveryAppendBody(r)
		require.NoError(t, err)
		var row map[string]any
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &row))
		committed <- row
		writeRecoveryAppendCommitted(t, w)
	}))
	defer backend.Close()

	for _, signal := range []string{"logs", "traces", "metrics"} {
		runtime := startRecoveryCollector(t, recoveryCollectorConfig(
			backend.URL,
			freeTCPAddress(t),
			queueDir,
			signal,
		))
		select {
		case row := <-committed:
			assert.Equal(t, map[string]any{"value": "queue-v1-" + signal}, row)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s v1 queue fixture was not appended", signal)
		}
		runtime.stop(t)

		restarted := startRecoveryCollector(t, recoveryCollectorConfig(
			backend.URL,
			freeTCPAddress(t),
			queueDir,
			signal,
		))
		select {
		case row := <-committed:
			t.Fatalf("%s v1 queue fixture was appended twice: %#v", signal, row)
		case <-time.After(200 * time.Millisecond):
		}
		restarted.stop(t)
	}
}

func testPersistentQueueRecoversAfterCollectorRestart(t *testing.T, signal string) {
	var backendAvailable atomic.Bool
	failedAttempt := make(chan struct{}, 1)
	committed := make(chan map[string]any, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		if !backendAvailable.Load() {
			select {
			case failedAttempt <- struct{}{}:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"message":   "test outage",
				"details":   map[string]any{"status": "rejected"},
				"retryable": true,
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
			"status":            "committed",
			"num_rows_inserted": 1,
		}))
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

	second := startRecoveryCollector(t, config)
	defer second.stop(t)
	backendAvailable.Store(true)
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
			statuses := scopedbexporter.NewStatusRegistry()
			runtime := startCollectorWithSettings(t, newSettings("yaml:"+config, "test", true, statuses, scopedbexporter.NewCaptureRegistry()))
			defer runtime.stop(t)

			err := sendRecoverySignal(address, signal, signal+" larger than one byte")
			require.Error(t, err)
			assert.ErrorContains(t, err, "503 Service Unavailable")
			assert.Equal(t, uint64(1), statuses.Snapshot().Signals[signal].EnqueueFailedRecords)
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
	return startCollectorFromURI(t, "yaml:"+config)
}

func startCollectorFromURI(t *testing.T, configURI string) *recoveryCollector {
	t.Helper()
	return startCollectorWithSettings(t, Settings(configURI, "test"))
}

func startCollectorWithSettings(t *testing.T, settings otelcol.CollectorSettings) *recoveryCollector {
	t.Helper()
	instance, err := otelcol.NewCollector(settings)
	require.NoError(t, err)
	return startCollectorInstance(t, instance)
}

func startCollectorInstance(t *testing.T, instance *otelcol.Collector) *recoveryCollector {
	t.Helper()
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

func TestShutdownBudgetPreservesInFlightFileRequestsAcrossSignals(t *testing.T) {
	var available atomic.Bool
	entered := make(chan struct{}, 3)
	committed := make(chan string, 6)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeRecoveryAppendBody(r)
		require.NoError(t, err)
		if !available.Load() {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		var row map[string]string
		require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &row))
		committed <- row["value"]
		writeRecoveryAppendCommitted(t, w)
	}))
	t.Cleanup(func() { backend.CloseClientConnections(); backend.Close() })
	address := freeTCPAddress(t)
	config := recoveryCollectorConfig(backend.URL, address, t.TempDir(), "logs") + `
    traces: {receivers: [otlp], exporters: [scopedb]}
    metrics: {receivers: [otlp], exporters: [scopedb]}
`
	config = strings.Replace(config, "timeout: 1s", "timeout: 1m", 1)
	statuses := scopedbexporter.NewStatusRegistry()
	runtime, err := New("yaml:"+config, "test", statuses, nil)
	require.NoError(t, err)
	first := startCollectorInstance(t, runtime.Collector)
	var want []string
	for _, signal := range []string{"logs", "traces", "metrics"} {
		for _, value := range []string{signal + " in flight", signal + " queued"} {
			require.NoError(t, sendRecoverySignal(address, signal, value))
			want = append(want, value)
		}
	}
	for range 3 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("not all signals started an append")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	runtime.Shutdown(ctx)
	first.stop(t)
	require.Less(t, time.Since(started), 2*time.Second)
	for _, state := range statuses.Snapshot().Signals {
		require.Zero(t, state.UnknownRecords+state.RetryExhaustedRecords)
	}
	available.Store(true)
	restarted := startRecoveryCollector(t, config)
	defer restarted.stop(t)
	var got []string
	for range want {
		select {
		case value := <-committed:
			got = append(got, value)
		case <-time.After(5 * time.Second):
			t.Fatal("interrupted request was not restored")
		}
	}
	require.ElementsMatch(t, want, got)
}

func TestShutdownBudgetIncludesFinalMetricsPush(t *testing.T) {
	var pushed atomic.Bool
	gateway := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		pushed.Store(true)
		<-r.Context().Done()
	}))
	t.Cleanup(func() { gateway.CloseClientConnections(); gateway.Close() })
	config := defaultConfig()
	config.Destination = DestinationConfig{Endpoint: "https://scopedb.invalid", APIKey: "test-key"}
	config.Receivers = ReceiverConfig{GRPCAddr: freeTCPAddress(t), HTTPAddr: freeTCPAddress(t)}
	config.Delivery.Queue.Type, config.Delivery.Queue.MaxBytes = "memory", 1<<20
	config.Telemetry.MetricsAddr = freeTCPAddress(t)
	config.Telemetry.OTLP.Endpoint = gateway.URL
	config.Telemetry.OTLP.Interval, config.Telemetry.OTLP.Timeout = time.Hour, time.Minute
	config.Signals.Logs = scopedbexporter.SignalIngestionConfig{Table: "logs", Mapping: scopedbexporter.MappingConfig{"value": {Source: "log.message"}}}
	uri, err := ConfigURI(config)
	require.NoError(t, err)
	runtime, err := New(uri, "test", scopedbexporter.NewStatusRegistry(), nil)
	require.NoError(t, err)
	first := startCollectorInstance(t, runtime.Collector)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	runtime.Shutdown(ctx)
	select {
	case err := <-first.done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("final metrics push exceeded the shutdown budget")
	}
	require.Less(t, time.Since(started), 2*time.Second)
	require.True(t, pushed.Load())
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
	_, err = io.Copy(io.Discard, response.Body)
	return err
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

func writeRecoveryAppendCommitted(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"status":            "committed",
		"num_rows_inserted": 1,
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

func restoreQueueFixture(t *testing.T, sourceDir string) string {
	t.Helper()
	destinationDir := t.TempDir()
	entries, err := os.ReadDir(sourceDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".gz") {
			continue
		}
		compressed, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		require.NoError(t, err)
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.NoError(t, os.WriteFile(
			filepath.Join(destinationDir, strings.TrimSuffix(entry.Name(), ".gz")),
			data,
			0o600,
		))
	}
	return destinationDir
}

func TestConnectionRefusalKeepsPersistentQueueUntilRecovery(t *testing.T) {
	backendAddress, receiverAddress := freeTCPAddress(t), freeTCPAddress(t)
	statuses := scopedbexporter.NewStatusRegistry()
	config := recoveryCollectorConfig("http://"+backendAddress, receiverAddress, t.TempDir(), "logs")
	runtime := startCollectorWithSettings(t, newSettings("yaml:"+config, "test", true, statuses, scopedbexporter.NewCaptureRegistry()))
	defer runtime.stop(t)
	require.NoError(t, sendRecoverySignal(receiverAddress, "logs", "retained during connection outage"))
	require.Eventually(t, func() bool { return statuses.Snapshot().Signals["logs"].LastError != "" }, 5*time.Second, 10*time.Millisecond)
	state := statuses.Snapshot().Signals["logs"]
	require.Zero(t, state.UnknownRecords)
	require.Zero(t, state.PermanentFailedRecords)
	require.Zero(t, state.RetryExhaustedRecords)

	listener, err := net.Listen("tcp", backendAddress)
	require.NoError(t, err)
	committed := make(chan struct{}, 1)
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeRecoveryAppendBody(r)
		require.NoError(t, err)
		require.Contains(t, string(body), "retained during connection outage")
		writeRecoveryAppendCommitted(t, w)
		committed <- struct{}{}
	}))
	require.NoError(t, backend.Listener.Close())
	backend.Listener = listener
	backend.Start()
	defer backend.Close()
	select {
	case <-committed:
	case <-time.After(5 * time.Second):
		t.Fatal("connection recovery did not deliver the retained record")
	}
	require.Eventually(t, func() bool { return statuses.Snapshot().Signals["logs"].ConfirmedWrittenRecords == 1 }, time.Second, 10*time.Millisecond)
	require.Zero(t, statuses.Snapshot().Signals["logs"].UnknownRecords)
}

func TestPersistentUnknownRetryRecovery(t *testing.T) {
	for _, signal := range []string{"logs", "traces", "metrics"} {
		t.Run(signal, func(t *testing.T) {
			var recovered atomic.Bool
			var recoveredCalls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if recovered.Load() && recoveredCalls.Add(1) > 1 {
					body, err := decodeRecoveryAppendBody(r)
					require.NoError(t, err)
					require.Contains(t, string(body), "unknown survives restart")
					writeRecoveryAppendCommitted(t, w)
					return
				}
				w.WriteHeader(503)
				_, _ = io.WriteString(w, "unavailable")
			}))
			defer backend.Close()
			address := freeTCPAddress(t)
			config := recoveryCollectorConfig(backend.URL, address, t.TempDir(), signal)
			config = strings.Replace(config, "    retry_on_failure:", "    unknown_retry: {enabled: true, max_retries: 2, max_elapsed_time: 5m}\n    retry_on_failure:", 1)
			config = strings.Replace(config, "initial_interval: 1s", "initial_interval: 1h", 1)
			config = strings.Replace(config, "max_interval: 1s", "max_interval: 1h", 1)
			firstStatus := scopedbexporter.NewStatusRegistry()
			first := startCollectorWithSettings(t, newSettings("yaml:"+config, "test", true, firstStatus, scopedbexporter.NewCaptureRegistry()))
			defer first.stop(t)
			require.NoError(t, sendRecoverySignal(address, signal, "unknown survives restart"))
			require.Eventually(t, func() bool { return firstStatus.Snapshot().Signals[signal].LastError != "" }, 5*time.Second, time.Millisecond)
			first.stop(t)
			require.Zero(t, firstStatus.Snapshot().Signals[signal].UnknownRecords)
			require.Zero(t, firstStatus.Snapshot().Signals[signal].RetryExhaustedRecords)
			recovered.Store(true)
			config = strings.ReplaceAll(config, "interval: 1h", "interval: 1ms")
			secondStatus := scopedbexporter.NewStatusRegistry()
			second := startCollectorWithSettings(t, newSettings("yaml:"+config, "test", true, secondStatus, scopedbexporter.NewCaptureRegistry()))
			defer second.stop(t)
			require.Eventually(t, func() bool { return secondStatus.Snapshot().Signals[signal].ConfirmedWrittenRecords == 1 }, 5*time.Second, time.Millisecond)
			require.Equal(t, uint64(1), secondStatus.Snapshot().Signals[signal].UnknownReplayAttempts)
			require.Equal(t, uint64(1), secondStatus.Snapshot().Signals[signal].UnknownReplayedRecords)
			require.Zero(t, secondStatus.Snapshot().Signals[signal].UnknownRecords)
		})
	}
}
