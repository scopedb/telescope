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
	"strings"
	"testing"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/otelcol"
	"go.yaml.in/yaml/v3"
)

func TestLoadAndRenderIngestionConfig(t *testing.T) {
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
	traceConfig, enabled := ingestion.Signal("traces")
	require.True(t, enabled)
	assert.Equal(t, "app.spans", traceConfig.Table)
	assert.Equal(t, "span.name", traceConfig.Mapping["name"].Source)
	assert.Equal(t, []string{
		`resource.attributes["service.name"]`,
		`resource.attributes["service"]`,
	}, traceConfig.Mapping["service"].Sources)
	assert.Equal(t, "unknown", traceConfig.Mapping["service"].Default)
	assert.Equal(t, []string{"traces"}, ingestion.EnabledSignals())

	uri, err := ConfigURI(ingestion)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(uri, "yaml:"))
	var rendered map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(strings.TrimPrefix(uri, "yaml:")), &rendered))
	exporters := rendered["exporters"].(map[string]any)
	scopeDB := exporters["scopedb"].(map[string]any)
	tables := scopeDB["tables"].(map[string]any)
	assert.Equal(t, map[string]any{"traces": "app.spans"}, tables)
	assert.Equal(t, "zstd", scopeDB["compression"])
	retry := scopeDB["retry_on_failure"].(map[string]any)
	assert.Equal(t, "0s", retry["max_elapsed_time"])
	service := rendered["service"].(map[string]any)
	telemetry := service["telemetry"].(map[string]any)
	logs := telemetry["logs"].(map[string]any)
	assert.Equal(t, "warn", logs["level"])
	pipelines := service["pipelines"].(map[string]any)
	assert.Equal(t, []string{"traces"}, mapKeys(pipelines))

	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://scope.example")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "test-key")
	settings := Settings(uri, "test")
	provider, err := otelcol.NewConfigProvider(settings.ConfigProviderSettings)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	factories, err := factories(scopedbexporter.NewStatusRegistry(), scopedbexporter.NewCaptureRegistry())
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

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestLoadConfigRejectsUnknownAndIncompleteFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte("unknown: true\n"), 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "field unknown not found")
}

func TestDeploymentConfigExamplesAreValid(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "deploy", "telescope.example.yaml"),
		filepath.Join("..", "..", "deploy", "kubernetes", "example", "telescope.yaml"),
	} {
		config, err := LoadConfig(path)
		require.NoError(t, err, path)
		assert.Equal(t, []string{"traces"}, config.EnabledSignals(), path)
	}
}
