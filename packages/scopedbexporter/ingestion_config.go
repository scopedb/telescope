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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.uber.org/zap"
)

// SignalIngestionConfig routes one OpenTelemetry signal into a user-owned
// ScopeDB table.
type SignalIngestionConfig struct {
	Table   string        `yaml:"table"`
	Mapping MappingConfig `yaml:"mapping"`
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

// ContractDigest identifies the normalized signal routes and mapping semantics.
func (cfg IngestionConfig) ContractDigest() (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	type canonicalRule struct {
		Sources    []string `json:"sources,omitempty"`
		Cast       string   `json:"cast,omitempty"`
		Default    any      `json:"default,omitempty"`
		DefaultSet bool     `json:"default_set,omitempty"`
		Value      any      `json:"value,omitempty"`
		ValueSet   bool     `json:"value_set,omitempty"`
	}
	type canonicalSignal struct {
		Signal  string                   `json:"signal"`
		Table   string                   `json:"table"`
		Mapping map[string]canonicalRule `json:"mapping"`
	}
	contract := struct {
		Version int               `json:"version"`
		Signals []canonicalSignal `json:"signals"`
	}{Version: 1}

	for _, signal := range cfg.EnabledSignals() {
		signalConfig, _ := cfg.Signal(signal)
		canonical := canonicalSignal{
			Signal:  signal,
			Table:   strings.TrimSpace(signalConfig.Table),
			Mapping: make(map[string]canonicalRule, len(signalConfig.Mapping)),
		}
		for column, mapping := range signalConfig.Mapping {
			rule, err := mapping.normalized()
			if err != nil {
				return "", fmt.Errorf("%s.%s: %w", signal, column, err)
			}
			sources := rule.Sources
			if rule.Source != "" {
				sources = []string{rule.Source}
			}
			canonical.Mapping[column] = canonicalRule{
				Sources:    sources,
				Cast:       rule.Cast,
				Default:    rule.Default,
				DefaultSet: rule.hasDefault(),
				Value:      rule.Value,
				ValueSet:   rule.hasValue(),
			}
		}
		contract.Signals = append(contract.Signals, canonical)
	}

	encoded, err := json.Marshal(contract)
	if err != nil {
		return "", fmt.Errorf("encode ingestion contract: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
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
		table := signalConfig.Table
		if strings.TrimSpace(table) == "" {
			errs = append(errs, fmt.Errorf("%s.table is required", prefix))
			table = "placeholder"
		}
		if _, err := compileMappingPlan(signal, table, signalConfig.Mapping); err != nil {
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
	_, err := InspectIngestionDestinations(ctx, endpoint, apiKey, ingestion)
	return err
}

// InspectIngestionDestinations returns the catalog type check for each mapped
// column without starting OTLP listeners or writing data.
func InspectIngestionDestinations(
	ctx context.Context,
	endpoint string,
	apiKey string,
	ingestion IngestionConfig,
) ([]SignalDestinationValidation, error) {
	if err := ingestion.Validate(); err != nil {
		return nil, err
	}
	client, err := newIngestionClient(endpoint, apiKey, ingestion)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var errs []error
	validations := make([]SignalDestinationValidation, 0, len(ingestion.EnabledSignals()))
	for _, signal := range ingestion.EnabledSignals() {
		validation, err := client.inspectDestination(ctx, signal)
		if validation.Signal != "" {
			validations = append(validations, validation)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", signal, err))
		}
	}
	return validations, errors.Join(errs...)
}

func newIngestionClient(endpoint string, apiKey string, ingestion IngestionConfig) (*Client, error) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = endpoint
	cfg.APIKey = configopaque.String(apiKey)
	cfg.Tables = TableRoutingConfig{
		Logs:    ingestion.Signals.Logs.Table,
		Traces:  ingestion.Signals.Traces.Table,
		Metrics: ingestion.Signals.Metrics.Table,
	}
	cfg.Mappings = SignalMappingConfig{
		Logs:    ingestion.Signals.Logs.Mapping,
		Traces:  ingestion.Signals.Traces.Mapping,
		Metrics: ingestion.Signals.Metrics.Mapping,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return newClient(cfg, zap.NewNop())
}
