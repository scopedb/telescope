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

package collector

import (
	"context"
	"time"

	"github.com/scopedb/telescope/scopedbexporter"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/service/telemetry"
)

// Runtime applies one shutdown budget across all pipelines, reserving its last
// fifth (up to five seconds) for closing storage and flushing final metrics.
// Collector otherwise shuts components down with an unbounded context.
type Runtime struct {
	*otelcol.Collector
	drain       context.Context
	cancelDrain context.CancelFunc
	flush       context.Context
	cancelFlush context.CancelFunc
}

func New(configURI, version string, statuses *scopedbexporter.StatusRegistry, captures *scopedbexporter.CaptureRegistry) (*Runtime, error) {
	r := &Runtime{}
	r.drain, r.cancelDrain = context.WithCancel(context.Background())
	r.flush, r.cancelFlush = context.WithCancel(context.Background())
	settings := newSettings(configURI, version, true, statuses, captures)
	settings.Factories = func() (otelcol.Factories, error) {
		f, err := factories(version, statuses, captures)
		if err != nil {
			return f, err
		}
		for id, factory := range f.Exporters {
			f.Exporters[id] = shutdownExporterFactory{Factory: factory, ctx: r.drain}
		}
		f.Telemetry = shutdownTelemetryFactory{Factory: f.Telemetry, ctx: r.flush}
		return f, nil
	}
	var err error
	r.Collector, err = otelcol.NewCollector(settings)
	if err != nil {
		r.cancelDrain()
		r.cancelFlush()
		return nil, err
	}
	return r, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	defer r.cancelDrain()
	defer r.cancelFlush()
	return r.Collector.Run(ctx)
}

func (r *Runtime) Shutdown(ctx context.Context) {
	context.AfterFunc(ctx, r.cancelDrain)
	context.AfterFunc(ctx, r.cancelFlush)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		timer := time.AfterFunc(remaining-min(5*time.Second, remaining/5), r.cancelDrain)
		context.AfterFunc(r.drain, func() { timer.Stop() })
	}
	r.Collector.Shutdown()
}

// Bind shutdown contexts at the component boundary. Startup and request
// contexts retain their original cancellation and tracing semantics.
type shutdownExporterFactory struct {
	exporter.Factory
	ctx context.Context
}

func (f shutdownExporterFactory) CreateLogs(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Logs, error) {
	e, err := f.Factory.CreateLogs(ctx, set, cfg)
	if err != nil {
		return nil, err
	}
	return shutdownLogs{Logs: e, ctx: f.ctx}, nil
}

func (f shutdownExporterFactory) CreateTraces(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Traces, error) {
	e, err := f.Factory.CreateTraces(ctx, set, cfg)
	if err != nil {
		return nil, err
	}
	return shutdownTraces{Traces: e, ctx: f.ctx}, nil
}

func (f shutdownExporterFactory) CreateMetrics(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Metrics, error) {
	e, err := f.Factory.CreateMetrics(ctx, set, cfg)
	if err != nil {
		return nil, err
	}
	return shutdownMetrics{Metrics: e, ctx: f.ctx}, nil
}

type shutdownLogs struct {
	exporter.Logs
	ctx context.Context
}

func (e shutdownLogs) Shutdown(context.Context) error { return e.Logs.Shutdown(e.ctx) }

type shutdownTraces struct {
	exporter.Traces
	ctx context.Context
}

func (e shutdownTraces) Shutdown(context.Context) error { return e.Traces.Shutdown(e.ctx) }

type shutdownMetrics struct {
	exporter.Metrics
	ctx context.Context
}

func (e shutdownMetrics) Shutdown(context.Context) error { return e.Metrics.Shutdown(e.ctx) }

type shutdownTelemetryFactory struct {
	telemetry.Factory
	ctx context.Context
}

func (f shutdownTelemetryFactory) CreateMeterProvider(ctx context.Context, set telemetry.MeterSettings, cfg component.Config) (telemetry.MeterProvider, error) {
	provider, err := f.Factory.CreateMeterProvider(ctx, set, cfg)
	if err != nil {
		return nil, err
	}
	return shutdownMeterProvider{MeterProvider: provider, ctx: f.ctx}, nil
}

type shutdownMeterProvider struct {
	telemetry.MeterProvider
	ctx context.Context
}

func (p shutdownMeterProvider) Shutdown(context.Context) error {
	return p.MeterProvider.Shutdown(p.ctx)
}
