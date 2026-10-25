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
	families := make(map[string]*dto.MetricFamily)
	add := func(name, help string, metric *dto.Metric) {
		family := families[name]
		if family == nil {
			kind := dto.MetricType_GAUGE
			if metric.Counter != nil {
				kind = dto.MetricType_COUNTER
			}
			family = &dto.MetricFamily{Name: &name, Help: &help, Type: &kind}
			families[name] = family
		}
		family.Metric = append(family.Metric, metric)
	}

	for _, signal := range snapshot.Signals {
		labels := []*dto.LabelPair{{Name: new("signal"), Value: new(signal.Signal)}, {Name: new("table"), Value: new(signal.Table)}}
		add("telescope_ingestion_received_total", "Telemetry items accepted by Telescope, in each signal's native unit.", prometheusCounter(float64(signal.Received), labels...))
		add("telescope_ingestion_written_total", "Telemetry items confirmed written to ScopeDB, in each signal's native unit.", prometheusCounter(float64(signal.Written), labels...))
		add("telescope_ingestion_unknown_total", "Telemetry items with an unknown append outcome, excluded from written and dropped items.", prometheusCounter(float64(signal.Unknown), labels...))
		add("telescope_ingestion_unknown_replay_attempts_total", "Append attempts replaying a chunk with a previous unknown outcome.", prometheusCounter(float64(signal.UnknownReplayAttempts), labels...))
		add("telescope_ingestion_unknown_replayed_records_total", "Telemetry items replayed after an unknown outcome, counted once per chunk in each queue delivery.", prometheusCounter(float64(signal.UnknownReplayedRecords), labels...))
		for _, drop := range []struct {
			reason string
			count  uint64
		}{
			{"retry_exhausted", signal.RetryExhausted},
			{"enqueue_failed", signal.EnqueueFailed},
			{"permanent_rejected", signal.PermanentRejected},
		} {
			add("telescope_ingestion_dropped_total", "Telemetry items ultimately dropped, partitioned by final reason.",
				prometheusCounter(float64(drop.count), append(labels, &dto.LabelPair{Name: new("reason"), Value: new(drop.reason)})...))
		}

		for _, reason := range slices.Sorted(maps.Keys(signal.InvalidItemsByReason)) {
			add("telescope_ingestion_invalid_items_total", "Invalid OpenTelemetry items rejected locally before ScopeDB append, partitioned by reason.", prometheusCounter(
				float64(signal.InvalidItemsByReason[reason]),
				&dto.LabelPair{Name: new("signal"), Value: new(signal.Signal)},
				&dto.LabelPair{Name: new("reason"), Value: new(reason)},
			))
		}

		if signal.Queue.Enabled && signal.Queue.Capacity > 0 {
			add("telescope_ingestion_queue_utilization", "Fraction of the configured exporter queue capacity currently retained, for every queue sizer.", prometheusGauge(
				float64(signal.Queue.Size)/float64(signal.Queue.Capacity),
				labels...,
			))
		}

		if signal.Queue.Enabled && signal.Queue.Unit == "bytes" {
			add("telescope_ingestion_queue_bytes", "Logical serialized bytes currently retained in the exporter queue.", prometheusGauge(float64(signal.Queue.Size), labels...))
			add("telescope_ingestion_queue_capacity_bytes", "Configured logical byte capacity of the exporter queue.", prometheusGauge(float64(signal.Queue.Capacity), labels...))
		}

		lastSuccessValue := float64(0)
		if signal.LastWriteSuccess != nil {
			lastSuccessValue = float64(signal.LastWriteSuccess.UnixNano()) / float64(1e9)
		}
		add("telescope_ingestion_last_success_timestamp_seconds", "Unix timestamp of the latest ScopeDB append success, or zero before the first success.", prometheusGauge(lastSuccessValue, labels...))
		destinationValue := float64(0)
		if signal.DestinationVerified {
			destinationValue = 1
		}
		add("telescope_ingestion_destination_verified", "Whether the ScopeDB destination has been verified by validation or a successful append.", prometheusGauge(destinationValue, labels...))
	}

	if snapshot.QueueStorage.Available {
		add("telescope_queue_storage_allocated_bytes", "Filesystem blocks currently allocated to files in the Telescope queue directory.",
			prometheusGauge(float64(snapshot.QueueStorage.AllocatedBytes)))
	}
	for _, name := range slices.Sorted(maps.Keys(families)) {
		if _, err := expfmt.MetricFamilyToText(w, families[name]); err != nil {
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
