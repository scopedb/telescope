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
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

type dbExporter struct {
	meter        metric.Meter
	exporterID   string
	stopping     atomic.Bool
	persistent   bool
	queueEnabled bool
	cfg          *Config
	client       *appendClient
	logger       *zap.Logger
	signal       string
	statuses     *StatusRegistry
}

func newDBExporter(cfg *Config, set exporter.Settings, signal string, statuses *StatusRegistry) (*dbExporter, error) {
	if _, enabled := cfg.signal(signal); !enabled {
		return nil, fmt.Errorf("%s signal is not configured for the scopedb exporter", signal)
	}
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	if statuses == nil {
		statuses = NewStatusRegistry()
	}
	statuses.configure(signal, cfg)

	exp := &dbExporter{
		persistent:   cfg.SendingQueue.HasValue() && cfg.SendingQueue.Get().StorageID != nil,
		queueEnabled: cfg.SendingQueue.HasValue(),
		cfg:          cfg,
		client:       client,
		logger:       set.Logger.Named("scopedbexporter"),
		signal:       signal,
		statuses:     statuses,
	}
	if set.MeterProvider != nil {
		exp.meter = set.MeterProvider.Meter("github.com/scopedb/telescope/scopedbexporter")
	}
	exp.exporterID = set.ID.String()
	return exp, nil
}

func (e *dbExporter) start(_ context.Context, _ component.Host) error {
	if e.meter != nil {
		if err := e.registerDeliveryMetrics(); err != nil {
			return fmt.Errorf("register delivery metrics: %w", err)
		}
	}
	signalConfig, _ := e.cfg.signal(e.signal)
	e.logger.Info(
		"Starting scopedb exporter",
		zap.String("endpoint", e.cfg.Endpoint),
		zap.String("signal", e.signal),
		zap.String("table", signalConfig.Table),
		zap.String("compression", e.cfg.compressionMode()),
	)

	// Destination checks belong to `telescope validate`. Runtime admission must
	// not depend on the downstream service being available during a restart.
	e.statuses.update(e.signal, func(status *SignalRuntimeStatus) { status.Ready = true })
	return nil
}

func (e *dbExporter) shutdown(_ context.Context) error {
	e.statuses.markStopped(e.signal)
	e.client.Close()
	s := e.statuses.Snapshot().Signals[e.signal]
	log := e.logger.Info
	if s.AmbiguousRecords+s.PermanentFailedRecords+s.RetryExhaustedRecords+s.EnqueueFailedRecords > 0 {
		log = e.logger.Warn
	}
	log("ScopeDB delivery summary",
		zap.String("signal", e.signal), zap.Uint64("written_records", s.ConfirmedWrittenRecords),
		zap.Uint64("ambiguous_records", s.AmbiguousRecords), zap.Uint64("permanent_failed_records", s.PermanentFailedRecords),
		zap.Uint64("retry_exhausted_records", s.RetryExhaustedRecords), zap.Uint64("enqueue_failed_records", s.EnqueueFailedRecords),
		zap.Bool("persistent_queue", e.persistent))
	return nil
}

func (e *dbExporter) recordAmbiguous(records int, started time.Time, err error) {
	e.statuses.recordAmbiguous(e.signal, records, started, err)
	if e.logger != nil {
		e.logger.Warn("ScopeDB append outcome is ambiguous; these records will not be retried",
			zap.String("signal", e.signal), zap.Int("ambiguous_records", records), zap.Error(err))
	}
}

func (e *dbExporter) recordDelivery(payload *ingestPayload, result deliveryResult, started time.Time) {
	if len(result.succeeded) > 0 {
		e.statuses.recordWrite(e.signal, len(result.succeeded), started, nil, false)
		e.statuses.recordProbeSuccess(e.signal, probeIDsFromRecords(payload, result.succeeded))
	}
	failedRecords := 0
	for _, failure := range result.failed {
		e.statuses.recordWrite(e.signal, failure.records, started, failure.err, true)
		failedRecords += failure.records
	}
	if len(result.failed) > 0 {
		e.logger.Warn("Telemetry records failed delivery",
			zap.String("signal", e.signal), zap.Int("failed_records", failedRecords), zap.Error(result.failed[0].err))
	}
	for _, failure := range result.ambiguous {
		e.recordAmbiguous(failure.records, started, failure.err)
	}
	if result.replayAttempts > 0 {
		e.statuses.update(e.signal, func(s *SignalRuntimeStatus) {
			s.AmbiguousReplayAttempts += uint64(result.replayAttempts)
			s.AmbiguousReplayedRecords += uint64(result.replayedRecords)
		})
	}
	if result.err != nil {
		e.statuses.recordWrite(e.signal, 0, started, result.err, false)
	}
}
