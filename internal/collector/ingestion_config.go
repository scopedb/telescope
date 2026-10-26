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
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/scopedb/telescope/scopedbexporter"
	"go.yaml.in/yaml/v3"
)

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read Telescope config %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	config := defaultConfig()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode Telescope config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("decode Telescope config %s: multiple YAML documents are not supported", path)
		}
		return Config{}, fmt.Errorf("decode Telescope config %s: %w", path, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return Config{}, err
	}
	config.Sources = make(map[string]string)
	walkRuntimeSettings(&document, "", func(path, _ string) { config.Sources[path] = "YAML" })
	if err := config.applyEnvironment(); err != nil {
		return Config{}, err
	}
	if config.Delivery.Queue.Type == "memory" && config.Sources["delivery.queue.max_bytes"] == "" && len(config.EnabledSignals()) > 0 {
		config.Delivery.Queue.MaxBytes = config.memorySoftLimitBytes() / 4 / int64(len(config.EnabledSignals()))
		config.Sources["delivery.queue.max_bytes"] = "derived from memory soft limit and enabled signals"
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate Telescope config %s: %w", path, err)
	}
	return config, nil
}

func ConfigURI(config Config) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}
	tables := make(map[string]string, 3)
	mappings := make(map[string]scopedbexporter.MappingConfig, 3)
	pipelines := make(map[string]any, 3)
	for _, signal := range config.EnabledSignals() {
		signalConfig, _ := config.Signal(signal)
		tables[signal] = signalConfig.Table
		mappings[signal] = signalConfig.Mapping
		pipelines[signal] = map[string]any{"receivers": []string{"otlp"}, "processors": []string{"memory_limiter", "batch"}, "exporters": []string{"scopedb"}}
	}
	q := config.Delivery.Queue
	queue := map[string]any{"enabled": true, "sizer": "bytes", "queue_size": q.MaxBytes, "num_consumers": q.Consumers}
	extensions := map[string]any{}
	enabledExtensions := []string{}
	if q.Type == "file" {
		queue["storage"] = "file_storage"
		extensions["file_storage"] = map[string]any{"directory": q.Directory, "create_directory": true, "fsync": true}
		enabledExtensions = append(enabledExtensions, "file_storage")
	}
	host, port, _ := net.SplitHostPort(config.Telemetry.MetricsAddr)
	portNumber, _ := strconv.Atoi(port)
	readers := []any{map[string]any{"pull": map[string]any{"exporter": map[string]any{"prometheus": map[string]any{"host": host, "port": portNumber}}}}}
	m := config.Telemetry.OTLP
	if m.Endpoint != "" {
		headers := []map[string]string{}
		names := make([]string, 0, len(m.Headers))
		for name := range m.Headers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			headers = append(headers, map[string]string{"name": name, "value": m.Headers[name]})
		}
		readers = append(readers, map[string]any{"periodic": map[string]any{
			"interval": m.Interval.Milliseconds(), "timeout": m.Timeout.Milliseconds(),
			"exporter": map[string]any{"otlp": map[string]any{"endpoint": m.Endpoint, "protocol": m.Protocol, "compression": m.Compression, "headers": headers, "timeout": m.Timeout.Milliseconds()}},
		}})
	}
	r := config.Delivery.Retry
	rendered := map[string]any{
		"extensions": extensions,
		"receivers": map[string]any{"otlp": map[string]any{"protocols": map[string]any{
			"grpc": map[string]any{"endpoint": config.Receivers.GRPCAddr}, "http": map[string]any{"endpoint": config.Receivers.HTTPAddr},
		}}},
		"processors": map[string]any{
			"memory_limiter": map[string]any{"check_interval": "1s", "limit_mib": config.Resources.MemoryLimitMiB, "spike_limit_mib": config.Resources.MemorySpikeMiB},
			"batch":          map[string]any{"timeout": config.Batch.Timeout.String(), "send_batch_size": config.Batch.Size, "send_batch_max_size": config.Batch.MaxSize},
		},
		"exporters": map[string]any{"scopedb": map[string]any{
			"endpoint": config.Destination.Endpoint, "api_key": config.Destination.APIKey, "compression": "zstd", "timeout": config.Delivery.Timeout.String(),
			"append_batch":     map[string]any{"max_rows": config.Delivery.MaxRows, "max_bytes": config.Delivery.MaxBytes},
			"sending_queue":    queue,
			"retry_on_failure": map[string]any{"enabled": true, "initial_interval": r.InitialInterval.String(), "max_interval": r.MaxInterval.String(), "max_elapsed_time": r.MaxElapsedTime.String()},
			"unknown_retry":    map[string]any{"enabled": config.Delivery.UnknownRetry.Enabled, "max_retries": config.Delivery.UnknownRetry.MaxRetries, "max_elapsed_time": config.Delivery.UnknownRetry.MaxElapsedTime.String()},
			"tables":           tables, "mappings": mappings,
		}},
		"service": map[string]any{
			"extensions": enabledExtensions, "pipelines": pipelines,
			"telemetry": map[string]any{"resource": config.Telemetry.Resource, "logs": map[string]any{"level": config.Telemetry.LogLevel}, "metrics": map[string]any{"level": config.Telemetry.Level, "readers": readers}},
		},
	}
	data, err := yaml.Marshal(rendered)
	if err != nil {
		return "", fmt.Errorf("encode collector config: %w", err)
	}
	// Values have already been resolved. Escape dollars so Collector's resolver
	// cannot reinterpret credentials or literal mapping values as providers.
	return "yaml:" + strings.ReplaceAll(strings.TrimSpace(string(data)), "$", "$$") + "\n", nil
}
