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

// IngestionConfig is Telescope's user-owned table routing and source mapping.
type IngestionConfig struct {
	Tables   TableRoutingConfig  `yaml:"tables"`
	Mappings SignalMappingConfig `yaml:"mappings"`
}

// StarterIngestionConfig returns the explicit starter profile. It is an
// example layout, not a universal ScopeDB telemetry schema.
func StarterIngestionConfig() IngestionConfig {
	return IngestionConfig{
		Tables: TableRoutingConfig{
			Logs:    defaultLogsTable,
			Traces:  defaultTracesTable,
			Metrics: defaultMetricsTable,
		},
		Mappings: defaultSignalMappings(),
	}
}

func (cfg IngestionConfig) Validate() error {
	var errs []error
	for _, route := range []struct {
		name  string
		table string
	}{
		{name: "tables.logs", table: cfg.Tables.Logs},
		{name: "tables.traces", table: cfg.Tables.Traces},
		{name: "tables.metrics", table: cfg.Tables.Metrics},
	} {
		if strings.TrimSpace(route.table) == "" {
			errs = append(errs, fmt.Errorf("%s is required", route.name))
			continue
		}
		if _, err := parseTableRef(route.table); err != nil {
			errs = append(errs, fmt.Errorf("%s %w", route.name, err))
		}
	}

	for _, signal := range []string{signalLogs, signalTraces, signalMetrics} {
		if _, err := compileMappingPlan(signal, cfg.tableForSignal(signal), cfg.mappingForSignal(signal)); err != nil {
			errs = append(errs, fmt.Errorf("mappings.%s: %w", signal, err))
		}
	}
	return errors.Join(errs...)
}

// CheckIngestionDestinations validates an ingestion mapping against its live
// ScopeDB tables without starting OTLP listeners or writing data.
func CheckIngestionDestinations(ctx context.Context, endpoint string, apiKey string, ingestion IngestionConfig) error {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = endpoint
	cfg.APIKey = configopaque.String(apiKey)
	cfg.Tables = ingestion.Tables
	cfg.Mappings = ingestion.Mappings
	if err := cfg.Validate(); err != nil {
		return err
	}

	client, err := newClient(cfg, zap.NewNop())
	if err != nil {
		return err
	}
	defer client.Close()

	var errs []error
	for _, signal := range []string{signalLogs, signalTraces, signalMetrics} {
		if err := client.ValidateDestination(ctx, signal); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", signal, err))
		}
	}
	return errors.Join(errs...)
}

func (cfg IngestionConfig) tableForSignal(signal string) string {
	switch signal {
	case signalLogs:
		return cfg.Tables.Logs
	case signalTraces:
		return cfg.Tables.Traces
	case signalMetrics:
		return cfg.Tables.Metrics
	default:
		return ""
	}
}

func (cfg IngestionConfig) mappingForSignal(signal string) map[string]string {
	switch signal {
	case signalLogs:
		return cfg.Mappings.Logs
	case signalTraces:
		return cfg.Mappings.Traces
	case signalMetrics:
		return cfg.Mappings.Metrics
	default:
		return nil
	}
}
