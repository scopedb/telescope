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
	"go.opentelemetry.io/collector/confmap"
	"go.yaml.in/yaml/v3"
)

func shorthandMapping(values map[string]string) MappingConfig {
	mapping := make(MappingConfig, len(values))
	for column, source := range values {
		mapping[column] = MappingRule{Source: source}
	}
	return mapping
}

func TestMappingRuleDecodesShorthandAndExpandedYAML(t *testing.T) {
	var config struct {
		Mapping MappingConfig `yaml:"mapping"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(`
mapping:
  message: log.message
  service:
    sources:
      - resource.attributes["service.name"]
      - resource.attributes["service"]
    default: unknown
    cast: string
  attempt:
    source: log.body["attempt"]
    cast: int
  sampled:
    value: false
  released_at:
    value: 2026-08-27T12:34:56+08:00
    cast: timestamp
`), &config))

	assert.Equal(t, "log.message", config.Mapping["message"].Source)
	assert.Equal(t, `log.body["attempt"]`, config.Mapping["attempt"].Source)
	assert.Equal(t, "int", config.Mapping["attempt"].Cast)
	assert.Equal(t, []string{
		`resource.attributes["service.name"]`,
		`resource.attributes["service"]`,
	}, config.Mapping["service"].Sources)
	assert.Equal(t, "unknown", config.Mapping["service"].Default)
	assert.True(t, config.Mapping["service"].hasDefault())
	assert.Equal(t, "string", config.Mapping["service"].Cast)
	assert.Equal(t, false, config.Mapping["sampled"].Value)
	assert.True(t, config.Mapping["sampled"].hasValue())
	compiled, err := compileMappingRule(signalLogs, config.Mapping["released_at"])
	require.NoError(t, err)
	evaluation, err := compiled.evaluate(nil)
	require.NoError(t, err)
	assert.True(t, evaluation.present)
	assert.Equal(t, "2026-08-27T04:34:56Z", evaluation.value)
}

func TestMappingRuleDecodesCollectorConfig(t *testing.T) {
	var config struct {
		Mapping MappingConfig `mapstructure:"mapping"`
	}
	conf := confmap.NewFromStringMap(map[string]any{
		"mapping": map[string]any{
			"message": "log.message",
			"service": map[string]any{
				"sources": []any{
					`resource.attributes["service.name"]`,
					`resource.attributes["service"]`,
				},
				"default": "unknown",
				"cast":    "string",
			},
		},
	})

	require.NoError(t, conf.Unmarshal(&config))
	assert.Equal(t, "log.message", config.Mapping["message"].Source)
	assert.Equal(t, "unknown", config.Mapping["service"].Default)
	assert.True(t, config.Mapping["service"].hasDefault())
}

func TestMappingRuleYAMLRoundTripPreservesFalsyValues(t *testing.T) {
	var config struct {
		Mapping MappingConfig `yaml:"mapping"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(`
mapping:
  enabled:
    source: log.attributes["enabled"]
    default: false
  retries:
    source: log.attributes["retries"]
    default: 0
  tenant:
    source: log.attributes["tenant"]
    default: ""
  sampled:
    value: false
`), &config))

	encoded, err := yaml.Marshal(config)
	require.NoError(t, err)
	var roundTrip struct {
		Mapping MappingConfig `yaml:"mapping"`
	}
	require.NoError(t, yaml.Unmarshal(encoded, &roundTrip))

	assert.Equal(t, false, roundTrip.Mapping["enabled"].Default)
	assert.True(t, roundTrip.Mapping["enabled"].hasDefault())
	assert.Equal(t, 0, roundTrip.Mapping["retries"].Default)
	assert.True(t, roundTrip.Mapping["retries"].hasDefault())
	assert.Equal(t, "", roundTrip.Mapping["tenant"].Default)
	assert.True(t, roundTrip.Mapping["tenant"].hasDefault())
	assert.Equal(t, false, roundTrip.Mapping["sampled"].Value)
	assert.True(t, roundTrip.Mapping["sampled"].hasValue())
}

