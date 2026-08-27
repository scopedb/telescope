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

const defaultCompression = "zstd"

var typeStr = component.MustNewType("scopedb")

var tablePartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type TableRoutingConfig struct {
	Logs    string `mapstructure:"logs" yaml:"logs"`
	Traces  string `mapstructure:"traces" yaml:"traces"`
	Metrics string `mapstructure:"metrics" yaml:"metrics"`
}

type SignalMappingConfig struct {
	Logs    MappingConfig `mapstructure:"logs" yaml:"logs"`
	Traces  MappingConfig `mapstructure:"traces" yaml:"traces"`
	Metrics MappingConfig `mapstructure:"metrics" yaml:"metrics"`
}

type Config struct {
	Endpoint       string                                                   `mapstructure:"endpoint"`
	APIKey         configopaque.String                                      `mapstructure:"api_key"`
	Tables         TableRoutingConfig                                       `mapstructure:"tables"`
	Mappings       SignalMappingConfig                                      `mapstructure:"mappings"`
	Compression    string                                                   `mapstructure:"compression"`
	Timeout        exporterhelper.TimeoutConfig                             `mapstructure:",squash"`
	RetryOnFailure configretry.BackOffConfig                                `mapstructure:"retry_on_failure"`
	SendingQueue   configoptional.Optional[exporterhelper.QueueBatchConfig] `mapstructure:"sending_queue"`
}

func createDefaultConfig() component.Config {
	retryCfg := configretry.NewDefaultBackOffConfig()
	retryCfg.Enabled = true
	retryCfg.InitialInterval = 5 * time.Second
	retryCfg.MaxInterval = time.Minute
	retryCfg.MaxElapsedTime = 0

	queueCfg := exporterhelper.NewDefaultQueueConfig()
	queueCfg.Sizer = exporterhelper.RequestSizerTypeBytes
	queueCfg.QueueSize = 512 << 20
	queueCfg.NumConsumers = 1
	queueCfg.Batch = configoptional.None[exporterhelper.BatchConfig]()

	return &Config{
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

	if strings.TrimSpace(string(cfg.APIKey)) == "" {
		errs = append(errs, errors.New("api_key is required"))
	}

	if err := cfg.ingestionConfig().Validate(); err != nil {
		errs = append(errs, err)
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Compression)) {
	case "gzip", "zstd":
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

func (cfg *Config) ingestionConfig() IngestionConfig {
	return IngestionConfig{Signals: IngestionSignalsConfig{
		Logs: SignalIngestionConfig{
			Table:   cfg.Tables.Logs,
			Mapping: cfg.Mappings.Logs,
		},
		Traces: SignalIngestionConfig{
			Table:   cfg.Tables.Traces,
			Mapping: cfg.Mappings.Traces,
		},
		Metrics: SignalIngestionConfig{
			Table:   cfg.Tables.Metrics,
			Mapping: cfg.Mappings.Metrics,
		},
	}}
}

func (cfg *Config) signal(signal string) (SignalIngestionConfig, bool) {
	return cfg.ingestionConfig().Signal(signal)
}

func (cfg *Config) compressionMode() string {
	mode := strings.ToLower(strings.TrimSpace(cfg.Compression))
	if mode == "" {
		return defaultCompression
	}
	return mode
}
