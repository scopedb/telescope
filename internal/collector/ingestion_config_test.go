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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scopedb/telescope/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/otelcol"
)

func TestLoadAndRenderIngestionConfig(t *testing.T) {
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://scope.example")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "test-key")

	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
signals:
  traces:
    table: app.spans
    mapping:
      ts: span.start_time
      name: span.name
      service:
        sources:
          - resource.attributes["service.name"]
          - resource.attributes["service"]
        default: unknown
        cast: string
      sampled:
        value: false
`), 0o600))

	ingestion, err := LoadConfig(path)
	require.NoError(t, err)
	uri, err := ConfigURI(ingestion)
	require.NoError(t, err)
	settings := Settings(uri, "test")
	provider, err := otelcol.NewConfigProvider(settings.ConfigProviderSettings)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	factories, err := factories("test", scopedbexporter.NewStatusRegistry(), scopedbexporter.NewCaptureRegistry())
	require.NoError(t, err)
	resolved, err := provider.Get(context.Background(), factories)
	require.NoError(t, err)
	require.Len(t, resolved.Exporters, 1)
	for _, exporterConfig := range resolved.Exporters {
		config := exporterConfig.(*scopedbexporter.Config)
		assert.Empty(t, config.Tables.Logs)
		assert.Equal(t, "app.spans", config.Tables.Traces)
		assert.Empty(t, config.Tables.Metrics)
		assert.Equal(t, "span.name", config.Mappings.Traces["name"].Source)
		assert.Equal(t, "string", config.Mappings.Traces["service"].Cast)
		assert.Equal(t, "unknown", config.Mappings.Traces["service"].Default)
		assert.Equal(t, false, config.Mappings.Traces["sampled"].Value)
		assert.Zero(t, config.RetryOnFailure.MaxElapsedTime)
		assert.Equal(t, "bytes", config.SendingQueue.Get().Sizer.String())
	}
}

func TestLoadConfigRejectsUnknownAndIncompleteFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte("unknown: true\n"), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "field unknown not found")
}

func TestRuntimeConfigurationPrecedenceAndContractDigest(t *testing.T) {
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://override.example")
	t.Setenv("TELESCOPE_WRITE_TIMEOUT", "7s")
	t.Setenv("TELESCOPE_QUEUE_MAX_BYTES", "")
	t.Setenv("TEST_RUNTIME_KEY", "key:${literal}")
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
destination:
  endpoint: https://config.example
  api_key: ${env:TEST_RUNTIME_KEY}
delivery:
  timeout: 3s
  unknown_retry: {enabled: true, max_retries: 2, max_elapsed_time: 10s}
  queue:
    type: memory
    max_bytes: 65536
  retry:
    max_elapsed_time: 5m
resources:
  memory_limit_mib: 1500
telemetry:
  otlp:
    endpoint: https://gateway.example/v1/metrics
    interval: 15s
signals:
  logs:
    table: logs
    mapping:
      value: log.message
`), 0o600))
	config, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "https://override.example", config.Destination.Endpoint)
	require.Equal(t, "key:${literal}", config.Destination.APIKey)
	require.Equal(t, 7*time.Second, config.Delivery.Timeout)
	require.Equal(t, int64(65536), config.Delivery.Queue.MaxBytes)
	require.Equal(t, 5*time.Minute, config.Delivery.Retry.MaxElapsedTime)
	require.Equal(t, 1500, config.Resources.MemoryLimitMiB)
	config.Signals.Logs.Mapping["literal"] = scopedbexporter.MappingRule{Value: "${env:UNRESOLVED_MAPPING_LITERAL}"}
	uri, err := ConfigURI(config)
	require.NoError(t, err)
	settings := newSettings(uri, "test", true, scopedbexporter.NewStatusRegistry(), scopedbexporter.NewCaptureRegistry())
	provider, err := otelcol.NewConfigProvider(settings.ConfigProviderSettings)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	components, err := factories("test", scopedbexporter.NewStatusRegistry(), scopedbexporter.NewCaptureRegistry())
	require.NoError(t, err)
	resolved, err := provider.Get(t.Context(), components)
	require.NoError(t, err)
	for _, value := range resolved.Exporters {
		exporter := value.(*scopedbexporter.Config)
		require.Equal(t, "key:${literal}", string(exporter.APIKey))
		require.Equal(t, "${env:UNRESOLVED_MAPPING_LITERAL}", exporter.Mappings.Logs["literal"].Value)
		require.Nil(t, exporter.SendingQueue.Get().StorageID)
		require.True(t, exporter.UnknownRetry.Enabled)
		require.Equal(t, 2, exporter.UnknownRetry.MaxRetries)
		require.Equal(t, 10*time.Second, exporter.UnknownRetry.MaxElapsedTime)
	}

	first, err := config.ContractDigest()
	require.NoError(t, err)
	config.Delivery.Queue.Type = "file"
	second, err := config.ContractDigest()
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestRuntimeConfigurationRejectsInvalidPolicy(t *testing.T) {
	for _, policy := range []string{
		"delivery: {queue: {type: shared}}",
		"delivery: {queue: {max_bytes: 0}}",
		"delivery: {timeout: -1s}",
		"delivery: {unknown_retry: {enabled: true, max_retries: 0}}",
		"delivery: {unknown_retry: {enabled: true, max_elapsed_time: 0s}}",
		"delivery: {retry: {initial_interval: 5s, max_interval: 1s}}",
		"batch: {size: 10, max_size: 1}",
		"resources: {memory_limit_mib: 128, memory_spike_mib: 128}",
		"telemetry: {otlp: {endpoint: file:///tmp/metrics}}",
		"telemetry: {otlp: {interval: 0s}}",
		"telemetry: {otlp: {protocol: udp}}",
		"telemetry: {otlp: {unknown_option: true}}",
		"telemetry: {otlp: {compression: zstd}}",
		"telemetry: {level: none}",
	} {
		t.Run(policy, func(t *testing.T) {
			for _, name := range []string{"TELESCOPE_QUEUE_MAX_BYTES", "TELESCOPE_WRITE_TIMEOUT", "TELESCOPE_OTEL_BATCH_SIZE", "TELESCOPE_OTEL_BATCH_MAX_SIZE"} {
				t.Setenv(name, "")
			}
			path := filepath.Join(t.TempDir(), "telescope.yaml")
			require.NoError(t, os.WriteFile(path, []byte(policy+"\nsignals:\n  logs:\n    table: logs\n    mapping:\n      value: log.message\n"), 0o600))
			_, err := LoadConfig(path)
			require.Error(t, err)
		})
	}
}

