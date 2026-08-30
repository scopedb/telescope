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
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type metricMappingFailure struct {
	err        error
	dataPoints int
}

// filterInvalidMetrics keeps mapping failures local to the metric that caused
// them. The returned Metrics aliases the input when every metric is valid and
// owns a filtered copy otherwise.
func filterInvalidMetrics(metrics pmetric.Metrics) (pmetric.Metrics, []metricMappingFailure) {
	failures := metricMappingFailures(metrics)
	if len(failures) == 0 {
		return metrics, nil
	}

	filtered := pmetric.NewMetrics()
	metrics.CopyTo(filtered)
	filtered.ResourceMetrics().RemoveIf(func(resource pmetric.ResourceMetrics) bool {
		resource.ScopeMetrics().RemoveIf(func(scope pmetric.ScopeMetrics) bool {
			scope.Metrics().RemoveIf(func(metric pmetric.Metric) bool {
				return metricMappingError(metric) != nil
			})
			return scope.Metrics().Len() == 0
		})
		return resource.ScopeMetrics().Len() == 0
	})

	return filtered, failures
}

func metricMappingFailures(metrics pmetric.Metrics) []metricMappingFailure {
	var failures []metricMappingFailure
	resourceMetrics := metrics.ResourceMetrics()
	for i := 0; i < resourceMetrics.Len(); i++ {
		scopeMetrics := resourceMetrics.At(i).ScopeMetrics()
		for j := 0; j < scopeMetrics.Len(); j++ {
			metricsSlice := scopeMetrics.At(j).Metrics()
			for k := 0; k < metricsSlice.Len(); k++ {
				metric := metricsSlice.At(k)
				if err := metricMappingError(metric); err != nil {
					failures = append(failures, metricMappingFailure{
						err:        fmt.Errorf("map metric at resource %d scope %d metric %d: %w", i, j, k, err),
						dataPoints: metricDataPointCount(metric),
					})
				}
			}
		}
	}
	return failures
}

func metricMappingError(metric pmetric.Metric) error {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		points := metric.Gauge().DataPoints()
		for i := 0; i < points.Len(); i++ {
			if points.At(i).ValueType() == pmetric.NumberDataPointValueTypeEmpty {
				return fmt.Errorf("gauge data point %d: %w", i, unsupportedNumberValueTypeError(points.At(i)))
			}
		}
	case pmetric.MetricTypeSum:
		points := metric.Sum().DataPoints()
		for i := 0; i < points.Len(); i++ {
			if points.At(i).ValueType() == pmetric.NumberDataPointValueTypeEmpty {
				return fmt.Errorf("sum data point %d: %w", i, unsupportedNumberValueTypeError(points.At(i)))
			}
		}
	case pmetric.MetricTypeHistogram,
		pmetric.MetricTypeExponentialHistogram,
		pmetric.MetricTypeSummary:
		return nil
	default:
		return &mappingError{
			reason: mappingReasonUnsupportedMetricType,
			err:    fmt.Errorf("metric %q has unsupported type %s", metric.Name(), metric.Type()),
		}
	}
	return nil
}

func mapMetrics(metrics pmetric.Metrics) (*IngestPayload, error) {
	payload := newPayload()

	resourceMetrics := metrics.ResourceMetrics()
	for i := 0; i < resourceMetrics.Len(); i++ {
		resourceMetric := resourceMetrics.At(i)

		scopeMetrics := resourceMetric.ScopeMetrics()
		for j := 0; j < scopeMetrics.Len(); j++ {
			scopeMetric := scopeMetrics.At(j)
			otelCtx := newOTelContext(resourceMetric.Resource(), resourceMetric.SchemaUrl(), scopeMetric.Scope(), scopeMetric.SchemaUrl())

			metricsSlice := scopeMetric.Metrics()
			for k := 0; k < metricsSlice.Len(); k++ {
				metric := metricsSlice.At(k)
				if err := appendMetricRecords(payload, metric, otelCtx); err != nil {
					return nil, fmt.Errorf("map metric at resource %d scope %d metric %d: %w", i, j, k, err)
				}
			}
		}
	}

	return payload, nil
}

