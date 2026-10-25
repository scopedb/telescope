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
)

func TestPreviewMappingUsesProductionMappersForEverySignal(t *testing.T) {
	tests := []struct {
		signal          string
		mapping         map[string]string
		wantRecords     int
		wantColumn      string
		wantPresent     int
		wantObserved    []string
		wantProjected   any
		projectedColumn string
	}{
		{
			signal: signalLogs,
			mapping: map[string]string{
				"body":    "log.body",
				"message": "log.message",
				"service": `resource.attributes["service.name"]`,
			},
			wantRecords:     1,
			wantColumn:      "body",
			wantPresent:     1,
			wantObserved:    []string{"object"},
			projectedColumn: "message",
			wantProjected:   `{"attempt":2,"message":"order accepted"}`,
		},
		{
			signal: signalTraces,
			mapping: map[string]string{
				"name":    "span.name",
				"service": `resource.attributes["service.name"]`,
				"started": "span.start_time",
			},
			wantRecords:     1,
			wantColumn:      "started",
			wantPresent:     1,
			wantObserved:    []string{"timestamp"},
			projectedColumn: "name",
			wantProjected:   "POST /checkout",
		},
		{
			signal: signalMetrics,
			mapping: map[string]string{
				"distribution": "datapoint.distribution",
				"name":         "metric.name",
				"value":        "datapoint.value",
			},
			wantRecords:     5,
			wantColumn:      "value",
			wantPresent:     2,
			wantObserved:    []string{"float", "int"},
			projectedColumn: "name",
			wantProjected:   "system.load",
		},
	}

	for _, tt := range tests {
		t.Run(tt.signal, func(t *testing.T) {
			preview, err := PreviewMapping(tt.signal, SignalIngestionConfig{
				Table:   "analytics." + tt.signal,
				Mapping: shorthandMapping(tt.mapping),
			}, readGoldenFile(t, tt.signal+".otlp.json"))
			require.NoError(t, err)
			assert.Equal(t, tt.wantRecords, preview.Records)
			require.NotEmpty(t, preview.Rows)
			assert.Equal(t, tt.wantProjected, preview.Rows[0][tt.projectedColumn])

			column := findPreviewColumn(t, preview, tt.wantColumn)
			assert.Equal(t, tt.wantPresent, column.Present)
			assert.Equal(t, tt.wantRecords, column.Total)
			assert.Equal(t, tt.wantObserved, column.ObservedTypes)
		})
	}
}

func TestPreviewMappingAcceptsOTLPProtobufForEverySignal(t *testing.T) {
	tests := []struct {
		signal string
		source string
	}{
		{signal: signalLogs, source: "log.message"},
		{signal: signalTraces, source: "span.name"},
		{signal: signalMetrics, source: "metric.name"},
	}

	for _, tt := range tests {
		t.Run(tt.signal, func(t *testing.T) {
			sample := goldenOTLPProtobuf(t, tt.signal, readGoldenFile(t, tt.signal+".otlp.json"))
			preview, err := PreviewMapping(tt.signal, SignalIngestionConfig{
				Table:   "analytics." + tt.signal,
				Mapping: shorthandMapping(map[string]string{"value": tt.source}),
			}, sample)
			require.NoError(t, err)
			assert.NotZero(t, preview.Records)
			assert.NotEmpty(t, preview.Rows[0]["value"])
		})
	}
}

func TestObservedTypeUsesMappingValueTypes(t *testing.T) {
	assert.Equal(t, "array", observedType(selectorTypeDynamic, []uint64{1, 2}))
	assert.Equal(t, "binary", observedType(selectorTypeDynamic, []byte{1, 2}))
}

