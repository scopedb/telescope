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
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

func NewFactory() exporter.Factory {
	return NewFactoryWithStatus(DefaultStatusRegistry)
}

func NewFactoryWithStatus(statuses *StatusRegistry) exporter.Factory {
	if statuses == nil {
		statuses = NewStatusRegistry()
	}
	return exporter.NewFactory(
		typeStr,
		createDefaultConfig,
		exporter.WithLogs(func(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Logs, error) {
			return createLogsExporter(ctx, set, cfg, statuses)
		}, component.StabilityLevelDevelopment),
		exporter.WithTraces(func(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Traces, error) {
			return createTracesExporter(ctx, set, cfg, statuses)
		}, component.StabilityLevelDevelopment),
		exporter.WithMetrics(func(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Metrics, error) {
			return createMetricsExporter(ctx, set, cfg, statuses)
		}, component.StabilityLevelDevelopment),
	)
}

func createLogsExporter(ctx context.Context, set exporter.Settings, cfg component.Config, statuses *StatusRegistry) (exporter.Logs, error) {
	baseCfg := cfg.(*Config)
	exp, err := newDBExporter(baseCfg, set, signalLogs, statuses)
	if err != nil {
		return nil, err
	}

	return exporterhelper.NewLogs(
		ctx,
		set,
		cfg,
		exp.pushLogs,
		exporterhelper.WithCapabilities(consumer.Capabilities{MutatesData: false}),
		exporterhelper.WithStart(exp.start),
		exporterhelper.WithShutdown(exp.shutdown),
		exporterhelper.WithTimeout(baseCfg.Timeout),
		exporterhelper.WithRetry(baseCfg.RetryOnFailure),
		exporterhelper.WithQueue(baseCfg.SendingQueue),
	)
}

func createTracesExporter(ctx context.Context, set exporter.Settings, cfg component.Config, statuses *StatusRegistry) (exporter.Traces, error) {
	baseCfg := cfg.(*Config)
	exp, err := newDBExporter(baseCfg, set, signalTraces, statuses)
	if err != nil {
		return nil, err
	}

	return exporterhelper.NewTraces(
		ctx,
		set,
		cfg,
		exp.pushTraces,
		exporterhelper.WithCapabilities(consumer.Capabilities{MutatesData: false}),
		exporterhelper.WithStart(exp.start),
		exporterhelper.WithShutdown(exp.shutdown),
		exporterhelper.WithTimeout(baseCfg.Timeout),
		exporterhelper.WithRetry(baseCfg.RetryOnFailure),
		exporterhelper.WithQueue(baseCfg.SendingQueue),
	)
}

func createMetricsExporter(ctx context.Context, set exporter.Settings, cfg component.Config, statuses *StatusRegistry) (exporter.Metrics, error) {
	baseCfg := cfg.(*Config)
	exp, err := newDBExporter(baseCfg, set, signalMetrics, statuses)
	if err != nil {
		return nil, err
	}

	metricsExporter, err := exporterhelper.NewMetrics(
		ctx,
		set,
		cfg,
		exp.pushMetrics,
		exporterhelper.WithCapabilities(consumer.Capabilities{MutatesData: false}),
		exporterhelper.WithStart(exp.start),
		exporterhelper.WithShutdown(exp.shutdown),
		exporterhelper.WithTimeout(baseCfg.Timeout),
		exporterhelper.WithRetry(baseCfg.RetryOnFailure),
		exporterhelper.WithQueue(baseCfg.SendingQueue),
	)
	if err != nil {
		return nil, err
	}
	return &metricFilteringExporter{
		Metrics:  metricsExporter,
		statuses: statuses,
		logger:   set.Logger.Named("scopedbexporter"),
	}, nil
}

type metricFilteringExporter struct {
	exporter.Metrics
	statuses *StatusRegistry
	logger   *zap.Logger
}

func (e *metricFilteringExporter) ConsumeMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	filtered, failures := filterInvalidMetrics(metrics)
	if len(failures) == 0 {
		return e.Metrics.ConsumeMetrics(ctx, metrics)
	}

	var sendErr error
	if filtered.DataPointCount() > 0 {
		sendErr = e.Metrics.ConsumeMetrics(ctx, filtered)
	}

	started := time.Now().UTC()
	droppedPoints := 0
	for _, failure := range failures {
		droppedPoints += failure.dataPoints
		e.statuses.recordWrite(
			signalMetrics,
			failure.dataPoints,
			started,
			consumererror.NewPermanent(failure.err),
			true,
		)
	}
	e.logger.Warn(
		"Dropped invalid metrics while preserving the rest of the batch",
		zap.Int("invalid_metrics", len(failures)),
		zap.Int("dropped_metric_points", droppedPoints),
		zap.Error(failures[0].err),
	)
	return sendErr
}
