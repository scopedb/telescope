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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunCaptureWritesOnlyOTLPPayloadToStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/v1/ingestion/capture", request.URL.Path)
		require.Equal(t, "traces", request.URL.Query().Get("signal"))
		require.Equal(t, "2", request.URL.Query().Get("limit"))
		require.Equal(t, "3s", request.URL.Query().Get("timeout"))

		w.Header().Set("X-Telescope-Signal", "traces")
		w.Header().Set("X-Telescope-Records", "2")
		_, _ = w.Write([]byte(`{"resourceSpans":[]}`))
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCaptureCommand(context.Background(), []string{
		"--endpoint", server.URL,
		"--limit", "2",
		"--timeout", "3s",
		"traces",
	}, &stdout, &stderr)
	require.NoError(t, err)
	require.Equal(t, "{\"resourceSpans\":[]}\n", stdout.String())

	wantStderr := "waiting for new traces after Collector batching; generate traffic now (limit=2, timeout=3s)\n" +
		"captured traces: 2 records\n"
	require.Equal(t, wantStderr, stderr.String())
}

func TestRunCaptureUsesDefaultTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "45s", request.URL.Query().Get("timeout"))

		_, _ = w.Write([]byte(`{"resourceSpans":[]}`))
	}))
	defer server.Close()

	err := runCaptureCommand(
		context.Background(),
		[]string{"--endpoint", server.URL, "traces"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	require.NoError(t, err)
}

func TestRunCaptureReportsEndpointError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "capture completed without data", http.StatusRequestTimeout)
	}))
	defer server.Close()

	err := runCaptureCommand(
		context.Background(),
		[]string{"--endpoint", server.URL, "--timeout", "1ms", "logs"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	require.Error(t, err)

	for _, expected := range []string{"no new exporter input observed within 1ms", "start capture before generating traffic", "batch delay"} {
		require.Contains(t, err.Error(), expected)
	}
}
