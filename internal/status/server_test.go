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

package status

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

func TestGetHealth(t *testing.T) {
	server := New("test")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	var response HealthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "ok" || response.Service != serviceName || response.Version != "test" {
		t.Fatalf("unexpected health response: %#v", response)
	}
}

func TestServerReady(t *testing.T) {
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"traces": {Signal: "traces", Ready: true},
		},
	}}
	service.ingestionMetrics = fakeCollectorMetricsReader{}

	if !newServer(service).Ready(context.Background()) {
		t.Fatal("expected server to report ready")
	}
}

func TestGetIngestionCaptureReturnsRawOTLPJSON(t *testing.T) {
	captures := &fakeCaptureReader{sample: scopedbexporter.CapturedSample{
		Signal:  "traces",
		Records: 2,
		Payload: []byte(`{"resourceSpans":[]}`),
	}}
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"traces": {Signal: "traces", Ready: true},
		},
	}}
	service.ingestionCapture = captures
	server := newServer(service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/ingestion/capture?signal=traces&limit=2&timeout=3s",
		nil,
	)

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d (%s)", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != `{"resourceSpans":[]}` {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	if got := recorder.Header().Get("X-Telescope-Signal"); got != "traces" {
		t.Fatalf("unexpected signal header: %q", got)
	}
	if got := recorder.Header().Get("X-Telescope-Records"); got != "2" {
		t.Fatalf("unexpected records header: %q", got)
	}
	if captures.signal != "traces" || captures.limit != 2 || captures.timeout != 3*time.Second {
		t.Fatalf("unexpected capture request: %#v", captures)
	}
}

func TestGetIngestionCaptureRejectsDisabledSignal(t *testing.T) {
	service := newService("test")
	service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
		Signals: map[string]scopedbexporter.SignalRuntimeStatus{
			"logs": {Signal: "logs", Ready: true},
		},
	}}
	service.ingestionCapture = &fakeCaptureReader{}
	recorder := httptest.NewRecorder()

	newServer(service).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/v1/ingestion/capture?signal=traces&limit=1&timeout=1s", nil),
	)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
}

func TestGetIngestionCaptureMapsExpectedErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "already active", err: scopedbexporter.ErrCaptureInProgress, wantStatus: http.StatusConflict},
		{name: "no data", err: scopedbexporter.ErrNoCapturedData, wantStatus: http.StatusRequestTimeout},
		{name: "canceled", err: context.Canceled, wantStatus: http.StatusRequestTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newService("test")
			service.ingestionRuntime = fakeExporterStatusReader{snapshot: scopedbexporter.StatusSnapshot{
				Signals: map[string]scopedbexporter.SignalRuntimeStatus{
					"metrics": {Signal: "metrics", Ready: true},
				},
			}}
			service.ingestionCapture = &fakeCaptureReader{err: tt.err}
			recorder := httptest.NewRecorder()

			newServer(service).ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/v1/ingestion/capture?signal=metrics&limit=1&timeout=1s", nil),
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("unexpected status: %d", recorder.Code)
			}
		})
	}
}

func TestGetIngestionCaptureRejectsInvalidOptions(t *testing.T) {
	service := newService("test")
	server := newServer(service)
	for _, target := range []string{
		"/v1/ingestion/capture",
		"/v1/ingestion/capture?signal=logs&limit=0&timeout=1s",
		"/v1/ingestion/capture?signal=logs&limit=1&timeout=never",
	} {
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: unexpected status %d", target, recorder.Code)
		}
	}
}

type fakeCaptureReader struct {
	sample  scopedbexporter.CapturedSample
	err     error
	signal  string
	limit   int
	timeout time.Duration
}

func (f *fakeCaptureReader) Capture(_ context.Context, signal string, limit int, timeout time.Duration) (scopedbexporter.CapturedSample, error) {
	f.signal = signal
	f.limit = limit
	f.timeout = timeout
	return f.sample, f.err
}
