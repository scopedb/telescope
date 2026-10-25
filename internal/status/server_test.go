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

	"github.com/scopedb/telescope/scopedbexporter"
	"github.com/stretchr/testify/require"
)

func TestGetHealth(t *testing.T) {
	server := newServer(newService("test"))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var response HealthResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, HealthResponse{Status: "ok", Service: "telescope", Version: "test"}, response)
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
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, `{"resourceSpans":[]}`, recorder.Body.String())
	require.Equal(t, "traces", recorder.Header().Get("X-Telescope-Signal"))
	require.Equal(t, "2", recorder.Header().Get("X-Telescope-Records"))
	require.Equal(t, "traces", captures.signal)
	require.Equal(t, 2, captures.limit)
	require.Equal(t, 3*time.Second, captures.timeout)
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
	require.Equal(t, http.StatusNotFound, recorder.Code)
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
			require.Equal(t, tt.wantStatus, recorder.Code)
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
		require.Equal(t, http.StatusBadRequest, recorder.Code)
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

func newService(version string) *service {
	return newServiceWithRuntime(
		version,
		scopedbexporter.NewStatusRegistry(),
		scopedbexporter.NewCaptureRegistry(),
	)
}
