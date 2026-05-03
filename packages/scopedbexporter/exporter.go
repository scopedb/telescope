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

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

type dbExporter struct {
	cfg    *Config
	client *Client
	logger *zap.Logger
}

func newDBExporter(cfg *Config, set exporter.Settings) (*dbExporter, error) {
	client, err := NewClient(cfg, set)
	if err != nil {
		return nil, err
	}

	return &dbExporter{
		cfg:    cfg,
		client: client,
		logger: set.Logger.Named("scopedbexporter"),
	}, nil
}

func (e *dbExporter) start(ctx context.Context, _ component.Host) error {
	e.logger.Info(
		"Starting scopedb exporter",
		zap.String("endpoint", e.cfg.Endpoint),
		zap.String("path", e.cfg.Path),
		zap.Strings("tables", e.cfg.configuredTables()),
		zap.Bool("create_tables_if_not_exist", e.cfg.CreateTablesIfNotExist),
		zap.String("compression", e.cfg.Compression),
	)

	return e.ensureTable(ctx)
}

func (e *dbExporter) shutdown(_ context.Context) error {
	e.client.Close()
	return nil
}

func (e *dbExporter) pushLogs(ctx context.Context, logs plog.Logs) error {
	payload, err := mapLogs(e.cfg, logs)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	return e.client.Send(ctx, signalLogs, payload)
}

func (e *dbExporter) pushTraces(ctx context.Context, traces ptrace.Traces) error {
	payload, err := mapTraces(e.cfg, traces)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	return e.client.Send(ctx, signalTraces, payload)
}

func (e *dbExporter) pushMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	payload, err := mapMetrics(e.cfg, metrics)
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	return e.client.Send(ctx, signalMetrics, payload)
}
