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

package main

import (
	"bytes"
	"compress/gzip"
	"context"
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

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
)

func TestRunPushesDeliveryMetricsAndKeepsOperationsAvailable(t *testing.T) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "TELESCOPE_") || strings.HasPrefix(name, "SCOPEDB_") {
			t.Setenv(name, "")
		}
	}
	t.Setenv("TEST_METRICS_TOKEN", "Bearer token$literal")
	t.Setenv("TEST_DEPLOYMENT", "test")
	var gatewayUnavailable, failedPush atomic.Bool
	var mu sync.Mutex
	observed := map[string]int64{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metrics" || r.Header.Get("Authorization") != "Bearer token$literal" {
			t.Error("unexpected telemetry path or authorization")
			w.WriteHeader(400)
			return
		}
		if gatewayUnavailable.Load() {
			failedPush.Store(true)
			w.WriteHeader(503)
			return
		}
		require.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
		reader, err := gzip.NewReader(r.Body)
		require.NoError(t, err)
		defer reader.Close()
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		request := pmetricotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalProto(body))
		resources := request.Metrics().ResourceMetrics()
		mu.Lock()
		defer mu.Unlock()
		for i := 0; i < resources.Len(); i++ {
			resource := resources.At(i)
			name, _ := resource.Resource().Attributes().Get("service.name")
			require.Equal(t, "telescope", name.Str())
			deployment, ok := resource.Resource().Attributes().Get("deployment.environment")
			require.True(t, ok)
			require.Equal(t, "test", deployment.Str())
			scopes := resource.ScopeMetrics()
			for j := 0; j < scopes.Len(); j++ {
				metrics := scopes.At(j).Metrics()
				for k := 0; k < metrics.Len(); k++ {
					m := metrics.At(k)
					key := m.Name()
					if m.Type() == pmetric.MetricTypeSum {
						for n := 0; n < m.Sum().DataPoints().Len(); n++ {
							point := m.Sum().DataPoints().At(n)
							pointKey := key
							if reason, ok := point.Attributes().Get("reason"); ok {
								pointKey += "/" + reason.Str()
							}
							observed[pointKey] = max(observed[pointKey], point.IntValue())
						}
					} else {
						observed[key] = 1
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		response, err := pmetricotlp.NewExportResponse().MarshalProto()
		require.NoError(t, err)
		_, _ = w.Write(response)
	}))
	t.Cleanup(gateway.Close)
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"code":"commit_unknown","message":"unknown","details":{"status":"unknown"}}`)
		case 2:
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"message":"rejected","details":{"status":"rejected"}}`)
		case 3:
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"details":{"status":"rejected"}}`)
		default:
			_, _ = io.WriteString(w, `{"status":"committed","num_rows_inserted":1}`)
		}
	}))
	t.Cleanup(backend.Close)
	grpcAddress, httpAddress, opsAddress, metricsAddress := runtimeTestAddress(t), runtimeTestAddress(t), runtimeTestAddress(t), runtimeTestAddress(t)
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`
destination:
  endpoint: %q
  api_key: test-key
receivers:
  grpc_addr: %q
  http_addr: %q
http_addr: %q
delivery:
  unknown_retry:
    enabled: true
  retry:
    initial_interval: 10ms
    max_interval: 10ms
  queue:
    type: memory
batch:
  size: 1
  max_size: 1
  timeout: 10ms
resources:
  memory_limit_mib: 1500
telemetry:
  level: detailed
  resource:
    deployment.environment: ${env:TEST_DEPLOYMENT}
  metrics_addr: %q
  otlp:
    endpoint: %q
    compression: gzip
    interval: 50ms
    timeout: 100ms
    headers:
      Authorization: ${env:TEST_METRICS_TOKEN}
signals:
  logs:
    table: logs
    mapping:
      value: log.message
`, backend.URL, grpcAddress, httpAddress, opsAddress, metricsAddress, gateway.URL)), 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var stderr bytes.Buffer
	go func() { done <- runTelescopeCommand(ctx, []string{path}, &stderr) }()
	t.Cleanup(func() {
		gatewayUnavailable.Store(false)
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err, stderr.String())
		case <-time.After(10 * time.Second):
			t.Error("run did not stop")
		}
		require.NotContains(t, stderr.String(), "token$literal")
	})
	client := &http.Client{Timeout: time.Second}
	ready := func() bool {
		response, err := client.Get("http://" + opsAddress + "/readyz")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	}
	require.Eventually(t, ready, 5*time.Second, 10*time.Millisecond)
	for range 3 {
		request := plogotlp.NewExportRequest()
		request.Logs().ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("record")
		body, err := request.MarshalProto()
		require.NoError(t, err)
		response, err := client.Post("http://"+httpAddress+"/v1/logs", "application/x-protobuf", bytes.NewReader(body))
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, response.Body)
		require.NoError(t, response.Body.Close())
		require.Equal(t, 200, response.StatusCode)
	}
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return observed["telescope_ingestion_unknown_total"] == 1 &&
			observed["telescope_ingestion_dropped_total/permanent_rejected"] == 1 &&
			observed["telescope_ingestion_written_total"] == 1 &&
			observed["otelcol_exporter_queue_size"] == 1 &&
			observed["otelcol_receiver_accepted_log_records"] == 3
	}, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	require.Equal(t, int64(1), observed["telescope_ingestion_unknown_replay_attempts_total"])
	require.Equal(t, int64(1), observed["telescope_ingestion_unknown_replayed_records_total"])
	mu.Unlock()
	require.Equal(t, int64(4), calls.Load())
	for _, path := range []string{"/metrics", "/v1/ingestion/status"} {
		response, err := client.Get("http://" + opsAddress + path)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, 200, response.StatusCode)
		if path == "/metrics" {
			require.Contains(t, string(body), `telescope_ingestion_unknown_replay_attempts_total{signal="logs",table="logs"} 1`)
			require.Contains(t, string(body), `telescope_ingestion_unknown_total{signal="logs",table="logs"} 1`)
			require.Contains(t, string(body), `telescope_ingestion_dropped_total{signal="logs",table="logs",reason="permanent_rejected"} 1`)
		} else {
			require.Contains(t, string(body), httpAddress)
			require.Contains(t, string(body), `"type":"memory"`)
		}
	}
	gatewayUnavailable.Store(true)
	require.Eventually(t, failedPush.Load, 3*time.Second, 10*time.Millisecond)
	require.True(t, ready())
}

func runtimeTestAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}
