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
	"sort"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type prometheusLabel struct {
	name  string
	value string
}

type prometheusMetricFamily struct {
	name    string
	help    string
	typeOf  dto.MetricType
	metrics []*dto.Metric
}

func writePrometheusMetrics(w io.Writer, snapshot IngestionStatusResponse) error {
	var received []*dto.Metric
	var written []*dto.Metric
	var dropped []*dto.Metric
	var invalid []*dto.Metric
	var queueBytes []*dto.Metric
	var queueCapacity []*dto.Metric
	var lastSuccess []*dto.Metric
	var destinationVerified []*dto.Metric

	for _, signal := range snapshot.Signals {
		labels := []prometheusLabel{{name: "signal", value: signal.Signal}, {name: "table", value: signal.Table}}
		received = append(received, prometheusCounter(float64(signal.Received), labels...))
		written = append(written, prometheusCounter(float64(signal.Written), labels...))
		dropped = append(dropped,
			prometheusCounter(float64(signal.RetryExhausted), append(labels, prometheusLabel{name: "reason", value: "retry_exhausted"})...),
			prometheusCounter(float64(signal.EnqueueFailed), append(labels, prometheusLabel{name: "reason", value: "enqueue_failed"})...),
			prometheusCounter(float64(signal.PermanentRejected), append(labels, prometheusLabel{name: "reason", value: "permanent_rejected"})...),
		)

		reasons := make([]string, 0, len(signal.InvalidItemsByReason))
		for reason := range signal.InvalidItemsByReason {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			invalid = append(invalid, prometheusCounter(
				float64(signal.InvalidItemsByReason[reason]),
				prometheusLabel{name: "signal", value: signal.Signal},
				prometheusLabel{name: "reason", value: reason},
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

	families := []prometheusMetricFamily{
		{
			name:    "telescope_ingestion_received_total",
			help:    "Telemetry items accepted by Telescope, in each signal's native unit.",
			typeOf:  dto.MetricType_COUNTER,
			metrics: received,
		},
		{
			name:    "telescope_ingestion_written_total",
			help:    "Telemetry items confirmed written to ScopeDB, in each signal's native unit.",
			typeOf:  dto.MetricType_COUNTER,
			metrics: written,
		},
		{
			name:    "telescope_ingestion_dropped_total",
			help:    "Telemetry items ultimately dropped, partitioned by final reason.",
			typeOf:  dto.MetricType_COUNTER,
			metrics: dropped,
		},
		{
			name:    "telescope_ingestion_invalid_items_total",
			help:    "Invalid OpenTelemetry items rejected locally before ScopeDB append, partitioned by reason.",
			typeOf:  dto.MetricType_COUNTER,
			metrics: invalid,
		},
		{
			name:    "telescope_ingestion_queue_bytes",
			help:    "Logical serialized bytes currently retained in the exporter queue.",
			typeOf:  dto.MetricType_GAUGE,
			metrics: queueBytes,
		},
		{
			name:    "telescope_ingestion_queue_capacity_bytes",
			help:    "Configured logical byte capacity of the exporter queue.",
			typeOf:  dto.MetricType_GAUGE,
			metrics: queueCapacity,
		},
		{
			name:    "telescope_ingestion_last_success_timestamp_seconds",
			help:    "Unix timestamp of the latest ScopeDB append success, or zero before the first success.",
			typeOf:  dto.MetricType_GAUGE,
			metrics: lastSuccess,
		},
		{
			name:    "telescope_ingestion_destination_verified",
			help:    "Whether the ScopeDB destination has been verified by validation or a successful append.",
			typeOf:  dto.MetricType_GAUGE,
			metrics: destinationVerified,
		},
	}
	if snapshot.QueueStorage.Available {
		families = append(families, prometheusMetricFamily{
			name:    "telescope_queue_storage_allocated_bytes",
			help:    "Filesystem blocks currently allocated to files in the Telescope queue directory.",
			typeOf:  dto.MetricType_GAUGE,
			metrics: []*dto.Metric{prometheusGauge(float64(snapshot.QueueStorage.AllocatedBytes))},
		})
	}

	for _, family := range families {
		if len(family.metrics) == 0 {
			continue
		}
		name := family.name
		help := family.help
		typeOf := family.typeOf
		if _, err := expfmt.MetricFamilyToText(w, &dto.MetricFamily{
			Name:   &name,
			Help:   &help,
			Type:   &typeOf,
			Metric: family.metrics,
		}); err != nil {
			return err
		}
	}
	return nil
}

func prometheusCounter(value float64, labels ...prometheusLabel) *dto.Metric {
	return &dto.Metric{Label: prometheusLabels(labels), Counter: &dto.Counter{Value: &value}}
}

func prometheusGauge(value float64, labels ...prometheusLabel) *dto.Metric {
	return &dto.Metric{Label: prometheusLabels(labels), Gauge: &dto.Gauge{Value: &value}}
}

func prometheusLabels(labels []prometheusLabel) []*dto.LabelPair {
	pairs := make([]*dto.LabelPair, 0, len(labels))
	for _, label := range labels {
		name := label.name
		value := label.value
		pairs = append(pairs, &dto.LabelPair{Name: &name, Value: &value})
	}
	return pairs
}
