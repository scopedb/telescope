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
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/expfmt"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

const (
	DefaultCaptureLimit   = 100
	DefaultCaptureTimeout = 45 * time.Second
)

type Server struct {
	service *service
	handler http.Handler
}

func New(version string, configDigest string) *Server {
	service := newService(version)
	service.configDigest = strings.TrimSpace(configDigest)
	return newServer(service)
}

func newServer(service *service) *Server {
	server := &Server{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.getHealth)
	mux.HandleFunc("GET /readyz", server.getReadiness)
	mux.HandleFunc("GET /metrics", server.getMetrics)
	mux.HandleFunc("GET /v1/ingestion/status", server.getIngestionStatus)
	mux.HandleFunc("GET /v1/ingestion/capture", server.getIngestionCapture)
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

func (s *Server) getIngestionCapture(w http.ResponseWriter, request *http.Request) {
	signal, limit, timeout, err := parseCaptureOptions(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sample, err := s.service.Capture(request.Context(), signal, limit, timeout)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, errCaptureSignalNotConfigured):
			status = http.StatusNotFound
		case errors.Is(err, scopedbexporter.ErrCaptureInProgress):
			status = http.StatusConflict
		case errors.Is(err, scopedbexporter.ErrNoCapturedData),
			errors.Is(err, context.Canceled),
			errors.Is(err, context.DeadlineExceeded):
			status = http.StatusRequestTimeout
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Telescope-Signal", sample.Signal)
	w.Header().Set("X-Telescope-Records", strconv.Itoa(sample.Records))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sample.Payload)
}

func parseCaptureOptions(request *http.Request) (string, int, time.Duration, error) {
	query := request.URL.Query()
	signal := strings.TrimSpace(query.Get("signal"))
	if signal != "logs" && signal != "traces" && signal != "metrics" {
		return "", 0, 0, errors.New("signal must be logs, traces, or metrics")
	}

	limit := DefaultCaptureLimit
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return "", 0, 0, errors.New("limit must be a positive integer")
		}
		limit = parsed
	}

	timeout := DefaultCaptureTimeout
	if value := strings.TrimSpace(query.Get("timeout")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return "", 0, 0, errors.New("timeout must be a positive duration")
		}
		timeout = parsed
	}
	return signal, limit, timeout, nil
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
