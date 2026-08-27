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
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

const maxStandaloneCaptureRequestBytes = 20 * 1024 * 1024

var (
	errCaptureRequestTooLarge  = errors.New("OTLP request exceeds the 20 MiB capture limit")
	errUnsupportedCaptureMedia = errors.New("capture accepts application/json or application/x-protobuf")
	errUnsupportedCaptureCodec = errors.New("capture accepts identity or gzip content encoding")
)

type standaloneCaptureResult struct {
	sample scopedbexporter.CapturedSample
	err    error
}

type otlpCaptureRequest interface {
	UnmarshalJSON([]byte) error
	UnmarshalProto([]byte) error
}

type otlpCaptureResponse interface {
	MarshalJSON() ([]byte, error)
	MarshalProto() ([]byte, error)
}

func captureStandaloneHTTP(
	ctx context.Context,
	address string,
	signal string,
	limit int,
	timeout time.Duration,
	onListen func(string),
) (scopedbexporter.CapturedSample, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return scopedbexporter.CapturedSample{}, fmt.Errorf("listen for standalone capture on %s: %w", address, err)
	}
	defer listener.Close()

	registry := scopedbexporter.NewCaptureRegistry()
	captureCtx, cancelCapture := context.WithCancel(ctx)
	defer cancelCapture()
	resultCh := make(chan standaloneCaptureResult, 1)
	go func() {
		sample, captureErr := registry.Capture(captureCtx, signal, limit, timeout)
		resultCh <- standaloneCaptureResult{sample: sample, err: captureErr}
	}()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !registry.HasActiveCapture(signal) {
		select {
		case result := <-resultCh:
			return result.sample, result.err
		case <-ticker.C:
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/"+signal, standaloneCaptureHandler(registry, signal))
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErrCh := make(chan error, 1)
	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		serveErrCh <- serveErr
	}()

	if onListen != nil {
		onListen("http://" + listener.Addr().String())
	}
	select {
	case result := <-resultCh:
		shutdownCaptureServer(server)
		if serveErr := <-serveErrCh; serveErr != nil && result.err == nil {
			return scopedbexporter.CapturedSample{}, fmt.Errorf("serve standalone capture: %w", serveErr)
		}
		return result.sample, result.err
	case serveErr := <-serveErrCh:
		cancelCapture()
		<-resultCh
		if serveErr == nil {
			serveErr = errors.New("capture listener stopped")
		}
		return scopedbexporter.CapturedSample{}, fmt.Errorf("serve standalone capture: %w", serveErr)
	}
}

func shutdownCaptureServer(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func standaloneCaptureHandler(registry *scopedbexporter.CaptureRegistry, signal string) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" && mediaType != "application/x-protobuf" {
			http.Error(w, errUnsupportedCaptureMedia.Error(), http.StatusUnsupportedMediaType)
			return
		}
		body, err := readStandaloneCaptureBody(w, request)
		if err != nil {
			status := http.StatusBadRequest
			switch {
			case errors.Is(err, errCaptureRequestTooLarge):
				status = http.StatusRequestEntityTooLarge
			case errors.Is(err, errUnsupportedCaptureCodec):
				status = http.StatusUnsupportedMediaType
			}
			http.Error(w, err.Error(), status)
			return
		}
		response, err := observeStandaloneCapture(registry, signal, mediaType, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", mediaType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(response)
	}
}

func readStandaloneCaptureBody(w http.ResponseWriter, request *http.Request) ([]byte, error) {
	reader := io.Reader(http.MaxBytesReader(w, request.Body, maxStandaloneCaptureRequestBytes))
	if encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" {
		if encoding != "gzip" {
			return nil, errUnsupportedCaptureCodec
		}
		compressed, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("decode gzip OTLP request: %w", err)
		}
		defer compressed.Close()
		reader = compressed
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxStandaloneCaptureRequestBytes+1))
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			return nil, errCaptureRequestTooLarge
		}
		return nil, fmt.Errorf("read OTLP request: %w", err)
	}
	if len(body) > maxStandaloneCaptureRequestBytes {
		return nil, errCaptureRequestTooLarge
	}
	return body, nil
}

func observeStandaloneCapture(
	registry *scopedbexporter.CaptureRegistry,
	signal string,
	mediaType string,
	body []byte,
) ([]byte, error) {
	jsonEncoding := mediaType == "application/json"
	switch signal {
	case "logs":
		request := plogotlp.NewExportRequest()
		if err := unmarshalCaptureRequest(request, jsonEncoding, body); err != nil {
			return nil, fmt.Errorf("decode logs OTLP: %w", err)
		}
		registry.ObserveLogs(request.Logs())
		return marshalCaptureResponse(plogotlp.NewExportResponse(), jsonEncoding)
	case "traces":
		request := ptraceotlp.NewExportRequest()
		if err := unmarshalCaptureRequest(request, jsonEncoding, body); err != nil {
			return nil, fmt.Errorf("decode traces OTLP: %w", err)
		}
		registry.ObserveTraces(request.Traces())
		return marshalCaptureResponse(ptraceotlp.NewExportResponse(), jsonEncoding)
	case "metrics":
		request := pmetricotlp.NewExportRequest()
		if err := unmarshalCaptureRequest(request, jsonEncoding, body); err != nil {
			return nil, fmt.Errorf("decode metrics OTLP: %w", err)
		}
		registry.ObserveMetrics(request.Metrics())
		return marshalCaptureResponse(pmetricotlp.NewExportResponse(), jsonEncoding)
	default:
		return nil, fmt.Errorf("unsupported capture signal %q", signal)
	}
}

func unmarshalCaptureRequest(request otlpCaptureRequest, jsonEncoding bool, body []byte) error {
	if jsonEncoding {
		return request.UnmarshalJSON(body)
	}
	return request.UnmarshalProto(body)
}

func marshalCaptureResponse(response otlpCaptureResponse, jsonEncoding bool) ([]byte, error) {
	if jsonEncoding {
		return response.MarshalJSON()
	}
	return response.MarshalProto()
}
