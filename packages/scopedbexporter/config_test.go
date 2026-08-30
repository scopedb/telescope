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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/confmap"
)

func TestConfigValidate(t *testing.T) {
	cfg := validTestConfig()
	require.NoError(t, cfg.Validate())
}

func TestConfigValidateRequiredConnectionFields(t *testing.T) {
	cfg := validTestConfig()
	cfg.Endpoint = ""
	cfg.APIKey = ""

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "endpoint is required")
	assert.ErrorContains(t, err, "api_key is required")
}

func TestConfigValidateMapping(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "empty mapping",
			mutate: func(cfg *Config) {
				cfg.Mappings.Logs = nil
			},
			want: "at least one destination column is required",
		},
		{
			name: "invalid destination column",
			mutate: func(cfg *Config) {
				cfg.Mappings.Logs = shorthandMapping(map[string]string{"bad-column": "log.body"})
			},
			want: "must be an unquoted ScopeDB identifier",
		},
		{
			name: "invalid source",
			mutate: func(cfg *Config) {
				cfg.Mappings.Traces = shorthandMapping(map[string]string{"trace_id": "span.unknown"})
			},
			want: "unsupported traces source",
		},
		{
			name: "wrong signal source",
			mutate: func(cfg *Config) {
				cfg.Mappings.Logs = shorthandMapping(map[string]string{"value": `span.attributes["order.id"]`})
			},
			want: "only valid for traces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validTestConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestConfigAllowsSignalsToShareTable(t *testing.T) {
	cfg := validTestConfig()
	cfg.Tables.Logs = "otel.events"
	cfg.Tables.Traces = "otel.events"

	require.NoError(t, cfg.Validate())
}

func TestConfigAllowsOneSignal(t *testing.T) {
	cfg := validTestConfig()
	cfg.Tables.Logs = ""
	cfg.Mappings.Logs = nil
	cfg.Tables.Metrics = ""
	cfg.Mappings.Metrics = nil

	require.NoError(t, cfg.Validate())
}

func TestCreateDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Empty(t, cfg.Tables)
	assert.Empty(t, cfg.Mappings)
	assert.Equal(t, defaultCompression, cfg.Compression)
	assert.True(t, cfg.RetryOnFailure.Enabled)
	assert.Zero(t, cfg.RetryOnFailure.MaxElapsedTime)
	assert.True(t, cfg.SendingQueue.HasValue())
	assert.Equal(t, "bytes", cfg.SendingQueue.Get().Sizer.String())
	assert.Equal(t, int64(512<<20), cfg.SendingQueue.Get().QueueSize)
	assert.Equal(t, 1, cfg.SendingQueue.Get().NumConsumers)
}

func TestConfigUnmarshalSetsOnlySpecifiedMappings(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	conf := confmap.NewFromStringMap(map[string]any{
		"mappings": map[string]any{
			"logs": map[string]any{
				"event": "log.body",
			},
		},
	})

	require.NoError(t, conf.Unmarshal(cfg))
	assert.Equal(t, "log.body", cfg.Mappings.Logs["event"].Source)
	assert.Empty(t, cfg.Mappings.Traces)
	assert.Empty(t, cfg.Mappings.Metrics)
}

func TestConfigUnmarshalExpandedMappingRule(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	conf := confmap.NewFromStringMap(map[string]any{
		"mappings": map[string]any{
			"logs": map[string]any{
				"service": map[string]any{
					"sources": []any{
						`resource.attributes["service.name"]`,
						`resource.attributes["service"]`,
					},
					"default": "unknown",
					"cast":    "string",
				},
			},
		},
	})

	require.NoError(t, conf.Unmarshal(cfg))
	rule := cfg.Mappings.Logs["service"]
	assert.Equal(t, []string{
		`resource.attributes["service.name"]`,
		`resource.attributes["service"]`,
	}, rule.Sources)
	assert.Equal(t, "unknown", rule.Default)
	assert.True(t, rule.hasDefault())
	assert.Equal(t, "string", rule.Cast)
}

func TestConfigUnmarshalPreservesExplicitEmptyMapping(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	conf := confmap.NewFromStringMap(map[string]any{
		"mappings": map[string]any{
			"logs": map[string]any{},
		},
	})

	require.NoError(t, conf.Unmarshal(cfg))
	assert.Empty(t, cfg.Mappings.Logs)
}

func validTestConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "https://scopedb.invalid"
	cfg.APIKey = configopaque.String("test-api-key")
	cfg.Tables = TableRoutingConfig{
		Logs:    "test.logs",
		Traces:  "test.traces",
		Metrics: "test.metrics",
	}
	cfg.Mappings = SignalMappingConfig{
		Logs:    shorthandMapping(map[string]string{"message": "log.message"}),
		Traces:  shorthandMapping(map[string]string{"name": "span.name"}),
		Metrics: shorthandMapping(map[string]string{"name": "metric.name"}),
	}
	return cfg
}
