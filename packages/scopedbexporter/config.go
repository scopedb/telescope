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
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

const (
	defaultPath          = "/v1/ingest"
	defaultEnv           = "default"
	defaultSchemaVersion = "v1"
	defaultCompression   = "zstd"
	defaultLogsTable     = "scopedb.otel.logs"
	defaultTracesTable   = "scopedb.otel.traces"
	defaultMetricsTable  = "scopedb.otel.metrics"
)

var typeStr = component.MustNewType("scopedb")

var tablePartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type TableRoutingConfig struct {
	Logs    string `mapstructure:"logs"`
	Traces  string `mapstructure:"traces"`
	Metrics string `mapstructure:"metrics"`
}

type Config struct {
	Endpoint               string                                                   `mapstructure:"endpoint"`
	Path                   string                                                   `mapstructure:"path"`
	APIKey                 configopaque.String                                      `mapstructure:"api_key"`
	Env                    string                                                   `mapstructure:"env"`
	Tables                 TableRoutingConfig                                       `mapstructure:"tables"`
	CreateTablesIfNotExist bool                                                     `mapstructure:"create_tables_if_not_exist"`
	SchemaVersion          string                                                   `mapstructure:"schema_version"`
	Compression            string                                                   `mapstructure:"compression"`
	Timeout                exporterhelper.TimeoutConfig                             `mapstructure:",squash"`
	RetryOnFailure         configretry.BackOffConfig                                `mapstructure:"retry_on_failure"`
	SendingQueue           configoptional.Optional[exporterhelper.QueueBatchConfig] `mapstructure:"sending_queue"`
}

func createDefaultConfig() component.Config {
	retryCfg := configretry.NewDefaultBackOffConfig()
	retryCfg.Enabled = true
	retryCfg.InitialInterval = time.Second
	retryCfg.MaxInterval = 30 * time.Second
	retryCfg.MaxElapsedTime = 0

	queueCfg := exporterhelper.NewDefaultQueueConfig()
	queueCfg.QueueSize = 10_000
	queueCfg.NumConsumers = 4
	queueCfg.Batch = configoptional.None[exporterhelper.BatchConfig]()

	return &Config{
		Path: "/v1/ingest",
		Env:  defaultEnv,
		Tables: TableRoutingConfig{
			Logs:    defaultLogsTable,
			Traces:  defaultTracesTable,
			Metrics: defaultMetricsTable,
		},
		SchemaVersion:  defaultSchemaVersion,
		Compression:    defaultCompression,
		Timeout:        exporterhelper.TimeoutConfig{Timeout: 10 * time.Second},
		RetryOnFailure: retryCfg,
		SendingQueue:   configoptional.Some(queueCfg),
	}
}

func (cfg *Config) Validate() error {
	var errs []error

	if strings.TrimSpace(cfg.Endpoint) == "" {
		errs = append(errs, errors.New("endpoint is required"))
	} else {
		parsed, err := url.Parse(cfg.Endpoint)
		if err != nil {
			errs = append(errs, fmt.Errorf("endpoint must be a valid URL: %w", err))
		} else if parsed.Scheme == "" || parsed.Host == "" {
			errs = append(errs, errors.New("endpoint must include scheme and host"))
		}
	}

	if strings.TrimSpace(cfg.Path) == "" {
		errs = append(errs, errors.New("path is required"))
	} else if !strings.HasPrefix(cfg.Path, "/") {
		errs = append(errs, errors.New("path must start with '/'"))
	}

	if strings.TrimSpace(string(cfg.APIKey)) == "" {
		errs = append(errs, errors.New("api_key is required"))
	}

	if strings.TrimSpace(cfg.Env) == "" {
		errs = append(errs, errors.New("env is required"))
	}

	for name, table := range map[string]string{
		"tables.logs":    cfg.Tables.Logs,
		"tables.traces":  cfg.Tables.Traces,
		"tables.metrics": cfg.Tables.Metrics,
	} {
		if strings.TrimSpace(table) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
			continue
		}
		if _, err := parseTableRef(table); err != nil {
			errs = append(errs, fmt.Errorf("%s %w", name, err))
		}
	}

	if strings.TrimSpace(cfg.Tables.Logs) != "" &&
		strings.TrimSpace(cfg.Tables.Logs) == strings.TrimSpace(cfg.Tables.Traces) {
		errs = append(errs, errors.New("tables.logs and tables.traces must point to different tables"))
	}
	if strings.TrimSpace(cfg.Tables.Logs) != "" &&
		strings.TrimSpace(cfg.Tables.Logs) == strings.TrimSpace(cfg.Tables.Metrics) {
		errs = append(errs, errors.New("tables.logs and tables.metrics must point to different tables"))
	}
	if strings.TrimSpace(cfg.Tables.Traces) != "" &&
		strings.TrimSpace(cfg.Tables.Traces) == strings.TrimSpace(cfg.Tables.Metrics) {
		errs = append(errs, errors.New("tables.traces and tables.metrics must point to different tables"))
	}

	if strings.TrimSpace(cfg.SchemaVersion) == "" {
		errs = append(errs, errors.New("schema_version is required"))
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Compression)) {
	case "none", "gzip", "zstd":
	default:
		errs = append(errs, fmt.Errorf("unsupported compression %q", cfg.Compression))
	}

	if err := cfg.Timeout.Validate(); err != nil {
		errs = append(errs, err)
	}

	if err := cfg.RetryOnFailure.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("retry_on_failure: %w", err))
	}

	if cfg.SendingQueue.HasValue() {
		if err := cfg.SendingQueue.Get().Validate(); err != nil {
			errs = append(errs, fmt.Errorf("sending_queue: %w", err))
		}
	}

	return errors.Join(errs...)
}

func (cfg *Config) compressionMode() string {
	mode := strings.ToLower(strings.TrimSpace(cfg.Compression))
	if mode == "" {
		return "none"
	}
	return mode
}

func (cfg *Config) compressionEnabled() bool {
	return cfg.compressionMode() != "none"
}

func (cfg *Config) tableForSignal(signal string) string {
	switch signal {
	case signalLogs:
		return cfg.Tables.Logs
	case signalTraces:
		return cfg.Tables.Traces
	case signalMetrics:
		return cfg.Tables.Metrics
	}

	return ""
}

func (cfg *Config) configuredTables() []string {
	candidates := []string{
		cfg.Tables.Logs,
		cfg.Tables.Traces,
		cfg.Tables.Metrics,
	}

	seen := make(map[string]struct{}, len(candidates))
	tables := make([]string, 0, len(candidates))
	for _, table := range candidates {
		table = strings.TrimSpace(table)
		if table == "" {
			continue
		}
		if _, ok := seen[table]; ok {
			continue
		}
		seen[table] = struct{}{}
		tables = append(tables, table)
	}
	return tables
}
