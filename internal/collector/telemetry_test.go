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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
)

func TestInternalMetricsReachOTLPGateway(t *testing.T) {
	var mu sync.Mutex
	observed := map[string]bool{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		request := pmetricotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalProto(body))
		resources := request.Metrics().ResourceMetrics()
		mu.Lock()
		defer mu.Unlock()
		for i := 0; i < resources.Len(); i++ {
			resource := resources.At(i)
			name, ok := resource.Resource().Attributes().Get("service.name")
			require.True(t, ok)
			require.Equal(t, "telescope", name.Str())
			scopes := resource.ScopeMetrics()
			for j := 0; j < scopes.Len(); j++ {
				metrics := scopes.At(j).Metrics()
				for k := 0; k < metrics.Len(); k++ {
					observed[strings.ReplaceAll(metrics.At(k).Name(), ".", "_")] = true
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		response, err := pmetricotlp.NewExportResponse().MarshalProto()
		require.NoError(t, err)
		_, err = w.Write(response)
		require.NoError(t, err)
	}))
	defer gateway.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"message": "test rejection", "retryable": false, "details": map[string]any{"status": "rejected"}}))
	}))
	defer backend.Close()
	address, metricsAddress := freeTCPAddress(t), freeTCPAddress(t)
	config := retryOutcomeCollectorConfig(backend.URL, address, metricsAddress)
	config = strings.Replace(config, "  telemetry:\n", "  telemetry:\n    resource:\n      service.name: telescope\n", 1)
	config = strings.Replace(config, "      readers:\n", fmt.Sprintf(`      readers:
        - periodic:
            interval: 100
            exporter:
              otlp:
                protocol: http/protobuf
                endpoint: %q
`, gateway.URL+"/v1/metrics"), 1)
	runtime := startRecoveryCollector(t, config)
	defer runtime.stop(t)
	require.NoError(t, sendRecoverySignal(address, "logs", "rejected record"))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return observed["otelcol_exporter_queue_size"] && observed["otelcol_exporter_send_failed_log_records"] && observed["otelcol_receiver_accepted_log_records"]
	}, 5*time.Second, 20*time.Millisecond)
	// The pull reader remains available for Telescope's status and metrics endpoints.
	require.Equal(t, float64(1), readCollectorCounter(t, metricsAddress, "otelcol_exporter_send_failed_log_records"))
}
