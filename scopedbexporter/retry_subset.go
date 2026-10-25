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
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func logsFromRecords(input plog.Logs, indexes []int) plog.Logs {
	output := plog.NewLogs()
	input.CopyTo(output)
	selected := selectedRecords(indexes)
	index := 0
	output.ResourceLogs().RemoveIf(func(resource plog.ResourceLogs) bool {
		resource.ScopeLogs().RemoveIf(func(scope plog.ScopeLogs) bool {
			scope.LogRecords().RemoveIf(func(plog.LogRecord) bool {
				remove := !selected[index]
				index++
				return remove
			})
			return scope.LogRecords().Len() == 0
		})
		return resource.ScopeLogs().Len() == 0
	})
	return output
}

func tracesFromRecords(input ptrace.Traces, indexes []int) ptrace.Traces {
	output := ptrace.NewTraces()
	input.CopyTo(output)
	selected := selectedRecords(indexes)
	index := 0
	output.ResourceSpans().RemoveIf(func(resource ptrace.ResourceSpans) bool {
		resource.ScopeSpans().RemoveIf(func(scope ptrace.ScopeSpans) bool {
			scope.Spans().RemoveIf(func(ptrace.Span) bool {
				remove := !selected[index]
				index++
				return remove
			})
			return scope.Spans().Len() == 0
		})
		return resource.ScopeSpans().Len() == 0
	})
	return output
}

func metricsFromRecords(input pmetric.Metrics, indexes []int) pmetric.Metrics {
	output := pmetric.NewMetrics()
	input.CopyTo(output)
	selected := selectedRecords(indexes)
	index := 0
	output.ResourceMetrics().RemoveIf(func(resource pmetric.ResourceMetrics) bool {
		resource.ScopeMetrics().RemoveIf(func(scope pmetric.ScopeMetrics) bool {
			scope.Metrics().RemoveIf(func(metric pmetric.Metric) bool {
				removeMetricDataPointsExcept(metric, selected, &index)
				return metricDataPointCount(metric) == 0
			})
			return scope.Metrics().Len() == 0
		})
		return resource.ScopeMetrics().Len() == 0
	})
	return output
}

func removeMetricDataPointsExcept(metric pmetric.Metric, selected map[int]bool, index *int) {
	remove := func() bool {
		shouldRemove := !selected[*index]
		(*index)++
		return shouldRemove
	}
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		metric.Gauge().DataPoints().RemoveIf(func(pmetric.NumberDataPoint) bool { return remove() })
	case pmetric.MetricTypeSum:
		metric.Sum().DataPoints().RemoveIf(func(pmetric.NumberDataPoint) bool { return remove() })
	case pmetric.MetricTypeHistogram:
		metric.Histogram().DataPoints().RemoveIf(func(pmetric.HistogramDataPoint) bool { return remove() })
	case pmetric.MetricTypeExponentialHistogram:
		metric.ExponentialHistogram().DataPoints().RemoveIf(func(pmetric.ExponentialHistogramDataPoint) bool { return remove() })
	case pmetric.MetricTypeSummary:
		metric.Summary().DataPoints().RemoveIf(func(pmetric.SummaryDataPoint) bool { return remove() })
	}
}

func selectedRecords(indexes []int) map[int]bool {
	selected := make(map[int]bool, len(indexes))
	for _, index := range indexes {
		selected[index] = true
	}
	return selected
}

func metricDataPointCount(metric pmetric.Metric) int {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		return metric.Gauge().DataPoints().Len()
	case pmetric.MetricTypeSum:
		return metric.Sum().DataPoints().Len()
	case pmetric.MetricTypeHistogram:
		return metric.Histogram().DataPoints().Len()
	case pmetric.MetricTypeExponentialHistogram:
		return metric.ExponentialHistogram().DataPoints().Len()
	case pmetric.MetricTypeSummary:
		return metric.Summary().DataPoints().Len()
	default:
		return 0
	}
}
