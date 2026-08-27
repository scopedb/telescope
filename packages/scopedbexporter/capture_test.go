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

package scopedbexporter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestCaptureRegistryCapturesAnExactPrefixForEverySignal(t *testing.T) {
	tests := []struct {
		signal  string
		observe func(*CaptureRegistry) int
		count   func(t *testing.T, payload []byte) int
	}{
		{
			signal: signalLogs,
			observe: func(registry *CaptureRegistry) int {
				request := plogotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(readGoldenFile(t, "logs.otlp.json")))
				logs := request.Logs()
				records := logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
				records.At(0).CopyTo(records.AppendEmpty())
				records.At(0).CopyTo(records.AppendEmpty())
				registry.ObserveLogs(logs)
				return logs.LogRecordCount()
			},
			count: func(t *testing.T, payload []byte) int {
				request := plogotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(payload))
				return request.Logs().LogRecordCount()
			},
		},
		{
			signal: signalTraces,
			observe: func(registry *CaptureRegistry) int {
				request := ptraceotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(readGoldenFile(t, "traces.otlp.json")))
				traces := request.Traces()
				spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
				spans.At(0).CopyTo(spans.AppendEmpty())
				spans.At(0).CopyTo(spans.AppendEmpty())
				registry.ObserveTraces(traces)
				return traces.SpanCount()
			},
			count: func(t *testing.T, payload []byte) int {
				request := ptraceotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(payload))
				return request.Traces().SpanCount()
			},
		},
		{
			signal: signalMetrics,
			observe: func(registry *CaptureRegistry) int {
				request := pmetricotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(readGoldenFile(t, "metrics.otlp.json")))
				metrics := request.Metrics()
				registry.ObserveMetrics(metrics)
				return metrics.DataPointCount()
			},
			count: func(t *testing.T, payload []byte) int {
				request := pmetricotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(payload))
				return request.Metrics().DataPointCount()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.signal, func(t *testing.T) {
			registry := NewCaptureRegistry()
			result := make(chan captureResult, 1)
			go func() {
				sample, err := registry.Capture(context.Background(), tt.signal, 2, time.Second)
				result <- captureResult{sample: sample, err: err}
			}()
			require.Eventually(t, func() bool {
				return captureActive(registry, tt.signal)
			}, time.Second, time.Millisecond)

			inputRecords := tt.observe(registry)
			captured := <-result
			require.NoError(t, captured.err)
			assert.Equal(t, tt.signal, captured.sample.Signal)
			assert.Equal(t, 2, captured.sample.Records)
			assert.Equal(t, 2, tt.count(t, captured.sample.Payload))
			assert.Greater(t, inputRecords, captured.sample.Records, "capture must not truncate its input")
		})
	}
}

func TestCaptureRegistryReturnsPartialSampleAtTimeout(t *testing.T) {
	registry := NewCaptureRegistry()
	result := make(chan captureResult, 1)
	go func() {
		sample, err := registry.Capture(context.Background(), signalLogs, 2, 50*time.Millisecond)
		result <- captureResult{sample: sample, err: err}
	}()
	require.Eventually(t, func() bool {
		return captureActive(registry, signalLogs)
	}, time.Second, time.Millisecond)

	request := plogotlp.NewExportRequest()
	require.NoError(t, request.UnmarshalJSON(readGoldenFile(t, "logs.otlp.json")))
	registry.ObserveLogs(request.Logs())

	captured := <-result
	require.NoError(t, captured.err)
	assert.Equal(t, 1, captured.sample.Records)
}

func TestCaptureRegistryRejectsConcurrentCaptureForSameSignal(t *testing.T) {
	registry := NewCaptureRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := registry.Capture(ctx, signalTraces, 1, time.Second)
		result <- err
	}()
	require.Eventually(t, func() bool {
		return captureActive(registry, signalTraces)
	}, time.Second, time.Millisecond)

	_, err := registry.Capture(context.Background(), signalTraces, 1, time.Second)
	assert.ErrorIs(t, err, ErrCaptureInProgress)
	cancel()
	assert.ErrorIs(t, <-result, context.Canceled)
}

func TestCaptureRegistryReportsTimeoutWithoutData(t *testing.T) {
	registry := NewCaptureRegistry()
	_, err := registry.Capture(context.Background(), signalMetrics, 1, time.Millisecond)
	assert.ErrorIs(t, err, ErrNoCapturedData)
}

type captureResult struct {
	sample CapturedSample
	err    error
}

func captureActive(registry *CaptureRegistry, signal string) bool {
	if registry == nil || !registry.active.Load() {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.sessions[signal] != nil
}

func TestCaptureRegistryRejectsInvalidRequest(t *testing.T) {
	registry := NewCaptureRegistry()
	_, err := registry.Capture(context.Background(), "profiles", 1, time.Second)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrCaptureInProgress))

	_, err = registry.Capture(context.Background(), signalLogs, 0, time.Second)
	assert.Error(t, err)

	_, err = registry.Capture(context.Background(), signalLogs, 1, 0)
	assert.Error(t, err)
}
