/*
 * Copyright 2026 ScopeDB contributors
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
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func mapMetrics(cfg *Config, metrics pmetric.Metrics) (*IngestPayload, error) {
	payload := newPayload(cfg, signalMetrics)

	resourceMetrics := metrics.ResourceMetrics()
	for i := 0; i < resourceMetrics.Len(); i++ {
		resourceMetric := resourceMetrics.At(i)
		resource := attributesToMap(resourceMetric.Resource().Attributes())

		scopeMetrics := resourceMetric.ScopeMetrics()
		for j := 0; j < scopeMetrics.Len(); j++ {
			scopeMetric := scopeMetrics.At(j)
			scope := scopeToMap(scopeMetric.Scope())

			metricsSlice := scopeMetric.Metrics()
			for k := 0; k < metricsSlice.Len(); k++ {
				metric := metricsSlice.At(k)
				appendMetricRecords(payload, metric, resource, scope)
			}
		}
	}

	return payload, nil
}

func appendMetricRecords(payload *IngestPayload, metric pmetric.Metric, resource map[string]any, scope map[string]any) {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		points := metric.Gauge().DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			record := newMetricRecord(metric, resource, scope, point.Attributes(), point.Timestamp(), point.StartTimestamp())
			record["type"] = "gauge"
			record["value"] = numberDataPointValue(point)
			record["number_value"] = numberDataPointFloat(point)
			record["exemplars"] = exemplarsToSlice(point.Exemplars())
			payload.Records = append(payload.Records, record)
		}
	case pmetric.MetricTypeSum:
		sum := metric.Sum()
		points := sum.DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			record := newMetricRecord(metric, resource, scope, point.Attributes(), point.Timestamp(), point.StartTimestamp())
			record["type"] = "sum"
			record["temporality"] = strings.ToLower(sum.AggregationTemporality().String())
			record["is_monotonic"] = sum.IsMonotonic()
			record["value"] = numberDataPointValue(point)
			record["number_value"] = numberDataPointFloat(point)
			record["exemplars"] = exemplarsToSlice(point.Exemplars())
			payload.Records = append(payload.Records, record)
		}
	case pmetric.MetricTypeHistogram:
		histogram := metric.Histogram()
		points := histogram.DataPoints()
		for i := 0; i < points.Len(); i++ {
			point := points.At(i)
			distribution := histogramDataPointToMap(point)
			record := newMetricRecord(metric, resource, scope, point.Attributes(), point.Timestamp(), point.StartTimestamp())
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
			record := newMetricRecord(metric, resource, scope, point.Attributes(), point.Timestamp(), point.StartTimestamp())
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
			record := newMetricRecord(metric, resource, scope, point.Attributes(), point.Timestamp(), point.StartTimestamp())
			record["type"] = "summary"
			record["summary"] = distribution
			record["distribution"] = distribution
			payload.Records = append(payload.Records, record)
		}
	}
}

func newMetricRecord(metric pmetric.Metric, resource map[string]any, scope map[string]any, attrs pcommon.Map, ts pcommon.Timestamp, start pcommon.Timestamp) Record {
	return Record{
		"metric_name":               metric.Name(),
		"description":               metric.Description(),
		"unit":                      metric.Unit(),
		"timestamp_unix_nano":       timestampString(ts),
		"start_timestamp_unix_nano": timestampString(start),
		"resource":                  resource,
		"scope":                     scope,
		"attributes":                attributesToMap(attrs),
	}
}

func numberDataPointValue(point pmetric.NumberDataPoint) any {
	switch point.ValueType() {
	case pmetric.NumberDataPointValueTypeInt:
		return point.IntValue()
	case pmetric.NumberDataPointValueTypeDouble:
		return point.DoubleValue()
	default:
		return nil
	}
}

func numberDataPointFloat(point pmetric.NumberDataPoint) any {
	switch point.ValueType() {
	case pmetric.NumberDataPointValueTypeInt:
		return float64(point.IntValue())
	case pmetric.NumberDataPointValueTypeDouble:
		return point.DoubleValue()
	default:
		return nil
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
		case pmetric.ExemplarValueTypeDouble:
			item["value"] = exemplar.DoubleValue()
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