func appendMetricRecords(payload *IngestPayload, metric pmetric.Metric, otelCtx otelContext) error {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		points := metric.Gauge().DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			record, err := newNumberMetricRecord(metric, otelCtx, point)
			if err != nil {
				return fmt.Errorf("gauge data point %d: %w", i, err)
			}
			record["type"] = "gauge"
			record["exemplars"] = exemplarsToSlice(point.Exemplars())
			payload.Records = append(payload.Records, record)
		}
	case pmetric.MetricTypeSum:
		sum := metric.Sum()
		points := sum.DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			record, err := newNumberMetricRecord(metric, otelCtx, point)
			if err != nil {
				return fmt.Errorf("sum data point %d: %w", i, err)
			}
			record["type"] = "sum"
			record["temporality"] = strings.ToLower(sum.AggregationTemporality().String())
			record["is_monotonic"] = sum.IsMonotonic()
			record["exemplars"] = exemplarsToSlice(point.Exemplars())
			payload.Records = append(payload.Records, record)
		}
	case pmetric.MetricTypeHistogram:
		histogram := metric.Histogram()
		points := histogram.DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			distribution := histogramDataPointToMap(point)
			record := newMetricRecord(metric, otelCtx, point.Attributes(), point.Timestamp(), point.StartTimestamp(), point.Flags())
			record["type"] = "histogram"
			record["temporality"] = strings.ToLower(histogram.AggregationTemporality().String())
			record["histogram"] = distribution
			record["distribution"] = distribution
			record["exemplars"] = exemplarsToSlice(point.Exemplars())
			payload.Records = append(payload.Records, record)
		}
	case pmetric.MetricTypeExponentialHistogram:
		histogram := metric.ExponentialHistogram()
		points := histogram.DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			distribution := exponentialHistogramDataPointToMap(point)
			record := newMetricRecord(metric, otelCtx, point.Attributes(), point.Timestamp(), point.StartTimestamp(), point.Flags())
			record["type"] = "exponential_histogram"
			record["temporality"] = strings.ToLower(histogram.AggregationTemporality().String())
			record["histogram"] = distribution
			record["distribution"] = distribution
			record["exemplars"] = exemplarsToSlice(point.Exemplars())
			payload.Records = append(payload.Records, record)
		}
	case pmetric.MetricTypeSummary:
		summary := metric.Summary()
		points := summary.DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			distribution := summaryDataPointToMap(point)
			record := newMetricRecord(metric, otelCtx, point.Attributes(), point.Timestamp(), point.StartTimestamp(), point.Flags())
			record["type"] = "summary"
			record["summary"] = distribution
			record["distribution"] = distribution
			payload.Records = append(payload.Records, record)
		}
	default:
		return &mappingError{
			reason: mappingReasonUnsupportedMetricType,
			err:    fmt.Errorf("metric %q has unsupported type %s", metric.Name(), metric.Type()),
		}
	}
	return nil
}

func newMetricRecord(metric pmetric.Metric, otelCtx otelContext, attrs pcommon.Map, ts pcommon.Timestamp, start pcommon.Timestamp, flags pmetric.DataPointFlags) Record {
	record := Record{
		"metric_name":               metric.Name(),
		"description":               metric.Description(),
		"unit":                      metric.Unit(),
		"metadata":                  attributesToMap(metric.Metadata()),
		"timestamp_unix_nano":       timestampString(ts),
		"start_timestamp_unix_nano": timestampString(start),
		"attributes":                attributesToMap(attrs),
		"flags":                     uint32(flags),
	}
	otelCtx.addTo(record)
	return record
}

func newNumberMetricRecord(metric pmetric.Metric, otelCtx otelContext, point pmetric.NumberDataPoint) (Record, error) {
	value, valueType, err := numberDataPointValue(point)
	if err != nil {
		return nil, err
	}
	record := newMetricRecord(metric, otelCtx, point.Attributes(), point.Timestamp(), point.StartTimestamp(), point.Flags())
	record["value"] = value
	record["value_type"] = valueType
	if valueType == "int" {
		record["int_value"] = value
		record["number_value"] = float64(value.(int64))
	} else {
		record["double_value"] = value
		record["number_value"] = value
	}
	return record, nil
}

