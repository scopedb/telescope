package vendordbexporter

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
	defaultDataset       = "default"
	defaultSchemaVersion = "v1"
	defaultCompression   = "zstd"
	defaultLogsTable     = "scopedb.otel.logs"
	defaultTracesTable   = "scopedb.otel.traces"
	defaultMetricsTable  = "scopedb.otel.metrics"
)

var typeStr = component.MustNewType("vendordb")

var tablePartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type TableRoutingConfig struct {
	Default string `mapstructure:"default"`
	Logs    string `mapstructure:"logs"`
	Traces  string `mapstructure:"traces"`
	Metrics string `mapstructure:"metrics"`
}

type Config struct {
	Endpoint               string                                                   `mapstructure:"endpoint"`
	Path                   string                                                   `mapstructure:"path"`
	APIKey                 configopaque.String                                      `mapstructure:"api_key"`
	Dataset                string                                                   `mapstructure:"dataset"`
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
		Path:    "/v1/ingest",
		Dataset: defaultDataset,
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

	if strings.TrimSpace(cfg.Dataset) == "" {
		errs = append(errs, errors.New("dataset is required"))
	}

	if len(cfg.configuredTables()) == 0 {
		errs = append(errs, errors.New("at least one table route must be configured"))
	}

	for name, table := range map[string]string{
		"tables.default": cfg.Tables.Default,
		"tables.logs":    cfg.Tables.Logs,
		"tables.traces":  cfg.Tables.Traces,
		"tables.metrics": cfg.Tables.Metrics,
	} {
		if strings.TrimSpace(table) == "" {
			continue
		}
		if _, err := parseTableRef(table); err != nil {
			errs = append(errs, fmt.Errorf("%s %w", name, err))
		}
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
		if strings.TrimSpace(cfg.Tables.Logs) != "" {
			return cfg.Tables.Logs
		}
	case signalTraces:
		if strings.TrimSpace(cfg.Tables.Traces) != "" {
			return cfg.Tables.Traces
		}
	case signalMetrics:
		if strings.TrimSpace(cfg.Tables.Metrics) != "" {
			return cfg.Tables.Metrics
		}
	}

	return cfg.Tables.Default
}

func (cfg *Config) configuredTables() []string {
	candidates := []string{
		cfg.Tables.Default,
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