func TestPreviewMappingAppliesExpandedRules(t *testing.T) {
	preview, err := PreviewMapping(signalLogs, SignalIngestionConfig{
		Table: "analytics.logs",
		Mapping: MappingConfig{
			"attempt": {Source: `log.body["attempt"]`, Cast: "int"},
			"environment": {
				Source:  `resource.attributes["deployment.environment.name"]`,
				Default: "unknown",
			},
			"message": {Sources: []string{
				`log.attributes["message"]`,
				`log.body["message"]`,
			}},
			"origin": {Value: "otel"},
		},
	}, readGoldenFile(t, "logs.otlp.json"))
	require.NoError(t, err)
	require.Len(t, preview.Rows, 1)
	assert.Equal(t, int64(2), preview.Rows[0]["attempt"])
	assert.Equal(t, "unknown", preview.Rows[0]["environment"])
	assert.Equal(t, "order accepted", preview.Rows[0]["message"])
	assert.Equal(t, "otel", preview.Rows[0]["origin"])

	attempt := findPreviewColumn(t, preview, "attempt")
	assert.Equal(t, []string{"int"}, attempt.ObservedTypes)
	assert.True(t, attempt.RuntimeDependent)
	assert.Equal(t, []MappingSelectionPreview{{Source: `log.body["attempt"]`, Count: 1}}, attempt.Selections)
	assert.Equal(t, []MappingSelectionPreview{{Source: "default", Count: 1}}, findPreviewColumn(t, preview, "environment").Selections)
	assert.Equal(t, []MappingSelectionPreview{{Source: `log.body["message"]`, Count: 1}}, findPreviewColumn(t, preview, "message").Selections)
	assert.Equal(t, []MappingSelectionPreview{{Source: "value", Count: 1}}, findPreviewColumn(t, preview, "origin").Selections)
}

func TestPreviewMappingCollectsCastFailuresAndKeepsValidRows(t *testing.T) {
	preview, err := PreviewMapping(signalLogs, SignalIngestionConfig{
		Table: "analytics.logs",
		Mapping: MappingConfig{
			"attempt": {Source: `log.body["attempt"]`, Cast: "int"},
			"enabled": {Source: `log.body["enabled"]`, Cast: "boolean"},
		},
	}, []byte(`{
  "resourceLogs": [{
    "scopeLogs": [{
      "logRecords": [
        {"body":{"kvlistValue":{"values":[
          {"key":"attempt","value":{"stringValue":"first"}},
          {"key":"enabled","value":{"stringValue":"sometimes"}}
        ]}}},
        {"body":{"kvlistValue":{"values":[
          {"key":"attempt","value":{"stringValue":"2"}},
          {"key":"enabled","value":{"stringValue":"false"}}
        ]}}}
      ]
    }]
  }]
}`))
	require.NoError(t, err)
	assert.Equal(t, 2, preview.Records)
	assert.Equal(t, 1, preview.ValidRecords)
	assert.Equal(t, 1, preview.InvalidRecords)
	assert.Equal(t, 2, preview.ErrorCount)
	require.Len(t, preview.Errors, 2)
	assert.Equal(t, 1, preview.Errors[0].Record)
	assert.Equal(t, "attempt", preview.Errors[0].Column)
	assert.Equal(t, `log.body["attempt"]`, preview.Errors[0].Source)
	assert.Equal(t, `string value "first" cannot be converted to int`, preview.Errors[0].Message)
	assert.NotContains(t, preview.Errors[0].Message, "strconv")
	assert.Equal(t, "enabled", preview.Errors[1].Column)
	require.Len(t, preview.Rows, 1)
	assert.Equal(t, int64(2), preview.Rows[0]["attempt"])
	assert.Equal(t, false, preview.Rows[0]["enabled"])
	attempt := findPreviewColumn(t, preview, "attempt")
	assert.Equal(t, 1, attempt.Errors)
	assert.Equal(t, 1, attempt.Present)
	assert.Equal(t, []MappingSelectionPreview{{Source: `log.body["attempt"]`, Count: 2}}, attempt.Selections)
}

func TestDescribeIngestionMappingsShowsExpandedRuleAndOutputType(t *testing.T) {
	descriptions, err := DescribeIngestionMappings(IngestionConfig{Signals: IngestionSignalsConfig{
		Logs: SignalIngestionConfig{
			Table: "analytics.logs",
			Mapping: MappingConfig{
				"service": {
					Sources: []string{
						`resource.attributes["service.name"]`,
						`resource.attributes["service"]`,
					},
					Default: "unknown",
					Cast:    "string",
				},
			},
		},
	}})
	require.NoError(t, err)
	require.Len(t, descriptions, 1)
	require.Len(t, descriptions[0].Columns, 1)
	column := descriptions[0].Columns[0]
	assert.Equal(t, `resource.attributes["service.name"] -> resource.attributes["service"] -> default("unknown") | cast=string`, column.Source)
	assert.Equal(t, "string", column.OutputType)
	assert.True(t, column.RuntimeDependent)
}

func findPreviewColumn(t *testing.T, preview MappingPreview, name string) MappingColumnPreview {
	t.Helper()
	for _, column := range preview.Columns {
		if column.Column == name {
			return column
		}
	}
	t.Fatalf("preview column %q not found", name)
	return MappingColumnPreview{}
}
