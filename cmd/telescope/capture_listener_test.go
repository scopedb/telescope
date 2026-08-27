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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestStandaloneCaptureAcceptsOTLPHTTP(t *testing.T) {
	jsonMetrics := deploymentSample(t, "metrics")
	tests := []struct {
		name            string
		signal          string
		contentType     string
		contentEncoding string
		payload         []byte
	}{
		{
			name:        "logs json",
			signal:      "logs",
			contentType: "application/json; charset=utf-8",
			payload:     deploymentSample(t, "logs"),
		},
		{
			name:        "traces protobuf",
			signal:      "traces",
			contentType: "application/x-protobuf",
			payload:     ingestionProbePayload(t, "traces"),
		},
		{
			name:            "metrics gzip json",
			signal:          "metrics",
			contentType:     "application/json",
			contentEncoding: "gzip",
			payload:         gzipCapturePayload(t, jsonMetrics),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready := make(chan string, 1)
			result := make(chan standaloneCaptureResult, 1)
			go func() {
				sample, err := captureStandaloneHTTP(
					context.Background(),
					"127.0.0.1:0",
					tt.signal,
					1,
					time.Second,
					func(endpoint string) { ready <- endpoint },
				)
				result <- standaloneCaptureResult{sample: sample, err: err}
			}()

			endpoint := waitCaptureEndpoint(t, ready)
			request, err := http.NewRequest(http.MethodPost, endpoint+"/v1/"+tt.signal, bytes.NewReader(tt.payload))
			require.NoError(t, err)
			request.Header.Set("Content-Type", tt.contentType)
			if tt.contentEncoding != "" {
				request.Header.Set("Content-Encoding", tt.contentEncoding)
			}
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			response.Body.Close()
			assert.Equal(t, http.StatusOK, response.StatusCode)

			captured := waitStandaloneCaptureResult(t, result)
			require.NoError(t, captured.err)
			assert.Equal(t, 1, captured.sample.Records)
			assert.Equal(t, 1, capturedRecordCount(t, tt.signal, captured.sample.Payload))
		})
	}
}

func TestStandaloneCaptureReturnsPartialSampleAtTimeout(t *testing.T) {
	ready := make(chan string, 1)
	result := make(chan standaloneCaptureResult, 1)
	go func() {
		sample, err := captureStandaloneHTTP(
			context.Background(),
			"127.0.0.1:0",
			"traces",
			2,
			50*time.Millisecond,
			func(endpoint string) { ready <- endpoint },
		)
		result <- standaloneCaptureResult{sample: sample, err: err}
	}()

	request, err := http.NewRequest(
		http.MethodPost,
		waitCaptureEndpoint(t, ready)+"/v1/traces",
		bytes.NewReader(deploymentSample(t, "traces")),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)

	captured := waitStandaloneCaptureResult(t, result)
	require.NoError(t, captured.err)
	assert.Equal(t, 1, captured.sample.Records)
}

func TestStandaloneCaptureReportsNoData(t *testing.T) {
	_, err := captureStandaloneHTTP(
		context.Background(),
		"127.0.0.1:0",
		"logs",
		1,
		5*time.Millisecond,
		nil,
	)
	assert.ErrorIs(t, err, scopedbexporter.ErrNoCapturedData)
}

func TestStandaloneCaptureRejectsUnsupportedPayloadThenContinues(t *testing.T) {
	ready := make(chan string, 1)
	result := make(chan standaloneCaptureResult, 1)
	go func() {
		sample, err := captureStandaloneHTTP(
			context.Background(),
			"127.0.0.1:0",
			"logs",
			1,
			time.Second,
			func(endpoint string) { ready <- endpoint },
		)
		result <- standaloneCaptureResult{sample: sample, err: err}
	}()
	endpoint := waitCaptureEndpoint(t, ready)

	response, err := http.Post(endpoint+"/v1/traces", "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusNotFound, response.StatusCode)

	response, err = http.Post(endpoint+"/v1/logs", "text/plain", bytes.NewReader([]byte("invalid")))
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusUnsupportedMediaType, response.StatusCode)

	response, err = http.Post(endpoint+"/v1/logs", "application/json", bytes.NewReader([]byte("invalid")))
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)

	response, err = http.Post(
		endpoint+"/v1/logs",
		"application/json",
		bytes.NewReader(deploymentSample(t, "logs")),
	)
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, waitStandaloneCaptureResult(t, result).err)
}

func TestStandaloneCaptureReportsListenerConflict(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	_, err = captureStandaloneHTTP(
		context.Background(),
		listener.Addr().String(),
		"traces",
		1,
		time.Second,
		nil,
	)
	assert.ErrorContains(t, err, "listen for standalone capture")
}

func deploymentSample(t *testing.T, signal string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "deploy", "samples", signal+".otlp.json"))
	require.NoError(t, err)
	return payload
}

func ingestionProbePayload(t *testing.T, signal string) []byte {
	t.Helper()
	payload, err := marshalIngestionProbe(signal, "standalone-capture-test", time.Now())
	require.NoError(t, err)
	return payload
}

func gzipCapturePayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return compressed.Bytes()
}

func capturedRecordCount(t *testing.T, signal string, payload []byte) int {
	t.Helper()
	switch signal {
	case "logs":
		request := plogotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalJSON(payload))
		return request.Logs().LogRecordCount()
	case "traces":
		request := ptraceotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalJSON(payload))
		return request.Traces().SpanCount()
	case "metrics":
		request := pmetricotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalJSON(payload))
		return request.Metrics().DataPointCount()
	default:
		t.Fatalf("unsupported signal %q", signal)
		return 0
	}
}

func waitCaptureEndpoint(t *testing.T, ready <-chan string) string {
	t.Helper()
	select {
	case endpoint := <-ready:
		return endpoint
	case <-time.After(time.Second):
		t.Fatal("standalone capture did not start listening")
		return ""
	}
}

func waitStandaloneCaptureResult(t *testing.T, result <-chan standaloneCaptureResult) standaloneCaptureResult {
	t.Helper()
	select {
	case captured := <-result:
		return captured
	case <-time.After(2 * time.Second):
		t.Fatal("standalone capture did not finish")
		return standaloneCaptureResult{}
	}
}
