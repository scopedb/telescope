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
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/scopedb/telescope/scopedbexporter"
)

// Config keeps ingestion mappings and runtime policy in one validated document.
// Only the ingestion contract contributes to the mapping digest.
type Config struct {
	scopedbexporter.IngestionConfig `yaml:",inline"`
	Destination                     DestinationConfig `yaml:"destination"`
	Receivers                       ReceiverConfig    `yaml:"receivers"`
	HTTPAddr                        string            `yaml:"http_addr"`
	Delivery                        DeliveryConfig    `yaml:"delivery"`
	Batch                           BatchConfig       `yaml:"batch"`
	Resources                       ResourceConfig    `yaml:"resources"`
	Telemetry                       TelemetryConfig   `yaml:"telemetry"`
}

type DestinationConfig struct {
	Endpoint string `yaml:"endpoint"`
	APIKey   string `yaml:"api_key"`
}

type ReceiverConfig struct {
	GRPCAddr string `yaml:"grpc_addr"`
	HTTPAddr string `yaml:"http_addr"`
}

type DeliveryConfig struct {
	Timeout  time.Duration `yaml:"timeout"`
	MaxRows  int           `yaml:"max_rows"`
	MaxBytes int           `yaml:"max_bytes"`
	Queue    QueueConfig   `yaml:"queue"`
	Retry    RetryConfig   `yaml:"retry"`
}

type QueueConfig struct {
	// A file queue has one owner. A memory queue is lost on process exit.
	Type      string `yaml:"type"`
	Directory string `yaml:"directory"`
	MaxBytes  int64  `yaml:"max_bytes"`
	Consumers int    `yaml:"consumers"`
}

type RetryConfig struct {
	InitialInterval time.Duration `yaml:"initial_interval"`
	MaxInterval     time.Duration `yaml:"max_interval"`
	MaxElapsedTime  time.Duration `yaml:"max_elapsed_time"`
}

type BatchConfig struct {
	Size    int           `yaml:"size"`
	MaxSize int           `yaml:"max_size"`
	Timeout time.Duration `yaml:"timeout"`
}

type ResourceConfig struct {
	MemoryLimitMiB int `yaml:"memory_limit_mib"`
	MemorySpikeMiB int `yaml:"memory_spike_mib"`
}

type TelemetryConfig struct {
	// The local reader also supplies operational status and readiness.
	MetricsAddr string            `yaml:"metrics_addr"`
	OTLP        OTLPMetricsConfig `yaml:"otlp"`
}

type OTLPMetricsConfig struct {
	Endpoint string            `yaml:"endpoint"`
	Protocol string            `yaml:"protocol"`
	Headers  map[string]string `yaml:"headers"`
	Interval time.Duration     `yaml:"interval"`
	Timeout  time.Duration     `yaml:"timeout"`
}

func defaultConfig() Config {
	return Config{
		Receivers: ReceiverConfig{GRPCAddr: "0.0.0.0:4317", HTTPAddr: "0.0.0.0:4318"},
		HTTPAddr:  "127.0.0.1:8080",
		Delivery: DeliveryConfig{
			Timeout: 30 * time.Second, MaxRows: 8000, MaxBytes: 4 << 20,
			Queue: QueueConfig{Type: "file", Directory: defaultQueueDir(), MaxBytes: 512 << 20, Consumers: 1},
			Retry: RetryConfig{InitialInterval: 5 * time.Second, MaxInterval: time.Minute},
		},
		Batch:     BatchConfig{Size: 2000, MaxSize: 2000, Timeout: 30 * time.Second},
		Resources: ResourceConfig{MemoryLimitMiB: 512, MemorySpikeMiB: 128},
		Telemetry: TelemetryConfig{MetricsAddr: "127.0.0.1:8888", OTLP: OTLPMetricsConfig{
			Protocol: "http/protobuf", Interval: time.Minute, Timeout: 10 * time.Second,
		}},
	}
}

