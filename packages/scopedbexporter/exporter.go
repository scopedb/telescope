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
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

type dbExporter struct {
	cfg      *Config
	client   *Client
	logger   *zap.Logger
	signal   string
	statuses *StatusRegistry
}

func newDBExporter(cfg *Config, set exporter.Settings, signal string, statuses *StatusRegistry) (*dbExporter, error) {
	if !cfg.signalEnabled(signal) {
		return nil, fmt.Errorf("%s signal is not configured for the scopedb exporter", signal)
	}
	client, err := NewClient(cfg, set)
	if err != nil {
		return nil, err
	}
	statuses.configure(signal, cfg)

	return &dbExporter{
		cfg:      cfg,
		client:   client,
		logger:   set.Logger.Named("scopedbexporter"),
		signal:   signal,
		statuses: statuses,
	}, nil
}

func (e *dbExporter) start(ctx context.Context, _ component.Host) error {
	e.logger.Info(
		"Starting scopedb exporter",
		zap.String("endpoint", e.cfg.Endpoint),
		zap.String("signal", e.signal),
		zap.String("table", e.cfg.tableForSignal(e.signal)),
		zap.String("compression", e.cfg.compressionMode()),
	)

	if err := e.validateDestination(ctx); err != nil {
		if isTransientDestinationError(err) {
			e.logger.Warn(
				"ScopeDB destination is temporarily unavailable; starting exporter with destination unverified",
				zap.Error(err),
			)
			e.statuses.markReadyDegraded(e.signal, err)
			return nil
		}
		e.statuses.recordStartFailure(e.signal, err)
		return err
	}
	e.statuses.markReady(e.signal)
	return nil
}

func (e *dbExporter) shutdown(_ context.Context) error {
	e.statuses.markStopped(e.signal)
	e.client.Close()
	return nil
}

func (e *dbExporter) pushLogs(ctx context.Context, logs plog.Logs) error {
	started := time.Now().UTC()
	records := logs.LogRecordCount()
	payload, err := mapLogs(logs)
	if err != nil {
		permanentErr := consumererror.NewPermanent(err)
		e.statuses.recordWrite(signalLogs, records, started, permanentErr, true)
		return permanentErr
	}
	err = e.client.Send(ctx, signalLogs, payload)
	affectedRecords := records
	if uncommittedFrom, ok := uncommittedFromRecord(err); ok && uncommittedFrom > 0 {
		affectedRecords = records - uncommittedFrom
		err = consumererror.NewLogs(err, logsFromRecord(logs, uncommittedFrom))
	}
	e.statuses.recordWrite(signalLogs, affectedRecords, started, err, consumererror.IsPermanent(err))
	if err == nil {
		e.statuses.recordProbeSuccess(signalLogs, probeIDsFromPayload(payload))
	}
	return err
}

func (e *dbExporter) pushTraces(ctx context.Context, traces ptrace.Traces) error {
	started := time.Now().UTC()
	records := traces.SpanCount()
	payload, err := mapTraces(traces)
	if err != nil {
		permanentErr := consumererror.NewPermanent(err)
		e.statuses.recordWrite(signalTraces, records, started, permanentErr, true)
		return permanentErr
	}
	err = e.client.Send(ctx, signalTraces, payload)
	affectedRecords := records
	if uncommittedFrom, ok := uncommittedFromRecord(err); ok && uncommittedFrom > 0 {
		affectedRecords = records - uncommittedFrom
		err = consumererror.NewTraces(err, tracesFromRecord(traces, uncommittedFrom))
	}
	e.statuses.recordWrite(signalTraces, affectedRecords, started, err, consumererror.IsPermanent(err))
	if err == nil {
		e.statuses.recordProbeSuccess(signalTraces, probeIDsFromPayload(payload))
	}
	return err
}

func (e *dbExporter) pushMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	started := time.Now().UTC()
	records := metrics.DataPointCount()
	payload, err := mapMetrics(metrics)
	if err != nil {
		permanentErr := consumererror.NewPermanent(err)
		e.statuses.recordWrite(signalMetrics, records, started, permanentErr, true)
		return permanentErr
	}
	err = e.client.Send(ctx, signalMetrics, payload)
	affectedRecords := records
	if uncommittedFrom, ok := uncommittedFromRecord(err); ok && uncommittedFrom > 0 {
		affectedRecords = records - uncommittedFrom
		err = consumererror.NewMetrics(err, metricsFromRecord(metrics, uncommittedFrom))
	}
	e.statuses.recordWrite(signalMetrics, affectedRecords, started, err, consumererror.IsPermanent(err))
	if err == nil {
		e.statuses.recordProbeSuccess(signalMetrics, probeIDsFromPayload(payload))
	}
	return err
}
