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
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunCaptureWritesOnlyOTLPPayloadToStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/ingestion/capture" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if got := request.URL.Query().Get("signal"); got != "traces" {
			t.Fatalf("unexpected signal: %q", got)
		}
		if got := request.URL.Query().Get("limit"); got != "2" {
			t.Fatalf("unexpected limit: %q", got)
		}
		if got := request.URL.Query().Get("timeout"); got != "3s" {
			t.Fatalf("unexpected timeout: %q", got)
		}
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
	if err != nil {
		t.Fatalf("runCaptureCommand() error = %v", err)
	}
	if got := stdout.String(); got != "{\"resourceSpans\":[]}\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	wantStderr := "waiting for new traces after Collector batching; generate traffic now (limit=2, timeout=3s)\n" +
		"captured traces: 2 records\n"
	if got := stderr.String(); got != wantStderr {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunCaptureUsesDefaultTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("timeout"); got != "45s" {
			t.Fatalf("unexpected timeout: %q", got)
		}
		_, _ = w.Write([]byte(`{"resourceSpans":[]}`))
	}))
	defer server.Close()

	err := runCaptureCommand(
		context.Background(),
		[]string{"--endpoint", server.URL, "traces"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("runCaptureCommand() error = %v", err)
	}
}

func TestCaptureHelpShowsUsageAndPreviewPipeline(t *testing.T) {
	var stderr bytes.Buffer
	err := runCaptureCommand(context.Background(), []string{"--help"}, &bytes.Buffer{}, &stderr)
	if err != flag.ErrHelp {
		t.Fatalf("runCaptureCommand() error = %v, want %v", err, flag.ErrHelp)
	}
	for _, expected := range []string{
		"Usage: telescope capture [options] <signal>",
		"past telemetry is not replayed",
		"--listen-http",
		"telescope preview --offline",
	} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("capture help missing %q: %s", expected, stderr.String())
		}
	}
}

func TestRunCaptureRejectsRemoteAndStandaloneModesTogether(t *testing.T) {
	err := runCaptureCommand(
		context.Background(),
		[]string{
			"--endpoint", "http://127.0.0.1:8080",
			"--listen-http", "127.0.0.1:4318",
			"traces",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCaptureRejectsInvalidSignal(t *testing.T) {
	err := runCaptureCommand(context.Background(), []string{"profiles"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error")
	}
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
	if err == nil {
		t.Fatal("expected error")
	}
	for _, expected := range []string{"no new exporter input observed within 1ms", "start capture before generating traffic", "batch delay"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("capture error missing %q: %v", expected, err)
		}
	}
}