func (c Config) Validate() error {
	if err := c.IngestionConfig.Validate(); err != nil {
		return err
	}
	for name, address := range map[string]string{
		"http_addr": c.HTTPAddr, "receivers.grpc_addr": c.Receivers.GRPCAddr,
		"receivers.http_addr": c.Receivers.HTTPAddr, "telemetry.metrics_addr": c.Telemetry.MetricsAddr,
	} {
		_, port, err := net.SplitHostPort(address)
		number, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || number < 1 || number > 65535 {
			return fmt.Errorf("%s must be a host:port address with port 1..65535", name)
		}
	}
	for name, endpoint := range map[string]string{"destination.endpoint": c.Destination.Endpoint, "telemetry.otlp.endpoint": c.Telemetry.OTLP.Endpoint} {
		if endpoint == "" {
			continue
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%s must be an HTTP(S) URL without credentials, query, or fragment", name)
		}
	}
	if c.Delivery.Timeout < 0 {
		return errors.New("delivery.timeout must not be negative")
	}
	if c.Delivery.MaxRows < 1 || c.Delivery.MaxRows > 200000 || c.Delivery.MaxBytes < 1 || c.Delivery.MaxBytes > 8<<20 {
		return errors.New("delivery requires max_rows in 1..200000 and max_bytes in 1..8388608")
	}
	q := c.Delivery.Queue
	if q.Type != "file" && q.Type != "memory" {
		return errors.New("delivery.queue.type must be file or memory")
	}
	if q.Type == "file" && strings.TrimSpace(q.Directory) == "" {
		return errors.New("delivery.queue.directory is required for file queues")
	}
	if q.MaxBytes <= 0 || q.Consumers <= 0 {
		return errors.New("delivery.queue.max_bytes and consumers must be positive")
	}
	r := c.Delivery.Retry
	if r.InitialInterval <= 0 || r.MaxInterval < r.InitialInterval || r.MaxElapsedTime < 0 {
		return errors.New("delivery.retry requires positive initial_interval, max_interval >= initial_interval, and nonnegative max_elapsed_time")
	}
	if c.Batch.Size < 1 || c.Batch.MaxSize < c.Batch.Size || c.Batch.Timeout <= 0 {
		return errors.New("batch requires positive size and timeout, and max_size >= size")
	}
	if c.Resources.MemoryLimitMiB <= 0 || c.Resources.MemorySpikeMiB < 0 || c.Resources.MemorySpikeMiB >= c.Resources.MemoryLimitMiB {
		return errors.New("resources requires memory_limit_mib > memory_spike_mib >= 0")
	}
	m := c.Telemetry.OTLP
	if m.Protocol != "http/protobuf" && m.Protocol != "grpc" {
		return errors.New("telemetry.otlp.protocol must be http/protobuf or grpc")
	}
	if m.Interval < time.Millisecond || m.Timeout < time.Millisecond {
		return errors.New("telemetry.otlp.interval and timeout must be at least 1ms")
	}
	return nil
}

// Environment overrides retain the existing CLI deployment interface. Values
// are parsed as scalars, never interpolated into YAML or ingestion mappings.
func (c *Config) applyEnvironment() error {
	for key, target := range map[string]any{
		"TELESCOPE_SCOPEDB_ENDPOINT": &c.Destination.Endpoint, "TELESCOPE_SCOPEDB_API_KEY": &c.Destination.APIKey,
		"TELESCOPE_OTLP_GRPC_ADDR": &c.Receivers.GRPCAddr, "TELESCOPE_OTLP_HTTP_ADDR": &c.Receivers.HTTPAddr,
		"TELESCOPE_HTTP_ADDR": &c.HTTPAddr,
		"TELESCOPE_QUEUE_DIR": &c.Delivery.Queue.Directory, "TELESCOPE_QUEUE_MAX_BYTES": &c.Delivery.Queue.MaxBytes,
		"TELESCOPE_QUEUE_CONSUMERS": &c.Delivery.Queue.Consumers, "TELESCOPE_WRITE_TIMEOUT": &c.Delivery.Timeout,
		"TELESCOPE_APPEND_MAX_ROWS": &c.Delivery.MaxRows, "TELESCOPE_APPEND_MAX_BYTES": &c.Delivery.MaxBytes,
		"TELESCOPE_OTEL_BATCH_TIMEOUT": &c.Batch.Timeout, "TELESCOPE_OTEL_BATCH_SIZE": &c.Batch.Size,
		"TELESCOPE_OTEL_BATCH_MAX_SIZE": &c.Batch.MaxSize,
	} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		var err error
		switch target := target.(type) {
		case *string:
			*target = value
		case *int:
			*target, err = strconv.Atoi(value)
		case *int64:
			*target, err = strconv.ParseInt(value, 10, 64)
		case *time.Duration:
			*target, err = time.ParseDuration(value)
		}
		if err != nil {
			return fmt.Errorf("invalid value for %s", key)
		}
	}
	for _, target := range []*string{&c.Destination.Endpoint, &c.Destination.APIKey, &c.Delivery.Queue.Directory, &c.Telemetry.OTLP.Endpoint} {
		value, err := resolveEnvironment(*target)
		if err != nil {
			return err
		}
		*target = value
	}
	for name, value := range c.Telemetry.OTLP.Headers {
		resolved, err := resolveEnvironment(value)
		if err != nil {
			return err
		}
		c.Telemetry.OTLP.Headers[name] = resolved
	}
	return nil
}

var environmentReference = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

func resolveEnvironment(value string) (string, error) {
	var err error
	result := environmentReference.ReplaceAllStringFunc(value, func(reference string) string {
		name := environmentReference.FindStringSubmatch(reference)[1]
		replacement, ok := os.LookupEnv(name)
		if !ok {
			err = fmt.Errorf("environment variable %s is not set", name)
		}
		return replacement
	})
	return result, err
}
