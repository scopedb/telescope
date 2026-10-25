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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Delivery counters use the same registry as the operational API. Collector
// readers export them alongside receiver and queue metrics, including when no
// Prometheus scraper is deployed.
func (e *dbExporter) registerDeliveryMetrics() (metric.Registration, error) {
	counters := []struct {
		name        string
		description string
		instrument  metric.Int64ObservableCounter
	}{
		{name: "telescope_ingestion_written_total", description: "Telemetry items confirmed written to ScopeDB."},
		{name: "telescope_ingestion_unknown_total", description: "Telemetry items with unknown append outcomes, excluded from written and dropped."},
		{name: "telescope_ingestion_dropped_total", description: "Telemetry items ultimately dropped, partitioned by final reason."},
		{name: "telescope_ingestion_invalid_items_total", description: "Invalid OpenTelemetry items rejected locally, partitioned by reason."},
	}
	instruments := make([]metric.Observable, 0, len(counters))
	for i := range counters {
		counter, err := e.meter.Int64ObservableCounter(counters[i].name, metric.WithDescription(counters[i].description))
		if err != nil {
			return nil, err
		}
		counters[i].instrument = counter
		instruments = append(instruments, counter)
	}
	lastSuccess, err := e.meter.Float64ObservableGauge("telescope_ingestion_last_success_timestamp_seconds", metric.WithDescription("Unix timestamp of the latest confirmed append, or zero before the first success."))
	if err != nil {
		return nil, err
	}
	verified, err := e.meter.Int64ObservableGauge("telescope_ingestion_destination_verified", metric.WithDescription("Whether a successful append or validation has verified the destination."))
	if err != nil {
		return nil, err
	}
	instruments = append(instruments, lastSuccess, verified)
	return e.meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		s := e.statuses.Snapshot().Signals[e.signal]
		attributes := metric.WithAttributes(attribute.String("signal", e.signal), attribute.String("table", s.Table), attribute.String("exporter", e.exporterID))
		observer.ObserveInt64(counters[0].instrument, int64(s.ConfirmedWrittenRecords), attributes)
		observer.ObserveInt64(counters[1].instrument, int64(s.UnknownRecords), attributes)
		timestamp := float64(0)
		if !s.LastWriteSuccess.IsZero() {
			timestamp = float64(s.LastWriteSuccess.UnixNano()) / 1e9
		}
		observer.ObserveFloat64(lastSuccess, timestamp, attributes)
		destinationVerified := int64(0)
		if s.DestinationVerified {
			destinationVerified = 1
		}
		observer.ObserveInt64(verified, destinationVerified, attributes)
		for reason, value := range map[string]uint64{
			"permanent_rejected": s.PermanentFailedRecords,
			"retry_exhausted":    s.RetryExhaustedRecords,
			"enqueue_failed":     s.EnqueueFailedRecords,
		} {
			observer.ObserveInt64(counters[2].instrument, int64(value), attributes, metric.WithAttributes(attribute.String("reason", reason)))
		}
		for reason, value := range s.InvalidItemsByReason {
			observer.ObserveInt64(counters[3].instrument, int64(value), attributes, metric.WithAttributes(attribute.String("reason", reason)))
		}
		return nil
	}, instruments...)
}
