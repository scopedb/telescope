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

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

type operationalService interface {
	Health(context.Context) HealthResponse
	Readiness(context.Context) (HealthResponse, bool)
	IngestionStatus(context.Context) IngestionStatusResponse
}

type Server struct {
	service operationalService
}

func New(version string) http.Handler {
	return NewWithService(NewService(version))
}

func NewWithService(service operationalService) http.Handler {
	server := &Server{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.getHealth)
	mux.HandleFunc("GET /readyz", server.getReadiness)
	mux.HandleFunc("GET /v1/ingestion/status", server.getIngestionStatus)
	return mux
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
