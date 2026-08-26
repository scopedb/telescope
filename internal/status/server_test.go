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