func TestMemoryQueueBudgetAcrossSignals(t *testing.T) {
	t.Setenv("TELESCOPE_QUEUE_MAX_BYTES", "")
	load := func(policy, signals string) (Config, error) {
		path := filepath.Join(t.TempDir(), "telescope.yaml")
		require.NoError(t, os.WriteFile(path, []byte(policy+"\nsignals:\n"+signals), 0o600))
		return LoadConfig(path)
	}
	one := "  logs: {table: logs, mapping: {message: log.message}}\n"
	three := one + "  traces: {table: traces, mapping: {name: span.name}}\n  metrics: {table: metrics, mapping: {name: metric.name}}\n"
	policy := "delivery: {queue: {type: memory}}"
	single, err := load(policy, one)
	require.NoError(t, err)
	multi, err := load(policy, three)
	require.NoError(t, err)
	require.Less(t, multi.Delivery.Queue.MaxBytes, single.Delivery.Queue.MaxBytes)
	require.Equal(t, single.Delivery.Queue.MaxBytes, 3*multi.Delivery.Queue.MaxBytes)
	require.Less(t, single.Delivery.Queue.MaxBytes, single.memorySoftLimitBytes())
	require.Empty(t, multi.Warnings())
	_, err = load("delivery: {queue: {type: memory, max_bytes: 134217728}}", three)
	require.ErrorContains(t, err, "combined memory queue capacity")
	_, err = load("delivery: {queue: {type: file, max_bytes: 134217728}}", three)
	require.NoError(t, err)
	explicit, err := load("delivery: {queue: {type: memory, max_bytes: 67108864}}", three)
	require.NoError(t, err)
	require.Len(t, explicit.Warnings(), 1)
	t.Setenv("TELESCOPE_QUEUE_MAX_BYTES", "134217728")
	_, err = load(policy, three)
	require.ErrorContains(t, err, "combined memory queue capacity")
}