func numberDataPointValue(point pmetric.NumberDataPoint) (any, string, error) {
	switch point.ValueType() {
	case pmetric.NumberDataPointValueTypeInt:
		return point.IntValue(), "int", nil
	case pmetric.NumberDataPointValueTypeDouble:
		return point.DoubleValue(), "double", nil
	default:
		return nil, "", unsupportedNumberValueTypeError(point)
	}
}

func unsupportedNumberValueTypeError(point pmetric.NumberDataPoint) error {
	return &mappingError{
		reason: mappingReasonUnsupportedNumberValueType,
		err:    fmt.Errorf("unsupported number value type %s", point.ValueType()),
	}
}

func exemplarsToSlice(exemplars pmetric.ExemplarSlice) []map[string]any {
	out := make([]map[string]any, 0, exemplars.Len())
	for i := 0; i < exemplars.Len(); i++ {
		exemplar := exemplars.At(i)
		item := map[string]any{
			"timestamp_unix_nano": timestampString(exemplar.Timestamp()),
			"trace_id":            traceIDString(exemplar.TraceID()),
			"span_id":             spanIDString(exemplar.SpanID()),
			"filtered_attributes": attributesToMap(exemplar.FilteredAttributes()),
		}
		switch exemplar.ValueType() {
		case pmetric.ExemplarValueTypeInt:
			item["value"] = exemplar.IntValue()
			item["value_type"] = "int"
		case pmetric.ExemplarValueTypeDouble:
			item["value"] = exemplar.DoubleValue()
			item["value_type"] = "double"
		}
		out = append(out, item)
	}
	return out
}

func histogramDataPointToMap(point pmetric.HistogramDataPoint) map[string]any {
	out := map[string]any{
		"count":           point.Count(),
		"bucket_counts":   uint64SliceToAny(point.BucketCounts()),
		"explicit_bounds": float64SliceToAny(point.ExplicitBounds()),
	}
	if point.HasSum() {
		out["sum"] = point.Sum()
	}
	if point.HasMin() {
		out["min"] = point.Min()
	}
	if point.HasMax() {
		out["max"] = point.Max()
	}
	return out
}

func exponentialHistogramDataPointToMap(point pmetric.ExponentialHistogramDataPoint) map[string]any {
	out := map[string]any{
		"count":          point.Count(),
		"scale":          point.Scale(),
		"zero_count":     point.ZeroCount(),
		"zero_threshold": point.ZeroThreshold(),
		"positive":       expHistogramBucketsToMap(point.Positive()),
		"negative":       expHistogramBucketsToMap(point.Negative()),
	}
	if point.HasSum() {
		out["sum"] = point.Sum()
	}
	if point.HasMin() {
		out["min"] = point.Min()
	}
	if point.HasMax() {
		out["max"] = point.Max()
	}
	return out
}

func expHistogramBucketsToMap(buckets pmetric.ExponentialHistogramDataPointBuckets) map[string]any {
	return map[string]any{
		"offset":        buckets.Offset(),
		"bucket_counts": uint64SliceToAny(buckets.BucketCounts()),
	}
}

func summaryDataPointToMap(point pmetric.SummaryDataPoint) map[string]any {
	out := map[string]any{
		"count":           point.Count(),
		"sum":             point.Sum(),
		"quantile_values": make([]map[string]any, 0, point.QuantileValues().Len()),
	}
	quantiles := point.QuantileValues()
	values := out["quantile_values"].([]map[string]any)
	for i := 0; i < quantiles.Len(); i++ {
		value := quantiles.At(i)
		values = append(values, map[string]any{
			"quantile": value.Quantile(),
			"value":    value.Value(),
		})
	}
	out["quantile_values"] = values
	return out
}