func TestMappingRuleRejectsAmbiguousAndUnknownConfiguration(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "unknown field",
			yaml: "mapping:\n  value:\n    transform: log.body\n",
			want: `unknown mapping rule field "transform"`,
		},
		{
			name: "source and sources",
			yaml: "mapping:\n  value:\n    source: log.body\n    sources: [log.message]\n",
			want: "source and sources cannot be used together",
		},
		{
			name: "unsupported cast",
			yaml: "mapping:\n  value:\n    source: log.body\n    cast: json\n",
			want: "unsupported cast",
		},
		{
			name: "empty source",
			yaml: "mapping:\n  value:\n    source: \"\"\n",
			want: "source is empty",
		},
		{
			name: "empty sources",
			yaml: "mapping:\n  value:\n    sources: []\n",
			want: "sources must contain at least one selector",
		},
		{
			name: "null default",
			yaml: "mapping:\n  value:\n    source: log.body\n    default: null\n",
			want: "default cannot be null",
		},
		{
			name: "null value",
			yaml: "mapping:\n  value:\n    value: null\n",
			want: "value cannot be null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config struct {
				Mapping MappingConfig `yaml:"mapping"`
			}
			err := yaml.Unmarshal([]byte(tt.yaml), &config)
			if err == nil {
				_, err = config.Mapping["value"].normalized()
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestCastMappingValue(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		target selectorType
		want   any
	}{
		{name: "string from object", value: map[string]any{"b": 2, "a": 1}, target: selectorTypeString, want: `{"a":1,"b":2}`},
		{name: "int from string", value: "42", target: selectorTypeInt, want: int64(42)},
		{name: "uint from int", value: int64(42), target: selectorTypeUInt, want: uint64(42)},
		{name: "float from string", value: "1.25", target: selectorTypeFloat, want: 1.25},
		{name: "boolean from string", value: "true", target: selectorTypeBoolean, want: true},
		{name: "timestamp from nanos", value: "1713835425123456789", target: selectorTypeTimestamp, want: "2024-04-23T01:23:45.123456789Z"},
		{name: "object assertion", value: map[string]any{"request_id": "42"}, target: selectorTypeObject, want: map[string]any{"request_id": "42"}},
		{name: "array assertion", value: []any{"one", "two"}, target: selectorTypeArray, want: []any{"one", "two"}},
		{name: "explicit any", value: map[string]any{"request_id": "42"}, target: selectorTypeAny, want: map[string]any{"request_id": "42"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := castMappingValue(tt.value, tt.target)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCastMappingValueRejectsLossyAndInvalidValues(t *testing.T) {
	_, err := castMappingValue(1.5, selectorTypeInt)
	assert.Error(t, err)
	_, err = castMappingValue(-1, selectorTypeUInt)
	assert.Error(t, err)
	_, err = castMappingValue("not-a-bool", selectorTypeBoolean)
	assert.Error(t, err)
	_, err = castMappingValue("not-a-time", selectorTypeTimestamp)
	assert.Error(t, err)
	_, err = castMappingValue("not-an-object", selectorTypeObject)
	assert.Error(t, err)
	_, err = castMappingValue(map[string]any{"not": "an array"}, selectorTypeArray)
	assert.Error(t, err)
}

func TestCompileMappingRuleRejectsInvalidKnownCast(t *testing.T) {
	_, err := compileMappingRule(signalLogs, MappingRule{Value: "second", Cast: "int"})
	require.Error(t, err)
	assert.ErrorContains(t, err, `value: string value "second" cannot be converted to int`)

	_, err = compileMappingRule(signalLogs, MappingRule{
		Source:  `log.attributes["attempt"]`,
		Default: "second",
		Cast:    "int",
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, `default: string value "second" cannot be converted to int`)
}

func TestCompileMappingRuleResolvesKnownConstantCast(t *testing.T) {
	compiled, err := compileMappingRule(signalLogs, MappingRule{Value: "42", Cast: "int"})
	require.NoError(t, err)
	assert.Equal(t, selectorTypeInt, compiled.valueType)
	assert.False(t, compiled.runtimeDependent)
	evaluation, err := compiled.evaluate(nil)
	require.NoError(t, err)
	assert.True(t, evaluation.present)
	assert.Equal(t, int64(42), evaluation.value)

	compiled, err = compileMappingRule(signalLogs, MappingRule{
		Source:  `log.attributes["attempt"]`,
		Default: "3",
		Cast:    "int",
	})
	require.NoError(t, err)
	evaluation, err = compiled.evaluate(Record{})
	require.NoError(t, err)
	assert.True(t, evaluation.present)
	assert.Equal(t, int64(3), evaluation.value)
}

func TestMappingCastRuntimeDependency(t *testing.T) {
	assert.False(t, mappingCastNeedsRuntimeCheck(selectorTypeInt, selectorTypeString))
	assert.False(t, mappingCastNeedsRuntimeCheck(selectorTypeTimestamp, selectorTypeString))
	assert.True(t, mappingCastNeedsRuntimeCheck(selectorTypeString, selectorTypeInt))
	assert.True(t, mappingCastNeedsRuntimeCheck(selectorTypeDynamic, selectorTypeString))
	assert.True(t, mappingCastNeedsRuntimeCheck(selectorTypeFloat, selectorTypeFloat))
}

func TestCompileMappingRuleDeclaresDynamicStructuredType(t *testing.T) {
	compiled, err := compileMappingRule(signalLogs, MappingRule{Source: "log.body", Cast: "object"})
	require.NoError(t, err)
	assert.Equal(t, selectorTypeObject, compiled.valueType)
	assert.True(t, compiled.runtimeDependent)

	evaluation, err := compiled.evaluate(Record{"body": map[string]any{"request_id": "42"}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"request_id": "42"}, evaluation.value)

	_, err = compiled.evaluate(Record{"body": "not-an-object"})
	require.Error(t, err)
}
