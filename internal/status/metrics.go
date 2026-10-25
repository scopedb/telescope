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
	"io"
	"maps"
	"slices"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

func writePrometheusMetrics(w io.Writer, snapshot IngestionStatusResponse) error {
	var received []*dto.Metric
	var written []*dto.Metric
	var unknown []*dto.Metric
	var dropped []*dto.Metric
	var invalid []*dto.Metric
	var queueBytes []*dto.Metric
	var queueCapacity []*dto.Metric
	var queueUtilization []*dto.Metric
	var lastSuccess []*dto.Metric
	var destinationVerified []*dto.Metric

	for _, signal := range snapshot.Signals {
		labels := []*dto.LabelPair{{Name: new("signal"), Value: new(signal.Signal)}, {Name: new("table"), Value: new(signal.Table)}}
		received = append(received, prometheusCounter(float64(signal.Received), labels...))
		written = append(written, prometheusCounter(float64(signal.Written), labels...))
		unknown = append(unknown, prometheusCounter(float64(signal.Unknown), labels...))
		dropped = append(dropped,
			prometheusCounter(float64(signal.RetryExhausted), append(labels, &dto.LabelPair{Name: new("reason"), Value: new("retry_exhausted")})...),
			prometheusCounter(float64(signal.EnqueueFailed), append(labels, &dto.LabelPair{Name: new("reason"), Value: new("enqueue_failed")})...),
			prometheusCounter(float64(signal.PermanentRejected), append(labels, &dto.LabelPair{Name: new("reason"), Value: new("permanent_rejected")})...),
		)

		for _, reason := range slices.Sorted(maps.Keys(signal.InvalidItemsByReason)) {
			invalid = append(invalid, prometheusCounter(
				float64(signal.InvalidItemsByReason[reason]),
				&dto.LabelPair{Name: new("signal"), Value: new(signal.Signal)},
				&dto.LabelPair{Name: new("reason"), Value: new(reason)},
			))
		}

		if signal.Queue.Enabled && signal.Queue.Capacity > 0 {
			queueUtilization = append(queueUtilization, prometheusGauge(
				float64(signal.Queue.Size)/float64(signal.Queue.Capacity),
				labels...,
			))
		}

		if signal.Queue.Enabled && signal.Queue.Unit == "bytes" {
			queueBytes = append(queueBytes, prometheusGauge(float64(signal.Queue.Size), labels...))
			queueCapacity = append(queueCapacity, prometheusGauge(float64(signal.Queue.Capacity), labels...))
		}

		lastSuccessValue := float64(0)
		if signal.LastWriteSuccess != nil {
			lastSuccessValue = float64(signal.LastWriteSuccess.UnixNano()) / float64(1e9)
		}
		lastSuccess = append(lastSuccess, prometheusGauge(lastSuccessValue, labels...))
		destinationValue := float64(0)
		if signal.DestinationVerified {
			destinationValue = 1
		}
		destinationVerified = append(destinationVerified, prometheusGauge(destinationValue, labels...))
	}

	families := []*dto.MetricFamily{
		{
			Name:   new("telescope_ingestion_unknown_total"),
			Help:   new("Telemetry items with an unknown append outcome, excluded from written and dropped items."),
			Type:   new(dto.MetricType_COUNTER),
			Metric: unknown,
		},
		{
			Name:   new("telescope_ingestion_received_total"),
			Help:   new("Telemetry items accepted by Telescope, in each signal's native unit."),
			Type:   new(dto.MetricType_COUNTER),
			Metric: received,
		},
		{
			Name:   new("telescope_ingestion_written_total"),
			Help:   new("Telemetry items confirmed written to ScopeDB, in each signal's native unit."),
			Type:   new(dto.MetricType_COUNTER),
			Metric: written,
		},
		{
			Name:   new("telescope_ingestion_dropped_total"),
			Help:   new("Telemetry items ultimately dropped, partitioned by final reason."),
			Type:   new(dto.MetricType_COUNTER),
			Metric: dropped,
		},
		{
			Name:   new("telescope_ingestion_invalid_items_total"),
			Help:   new("Invalid OpenTelemetry items rejected locally before ScopeDB append, partitioned by reason."),
			Type:   new(dto.MetricType_COUNTER),
			Metric: invalid,
		},
		{
			Name:   new("telescope_ingestion_queue_bytes"),
			Help:   new("Logical serialized bytes currently retained in the exporter queue."),
			Type:   new(dto.MetricType_GAUGE),
			Metric: queueBytes,
		},
		{
			Name:   new("telescope_ingestion_queue_capacity_bytes"),
			Help:   new("Configured logical byte capacity of the exporter queue."),
			Type:   new(dto.MetricType_GAUGE),
			Metric: queueCapacity,
		},
		{
			Name:   new("telescope_ingestion_queue_utilization"),
			Help:   new("Fraction of the configured exporter queue capacity currently retained, for every queue sizer."),
			Type:   new(dto.MetricType_GAUGE),
			Metric: queueUtilization,
		},
		{
			Name:   new("telescope_ingestion_last_success_timestamp_seconds"),
			Help:   new("Unix timestamp of the latest ScopeDB append success, or zero before the first success."),
			Type:   new(dto.MetricType_GAUGE),
			Metric: lastSuccess,
		},
		{
			Name:   new("telescope_ingestion_destination_verified"),
			Help:   new("Whether the ScopeDB destination has been verified by validation or a successful append."),
			Type:   new(dto.MetricType_GAUGE),
			Metric: destinationVerified,
		},
	}
	if snapshot.QueueStorage.Available {
		families = append(families, &dto.MetricFamily{
			Name:   new("telescope_queue_storage_allocated_bytes"),
			Help:   new("Filesystem blocks currently allocated to files in the Telescope queue directory."),
			Type:   new(dto.MetricType_GAUGE),
			Metric: []*dto.Metric{prometheusGauge(float64(snapshot.QueueStorage.AllocatedBytes))},
		})
	}

	for _, family := range families {
		if len(family.Metric) == 0 {
			continue
		}
		if _, err := expfmt.MetricFamilyToText(w, family); err != nil {
			return err
		}
	}
	return nil
}

func prometheusCounter(value float64, labels ...*dto.LabelPair) *dto.Metric {
	return &dto.Metric{Label: labels, Counter: &dto.Counter{Value: &value}}
}

func prometheusGauge(value float64, labels ...*dto.LabelPair) *dto.Metric {
	return &dto.Metric{Label: labels, Gauge: &dto.Gauge{Value: &value}}
}
