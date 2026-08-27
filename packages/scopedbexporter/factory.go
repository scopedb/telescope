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
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

func NewFactory() exporter.Factory {
	return newFactory(DefaultStatusRegistry, DefaultCaptureRegistry)
}

func NewFactoryWithStatus(statuses *StatusRegistry) exporter.Factory {
	return newFactory(statuses, NewCaptureRegistry())
}

func newFactory(statuses *StatusRegistry, captures *CaptureRegistry) exporter.Factory {
	if statuses == nil {
		statuses = NewStatusRegistry()
	}
	if captures == nil {
		captures = NewCaptureRegistry()
	}
	return exporter.NewFactory(
		typeStr,
		createDefaultConfig,
		exporter.WithLogs(func(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Logs, error) {
			return createLogsExporter(ctx, set, cfg, statuses, captures)
		}, component.StabilityLevelDevelopment),
		exporter.WithTraces(func(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Traces, error) {
			return createTracesExporter(ctx, set, cfg, statuses, captures)
		}, component.StabilityLevelDevelopment),
		exporter.WithMetrics(func(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Metrics, error) {
			return createMetricsExporter(ctx, set, cfg, statuses, captures)
		}, component.StabilityLevelDevelopment),
	)
}

func createLogsExporter(ctx context.Context, set exporter.Settings, cfg component.Config, statuses *StatusRegistry, captures *CaptureRegistry) (exporter.Logs, error) {
	baseCfg := cfg.(*Config)
	exp, err := newDBExporter(baseCfg, set, signalLogs, statuses)
	if err != nil {
		return nil, err
	}

	logsExporter, err := exporterhelper.NewLogs(
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
	if err != nil {
		return nil, err
	}
	return &capturingLogsExporter{Logs: logsExporter, captures: captures}, nil
}

func createTracesExporter(ctx context.Context, set exporter.Settings, cfg component.Config, statuses *StatusRegistry, captures *CaptureRegistry) (exporter.Traces, error) {
	baseCfg := cfg.(*Config)
	exp, err := newDBExporter(baseCfg, set, signalTraces, statuses)
	if err != nil {
		return nil, err
	}

	tracesExporter, err := exporterhelper.NewTraces(
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
	if err != nil {
		return nil, err
	}
	return &capturingTracesExporter{Traces: tracesExporter, captures: captures}, nil
}

func createMetricsExporter(ctx context.Context, set exporter.Settings, cfg component.Config, statuses *StatusRegistry, captures *CaptureRegistry) (exporter.Metrics, error) {
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
		captures: captures,
		logger:   set.Logger.Named("scopedbexporter"),
	}, nil
}

type capturingLogsExporter struct {
	exporter.Logs
	captures *CaptureRegistry
}

func (e *capturingLogsExporter) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
	e.captures.ObserveLogs(logs)
	return e.Logs.ConsumeLogs(ctx, logs)
}

type capturingTracesExporter struct {
	exporter.Traces
	captures *CaptureRegistry
}

func (e *capturingTracesExporter) ConsumeTraces(ctx context.Context, traces ptrace.Traces) error {
	e.captures.ObserveTraces(traces)
	return e.Traces.ConsumeTraces(ctx, traces)
}

type metricFilteringExporter struct {
	exporter.Metrics
	statuses *StatusRegistry
	captures *CaptureRegistry
	logger   *zap.Logger
}

func (e *metricFilteringExporter) ConsumeMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	e.captures.ObserveMetrics(metrics)
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
