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
	defaultCompression   = "gzip"
	defaultTable         = "public.vendor_otel_raw"
)

var typeStr = component.MustNewType("vendordb")

var tableIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*){1,2}$`)

type Config struct {
	Endpoint               string                                                   `mapstructure:"endpoint"`
	Path                   string                                                   `mapstructure:"path"`
	APIKey                 configopaque.String                                      `mapstructure:"api_key"`
	Dataset                string                                                   `mapstructure:"dataset"`
	Table                  string                                                   `mapstructure:"table"`
	CreateTableIfNotExists bool                                                     `mapstructure:"create_table_if_not_exists"`
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
		Path:           "/v1/ingest",
		Dataset:        defaultDataset,
		Table:          defaultTable,
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

	if strings.TrimSpace(cfg.Table) == "" {
		errs = append(errs, errors.New("table is required"))
	} else if !tableIdentifierPattern.MatchString(cfg.Table) {
		errs = append(errs, errors.New("table must be a simple identifier like public.vendor_otel_raw"))
	}

	if strings.TrimSpace(cfg.SchemaVersion) == "" {
		errs = append(errs, errors.New("schema_version is required"))
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Compression)) {
	case "none", "gzip":
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

func (cfg *Config) compressionEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Compression), "gzip")
}
