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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

const serviceName = "telescope"

var errCaptureSignalNotConfigured = errors.New("capture signal is not configured")

type captureReader interface {
	Capture(context.Context, string, int, time.Duration) (scopedbexporter.CapturedSample, error)
}

type service struct {
	version          string
	configDigest     string
	now              func() time.Time
	ingestionRuntime exporterStatusReader
	ingestionCapture captureReader
	ingestionMetrics collectorMetricsReader
	queueStorage     queueStorageReader
}

func newService(version string) *service {
	return newServiceWithRuntime(
		version,
		scopedbexporter.NewStatusRegistry(),
		scopedbexporter.NewCaptureRegistry(),
	)
}

func newServiceWithRuntime(version string, runtime exporterStatusReader, captures captureReader) *service {
	return &service{
		version:          strings.TrimSpace(version),
		now:              func() time.Time { return time.Now().UTC() },
		ingestionRuntime: runtime,
		ingestionCapture: captures,
		ingestionMetrics: newPrometheusCollectorMetricsReader(),
		queueStorage:     newDirectoryQueueStorageReader(),
	}
}

func (s *service) Capture(
	ctx context.Context,
	signal string,
	limit int,
	timeout time.Duration,
) (scopedbexporter.CapturedSample, error) {
	if _, configured := s.ingestionRuntime.Snapshot().Signals[signal]; !configured {
		return scopedbexporter.CapturedSample{}, fmt.Errorf("%w: %s", errCaptureSignalNotConfigured, signal)
	}
	return s.ingestionCapture.Capture(ctx, signal, limit, timeout)
}

func (s *service) Health(_ context.Context) HealthResponse {
	response := HealthResponse{
		Status:  "ok",
		Service: serviceName,
	}
	if s.version != "" {
		response.Version = s.version
	}
	return response
}

func (s *service) Readiness(ctx context.Context) (HealthResponse, bool) {
	status := s.IngestionStatus(ctx)
	ready := status.InternalTelemetry.Available && len(status.Signals) > 0
	for _, signal := range status.Signals {
		ready = ready && signal.Ready
	}
	response := HealthResponse{
		Status:  "not_ready",
		Service: serviceName,
	}
	if ready {
		response.Status = "ready"
	}
	if s.version != "" {
		response.Version = s.version
	}
	return response, ready
}
