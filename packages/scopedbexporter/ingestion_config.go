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
	"errors"
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.uber.org/zap"
)

const (
	starterLogsTable    = "scopedb.otel.logs"
	starterTracesTable  = "scopedb.otel.traces"
	starterMetricsTable = "scopedb.otel.metrics"
)

func starterSignalMappings() SignalMappingConfig {
	return SignalMappingConfig{
		Logs: map[string]string{
			"record_timestamp":   "log.timestamp",
			"observed_timestamp": "log.observed_timestamp",
			"trace_id":           "log.trace_id",
			"span_id":            "log.span_id",
			"service":            `resource.attributes["service.name"]`,
			"status":             "log.severity_text",
			"severity_number":    "log.severity_number",
			"message":            "log.message",
		},
		Traces: map[string]string{
			"start_timestamp": "span.start_time",
			"end_timestamp":   "span.end_time",
			"trace_id":        "span.trace_id",
			"span_id":         "span.span_id",
			"parent_span_id":  "span.parent_span_id",
			"service":         `resource.attributes["service.name"]`,
			"span_name":       "span.name",
			"span_kind":       "span.kind",
			"status_code":     "span.status.code",
			"duration_ns":     "span.duration_ns",
		},
		Metrics: map[string]string{
			"record_timestamp": "datapoint.timestamp",
			"start_timestamp":  "datapoint.start_time",
			"service":          `resource.attributes["service.name"]`,
			"metric_name":      "metric.name",
			"metric_type":      "metric.type",
			"temporality":      "metric.temporality",
			"unit":             "metric.unit",
			"int_value":        "datapoint.int_value",
			"double_value":     "datapoint.double_value",
			"distribution":     "datapoint.distribution",
		},
	}
}

// SignalIngestionConfig routes one OpenTelemetry signal into a user-owned
// ScopeDB table.
type SignalIngestionConfig struct {
	Table   string            `yaml:"table"`
	Mapping map[string]string `yaml:"mapping"`
}

type IngestionSignalsConfig struct {
	Logs    SignalIngestionConfig `yaml:"logs,omitempty"`
	Traces  SignalIngestionConfig `yaml:"traces,omitempty"`
	Metrics SignalIngestionConfig `yaml:"metrics,omitempty"`
}

// IngestionConfig is Telescope's user-facing table routing and source mapping.
// Signals that are absent from this config are not accepted by the embedded
// Collector.
type IngestionConfig struct {
	Signals IngestionSignalsConfig `yaml:"signals"`
}

// StarterIngestionConfig returns the explicit starter profile. It is an
// example layout, not a universal ScopeDB telemetry schema.
func StarterIngestionConfig() IngestionConfig {
	mappings := starterSignalMappings()
	return IngestionConfig{Signals: IngestionSignalsConfig{
		Logs: SignalIngestionConfig{
			Table:   starterLogsTable,
			Mapping: mappings.Logs,
		},
		Traces: SignalIngestionConfig{
			Table:   starterTracesTable,
			Mapping: mappings.Traces,
		},
		Metrics: SignalIngestionConfig{
			Table:   starterMetricsTable,
			Mapping: mappings.Metrics,
		},
	}}
}

func (cfg IngestionConfig) Validate() error {
	var errs []error
	if len(cfg.EnabledSignals()) == 0 {
		errs = append(errs, errors.New("at least one signal is required"))
	}

	for _, signal := range []string{signalLogs, signalTraces, signalMetrics} {
		signalConfig, enabled := cfg.Signal(signal)
		if !enabled {
			continue
		}
		prefix := "signals." + signal
		if strings.TrimSpace(signalConfig.Table) == "" {
			errs = append(errs, fmt.Errorf("%s.table is required", prefix))
			continue
		}
		if _, err := compileMappingPlan(signal, signalConfig.Table, signalConfig.Mapping); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", prefix, err))
		}
	}
	return errors.Join(errs...)
}

// EnabledSignals returns configured signals in stable OTLP order.
func (cfg IngestionConfig) EnabledSignals() []string {
	enabled := make([]string, 0, 3)
	for _, signal := range []string{signalLogs, signalTraces, signalMetrics} {
		if _, ok := cfg.Signal(signal); ok {
			enabled = append(enabled, signal)
		}
	}
	return enabled
}

// Signal returns a signal config and whether that signal was configured.
func (cfg IngestionConfig) Signal(signal string) (SignalIngestionConfig, bool) {
	var signalConfig SignalIngestionConfig
	switch signal {
	case signalLogs:
		signalConfig = cfg.Signals.Logs
	case signalTraces:
		signalConfig = cfg.Signals.Traces
	case signalMetrics:
		signalConfig = cfg.Signals.Metrics
	default:
		return SignalIngestionConfig{}, false
	}
	configured := strings.TrimSpace(signalConfig.Table) != "" || signalConfig.Mapping != nil
	return signalConfig, configured
}

// CheckIngestionDestinations validates an ingestion mapping against its live
// ScopeDB tables without starting OTLP listeners or writing data.
func CheckIngestionDestinations(ctx context.Context, endpoint string, apiKey string, ingestion IngestionConfig) error {
	if err := ingestion.Validate(); err != nil {
		return err
	}
	tables, mappings := ingestion.exporterConfig()
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = endpoint
	cfg.APIKey = configopaque.String(apiKey)
	cfg.Tables = tables
	cfg.Mappings = mappings
	if err := cfg.Validate(); err != nil {
		return err
	}

	client, err := newClient(cfg, zap.NewNop())
	if err != nil {
		return err
	}
	defer client.Close()

	var errs []error
	for _, signal := range ingestion.EnabledSignals() {
		if err := client.ValidateDestination(ctx, signal); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", signal, err))
		}
	}
	return errors.Join(errs...)
}

func (cfg IngestionConfig) exporterConfig() (TableRoutingConfig, SignalMappingConfig) {
	var tables TableRoutingConfig
	var mappings SignalMappingConfig
	for _, signal := range cfg.EnabledSignals() {
		signalConfig, _ := cfg.Signal(signal)
		switch signal {
		case signalLogs:
			tables.Logs = signalConfig.Table
			mappings.Logs = signalConfig.Mapping
		case signalTraces:
			tables.Traces = signalConfig.Table
			mappings.Traces = signalConfig.Mapping
		case signalMetrics:
			tables.Metrics = signalConfig.Table
			mappings.Metrics = signalConfig.Mapping
		}
	}
	return tables, mappings
}

func ingestionConfigFromExporter(tables TableRoutingConfig, mappings SignalMappingConfig) IngestionConfig {
	return IngestionConfig{Signals: IngestionSignalsConfig{
		Logs: SignalIngestionConfig{
			Table:   tables.Logs,
			Mapping: mappings.Logs,
		},
		Traces: SignalIngestionConfig{
			Table:   tables.Traces,
			Mapping: mappings.Traces,
		},
		Metrics: SignalIngestionConfig{
			Table:   tables.Metrics,
			Mapping: mappings.Metrics,
		},
	}}
}
