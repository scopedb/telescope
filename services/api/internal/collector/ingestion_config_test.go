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
	path := filepath.Join(t.TempDir(), "ingestion.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
tables:
  logs: app.logs
  traces: app.spans
  metrics: app.metrics
mappings:
  logs:
    ts: log.timestamp
    message: log.message
  traces:
    ts: span.start_time
    name: span.name
  metrics:
    ts: datapoint.timestamp
    name: metric.name
    int_value: datapoint.int_value
    double_value: datapoint.double_value
`), 0o600))

	ingestion, err := LoadIngestionConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "app.logs", ingestion.Tables.Logs)
	assert.Equal(t, "log.message", ingestion.Mappings.Logs["message"])

	uri, err := ConfigURIForIngestion(ingestion)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(uri, "yaml:"))
	var rendered map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(strings.TrimPrefix(uri, "yaml:")), &rendered))
	exporters := rendered["exporters"].(map[string]any)
	scopeDB := exporters["scopedb"].(map[string]any)
	tables := scopeDB["tables"].(map[string]any)
	assert.Equal(t, "app.logs", tables["logs"])
	assert.Equal(t, "zstd", scopeDB["compression"])

	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://scope.example")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "test-key")
	settings := Settings(uri, "test")
	provider, err := otelcol.NewConfigProvider(settings.ConfigProviderSettings)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	factories, err := Factories()
	require.NoError(t, err)
	resolved, err := provider.Get(context.Background(), factories)
	require.NoError(t, err)
	require.Len(t, resolved.Exporters, 1)
	for _, exporterConfig := range resolved.Exporters {
		config := exporterConfig.(*scopedbexporter.Config)
		assert.Equal(t, "app.logs", config.Tables.Logs)
		assert.Equal(t, "log.message", config.Mappings.Logs["message"])
		assert.Equal(t, "bytes", config.SendingQueue.Get().Sizer.String())
	}
}

func TestLoadIngestionConfigRejectsUnknownAndIncompleteFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingestion.yaml")
	require.NoError(t, os.WriteFile(path, []byte("unknown: true\n"), 0o600))

	_, err := LoadIngestionConfig(path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "field unknown not found")
}
