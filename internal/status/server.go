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
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/prometheus/common/expfmt"
)

type Server struct {
	service *service
	handler http.Handler
}

func New(version string) *Server {
	return newServer(newService(version))
}

func newServer(service *service) *Server {
	server := &Server{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.getHealth)
	mux.HandleFunc("GET /readyz", server.getReadiness)
	mux.HandleFunc("GET /metrics", server.getMetrics)
	mux.HandleFunc("GET /v1/ingestion/status", server.getIngestionStatus)
	server.handler = mux
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	s.handler.ServeHTTP(w, request)
}

func (s *Server) Ready(ctx context.Context) bool {
	_, ready := s.service.Readiness(ctx)
	return ready
}

func (s *Server) getHealth(w http.ResponseWriter, request *http.Request) {
	writeJSON(w, http.StatusOK, s.service.Health(request.Context()))
}

func (s *Server) getReadiness(w http.ResponseWriter, request *http.Request) {
	response, ready := s.service.Readiness(request.Context())
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, response)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getIngestionStatus(w http.ResponseWriter, request *http.Request) {
	writeJSON(w, http.StatusOK, s.service.IngestionStatus(request.Context()))
}

func (s *Server) getMetrics(w http.ResponseWriter, request *http.Request) {
	snapshot := s.service.IngestionStatus(request.Context())
	if !snapshot.InternalTelemetry.Available {
		http.Error(w, snapshot.InternalTelemetry.Error, http.StatusServiceUnavailable)
		return
	}
	var body bytes.Buffer
	if err := writePrometheusMetrics(&body, snapshot); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", string(expfmt.FmtText))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
