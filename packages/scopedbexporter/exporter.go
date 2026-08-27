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
	return pushSignal(e, ctx, signalLogs, logs, logs.LogRecordCount(), mapLogs, func(err error, from int) error {
		return consumererror.NewLogs(err, logsFromRecord(logs, from))
	})
}

func (e *dbExporter) pushTraces(ctx context.Context, traces ptrace.Traces) error {
	return pushSignal(e, ctx, signalTraces, traces, traces.SpanCount(), mapTraces, func(err error, from int) error {
		return consumererror.NewTraces(err, tracesFromRecord(traces, from))
	})
}

func (e *dbExporter) pushMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	return pushSignal(e, ctx, signalMetrics, metrics, metrics.DataPointCount(), mapMetrics, func(err error, from int) error {
		return consumererror.NewMetrics(err, metricsFromRecord(metrics, from))
	})
}

func pushSignal[T any](
	exporter *dbExporter,
	ctx context.Context,
	signal string,
	data T,
	records int,
	mapData func(T) (*IngestPayload, error),
	wrapSuffix func(error, int) error,
) error {
	started := time.Now().UTC()
	payload, err := mapData(data)
	if err != nil {
		permanentErr := consumererror.NewPermanent(err)
		exporter.statuses.recordWrite(signal, records, started, permanentErr, true)
		return permanentErr
	}
	err = exporter.client.Send(ctx, signal, payload)
	affectedRecords := records
	if uncommittedFrom, ok := uncommittedFromRecord(err); ok && uncommittedFrom > 0 {
		affectedRecords = records - uncommittedFrom
		err = wrapSuffix(err, uncommittedFrom)
	}
	exporter.statuses.recordWrite(signal, affectedRecords, started, err, consumererror.IsPermanent(err))
	if consumererror.IsPermanent(err) {
		exporter.statuses.recordPermanentExport(signal, records)
	}
	if err == nil {
		exporter.statuses.recordProbeSuccess(signal, probeIDsFromPayload(payload))
	}
	return err
}
