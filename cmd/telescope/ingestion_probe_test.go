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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestMarshalIngestionProbeSupportsEverySignal(t *testing.T) {
	for _, signal := range []string{"logs", "traces", "metrics"} {
		t.Run(signal, func(t *testing.T) {
			payload, err := marshalIngestionProbe(signal, "probe-test", time.Unix(1, 0))
			require.NoError(t, err)
			assert.Equal(t, "probe-test", probeIDFromOTLP(t, signal, payload))
		})
	}
}

func TestRunVerifyWaitsForExactProbe(t *testing.T) {
	var mu sync.RWMutex
	lastProbeIDs := []string{"different-probe"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/status":
			mu.RLock()
			probeIDs := append([]string(nil), lastProbeIDs...)
			mu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(statusapi.IngestionStatusResponse{
				Signals: []statusapi.IngestionSignalStatus{{
					Signal:              "traces",
					Ready:               true,
					DestinationVerified: true,
					LastProbeIDs:        probeIDs,
				}},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/traces":
			payload, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			probeID := probeIDFromOTLP(t, "traces", payload)
			mu.Lock()
			lastProbeIDs = []string{probeID, "batched-probe"}
			mu.Unlock()
			response, err := ptraceotlp.NewExportResponse().MarshalProto()
			require.NoError(t, err)
			w.Header().Set("Content-Type", "application/x-protobuf")
			_, _ = w.Write(response)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := runVerifyCommand(context.Background(), []string{
		"--otlp-endpoint", server.URL,
		"--status-endpoint", server.URL + "/status",
		"--timeout", "2s",
		"traces",
	}, io.Discard, io.Discard)
	require.NoError(t, err)
	mu.RLock()
	defer mu.RUnlock()
	require.NotEmpty(t, lastProbeIDs)
	assert.True(t, strings.HasPrefix(lastProbeIDs[0], "probe-"))
}

func TestVerifySignalsDefaultsToEnabledSignals(t *testing.T) {
	status := statusapi.IngestionStatusResponse{Signals: []statusapi.IngestionSignalStatus{
		{Signal: "logs"},
		{Signal: "metrics"},
	}}

	signals, err := verifySignals(nil, status)
	require.NoError(t, err)
	assert.Equal(t, []string{"logs", "metrics"}, signals)
}

func probeIDFromOTLP(t *testing.T, signal string, payload []byte) string {
	t.Helper()
	switch signal {
	case "logs":
		request := plogotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalProto(payload))
		value, ok := request.Logs().ResourceLogs().At(0).Resource().Attributes().Get(scopedbexporter.ProbeAttribute)
		require.True(t, ok)
		return value.Str()
	case "traces":
		request := ptraceotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalProto(payload))
		value, ok := request.Traces().ResourceSpans().At(0).Resource().Attributes().Get(scopedbexporter.ProbeAttribute)
		require.True(t, ok)
		return value.Str()
	case "metrics":
		request := pmetricotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalProto(payload))
		value, ok := request.Metrics().ResourceMetrics().At(0).Resource().Attributes().Get(scopedbexporter.ProbeAttribute)
		require.True(t, ok)
		return value.Str()
	default:
		t.Fatalf("unsupported signal %q", signal)
		return ""
	}
}
