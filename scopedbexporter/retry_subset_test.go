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
	"testing"

	"github.com/stretchr/testify/assert"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestRetrySubsetsDropCommittedPrefix(t *testing.T) {
	logs := plog.NewLogs()
	logRecords := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for i := 0; i < 3; i++ {
		logRecords.AppendEmpty().Body().SetInt(int64(i))
	}
	logSuffix := logsFromRecords(logs, []int{1, 2})
	assert.Equal(t, 2, logSuffix.LogRecordCount())
	assert.Equal(t, int64(1), logSuffix.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Int())

	traces := ptrace.NewTraces()
	spans := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
	for i := 0; i < 3; i++ {
		spans.AppendEmpty().SetName(string(rune('a' + i)))
	}
	traceSuffix := tracesFromRecords(traces, []int{2})
	assert.Equal(t, 1, traceSuffix.SpanCount())
	assert.Equal(t, "c", traceSuffix.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Name())

	metrics := pmetric.NewMetrics()
	metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	points := metric.SetEmptyGauge().DataPoints()
	for i := 0; i < 3; i++ {
		points.AppendEmpty().SetIntValue(int64(i))
	}
	metricSuffix := metricsFromRecords(metrics, []int{1, 2})
	assert.Equal(t, 2, metricSuffix.DataPointCount())
	assert.Equal(t, int64(1), metricSuffix.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).IntValue())
}

func TestRetrySubsetsSelectNonContiguousRecords(t *testing.T) {
	logs := plog.NewLogs()
	logRecords := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for i := 0; i < 4; i++ {
		logRecords.AppendEmpty().Body().SetInt(int64(i))
	}
	selectedLogs := logsFromRecords(logs, []int{0, 2})
	assert.Equal(t, 2, selectedLogs.LogRecordCount())
	assert.Equal(t, int64(0), selectedLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Body().Int())
	assert.Equal(t, int64(2), selectedLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(1).Body().Int())

	traces := ptrace.NewTraces()
	spans := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
	for i := 0; i < 4; i++ {
		spans.AppendEmpty().SetName(string(rune('a' + i)))
	}
	selectedTraces := tracesFromRecords(traces, []int{1, 3})
	assert.Equal(t, 2, selectedTraces.SpanCount())
	assert.Equal(t, "b", selectedTraces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Name())
	assert.Equal(t, "d", selectedTraces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(1).Name())

	metrics := pmetric.NewMetrics()
	metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	points := metric.SetEmptyGauge().DataPoints()
	for i := 0; i < 4; i++ {
		points.AppendEmpty().SetIntValue(int64(i))
	}
	selectedMetrics := metricsFromRecords(metrics, []int{0, 2})
	assert.Equal(t, 2, selectedMetrics.DataPointCount())
	selectedPoints := selectedMetrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints()
	assert.Equal(t, int64(0), selectedPoints.At(0).IntValue())
	assert.Equal(t, int64(2), selectedPoints.At(1).IntValue())
}
