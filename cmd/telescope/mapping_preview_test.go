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

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSampleFlagsRequireOneFilePerSignal(t *testing.T) {
	var samples sampleFlags
	require.NoError(t, samples.Set("traces=sample.json"))
	assert.Equal(t, map[string]string{"traces": "sample.json"}, samples.paths)
	assert.ErrorContains(t, samples.Set("traces=other.json"), "provided more than once")
	assert.ErrorContains(t, samples.Set("profiles=sample.json"), "unsupported sample signal")
	assert.ErrorContains(t, samples.Set("logs"), "signal=path")
}

func TestLoadMappingSamplesReadsStdin(t *testing.T) {
	ingestion := scopedbexporter.IngestionConfig{Signals: scopedbexporter.IngestionSignalsConfig{
		Logs: scopedbexporter.SignalIngestionConfig{
			Table:   "app.logs",
			Mapping: scopedbexporter.MappingConfig{"message": {Source: "log.message"}},
		},
	}}
	sample := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"}}]}]}]}`

	previews, err := loadMappingSamples(map[string]string{"logs": "-"}, ingestion, strings.NewReader(sample))
	require.NoError(t, err)
	require.Len(t, previews, 1)
	assert.Equal(t, "-", previews[0].path)
	assert.Equal(t, 1, previews[0].preview.Records)
	assert.Equal(t, "hello", previews[0].preview.Rows[0]["message"])
}

func TestDeploymentSamplesCanBePreviewed(t *testing.T) {
	ingestion := scopedbexporter.IngestionConfig{Signals: scopedbexporter.IngestionSignalsConfig{
		Logs: scopedbexporter.SignalIngestionConfig{
			Table:   "app.logs",
			Mapping: scopedbexporter.MappingConfig{"message": {Source: "log.message"}},
		},
		Traces: scopedbexporter.SignalIngestionConfig{
			Table:   "app.traces",
			Mapping: scopedbexporter.MappingConfig{"name": {Source: "span.name"}},
		},
		Metrics: scopedbexporter.SignalIngestionConfig{
			Table:   "app.metrics",
			Mapping: scopedbexporter.MappingConfig{"name": {Source: "metric.name"}},
		},
	}}
	paths := make(map[string]string, 3)
	for _, signal := range []string{"logs", "traces", "metrics"} {
		paths[signal] = filepath.Join("..", "..", "deploy", "samples", signal+".otlp.json")
	}

	previews, err := loadMappingSamples(paths, ingestion, strings.NewReader(""))
	require.NoError(t, err)
	require.Len(t, previews, 3)
	for _, sample := range previews {
		assert.Equal(t, 1, sample.preview.Records, sample.preview.Signal)
		assert.Zero(t, sample.preview.ErrorCount, sample.preview.Signal)
	}
}

func TestLoadMappingSamplesRejectsMultipleStdinSamples(t *testing.T) {
	ingestion := scopedbexporter.IngestionConfig{Signals: scopedbexporter.IngestionSignalsConfig{
		Logs: scopedbexporter.SignalIngestionConfig{
			Table:   "app.logs",
			Mapping: scopedbexporter.MappingConfig{"message": {Source: "log.message"}},
		},
		Traces: scopedbexporter.SignalIngestionConfig{
			Table:   "app.traces",
			Mapping: scopedbexporter.MappingConfig{"name": {Source: "span.name"}},
		},
	}}

	_, err := loadMappingSamples(
		map[string]string{"logs": "-", "traces": "-"},
		ingestion,
		strings.NewReader("{}"),
	)
	assert.ErrorContains(t, err, "stdin can provide only one signal sample")
}

func TestPrintMappingPreviewRejectsObservedTypeMismatch(t *testing.T) {
	preview := scopedbexporter.MappingPreview{
		Signal:       "logs",
		Records:      1,
		ValidRecords: 1,
		Columns: []scopedbexporter.MappingColumnPreview{{
			MappingColumnDescription: scopedbexporter.MappingColumnDescription{
				Column:           "body",
				Source:           "log.body",
				OutputType:       "dynamic",
				RuntimeDependent: true,
			},
			Present:       1,
			Total:         1,
			ObservedTypes: []string{"object"},
		}},
		Rows: []map[string]any{{"body": map[string]any{"message": "hello"}}},
	}
	destination := scopedbexporter.SignalDestinationValidation{
		Signal: "logs",
		Columns: []scopedbexporter.DestinationColumnValidation{{
			MappingColumnDescription: preview.Columns[0].MappingColumnDescription,
			TargetType:               "string",
			Compatibility:            scopedbexporter.MappingRuntimeDependent,
		}},
	}

	var output bytes.Buffer
	err := printMappingPreview(&output, mappingSample{path: "sample.json", preview: preview}, []scopedbexporter.SignalDestinationValidation{destination}, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "body (sample type)")
	assert.Contains(t, output.String(), "ERROR sample type")
	assert.Contains(t, output.String(), `{"body":{"message":"hello"}}`)
}

func TestPreviewColumnResultDoesNotTreatMissingSampleValuesAsValidated(t *testing.T) {
	destination := scopedbexporter.DestinationColumnValidation{
		MappingColumnDescription: scopedbexporter.MappingColumnDescription{
			Column:     "trace_id",
			OutputType: "string",
		},
		TargetType:    "string",
		Compatibility: scopedbexporter.MappingCompatible,
	}

	assert.Equal(t, "WARN unobserved", previewColumnResult(scopedbexporter.MappingColumnPreview{
		MappingColumnDescription: destination.MappingColumnDescription,
		Total:                    2,
	}, destination))
	assert.Equal(t, "WARN partial", previewColumnResult(scopedbexporter.MappingColumnPreview{
		MappingColumnDescription: destination.MappingColumnDescription,
		Present:                  1,
		Total:                    2,
		ObservedTypes:            []string{"string"},
	}, destination))
}

func TestPrintMappingPreviewShowsDefaultUseAndStrictFailsWarnings(t *testing.T) {
	preview := scopedbexporter.MappingPreview{
		Signal:       "logs",
		Records:      2,
		ValidRecords: 2,
		Columns: []scopedbexporter.MappingColumnPreview{{
			MappingColumnDescription: scopedbexporter.MappingColumnDescription{
				Column:     "tenant",
				Source:     `resource.attributes["tenant"] -> default("unknown")`,
				OutputType: "string",
			},
			Present:       2,
			Total:         2,
			ObservedTypes: []string{"string"},
			Selections: []scopedbexporter.MappingSelectionPreview{{
				Source: "default",
				Count:  2,
			}},
		}},
		Rows: []map[string]any{{"tenant": "unknown"}, {"tenant": "unknown"}},
	}

	var output bytes.Buffer
	require.NoError(t, printMappingPreview(&output, mappingSample{path: "sample.json", preview: preview}, nil, false))
	assert.Contains(t, output.String(), "SELECTED")
	assert.NotContains(t, output.String(), "TARGET")
	assert.Contains(t, output.String(), "default (2)")
	assert.Contains(t, output.String(), "WARN default only")

	output.Reset()
	err := printMappingPreview(&output, mappingSample{path: "sample.json", preview: preview}, nil, true)
	require.Error(t, err)
	assert.ErrorContains(t, err, "warnings under --strict")
}

func TestPrintMappingPreviewReportsAllMappingErrorsWithSelectedSource(t *testing.T) {
	preview := scopedbexporter.MappingPreview{
		Signal:         "logs",
		Records:        2,
		ValidRecords:   1,
		InvalidRecords: 1,
		ErrorCount:     2,
		Errors: []scopedbexporter.MappingPreviewError{
			{Record: 1, Column: "attempt", Source: `log.body["attempt"]`, Message: `string value "first" cannot be converted to int`},
			{Record: 1, Column: "enabled", Source: `log.body["enabled"]`, Message: `string value "sometimes" cannot be converted to boolean`},
		},
		Columns: []scopedbexporter.MappingColumnPreview{
			{
				MappingColumnDescription: scopedbexporter.MappingColumnDescription{Column: "attempt", OutputType: "int"},
				Present:                  1,
				Total:                    2,
				Errors:                   1,
				ObservedTypes:            []string{"int"},
				Selections:               []scopedbexporter.MappingSelectionPreview{{Source: `log.body["attempt"]`, Count: 2}},
			},
			{
				MappingColumnDescription: scopedbexporter.MappingColumnDescription{Column: "enabled", OutputType: "boolean"},
				Present:                  1,
				Total:                    2,
				Errors:                   1,
				ObservedTypes:            []string{"boolean"},
				Selections:               []scopedbexporter.MappingSelectionPreview{{Source: `log.body["enabled"]`, Count: 2}},
			},
		},
		Rows: []map[string]any{{"attempt": int64(2), "enabled": false}},
	}

	var output bytes.Buffer
	err := printMappingPreview(&output, mappingSample{path: "sample.json", preview: preview}, nil, false)
	require.Error(t, err)
	assert.Contains(t, output.String(), "mapping errors (2)")
	assert.Contains(t, output.String(), `record 1, column attempt <- log.body["attempt"]`)
	assert.Contains(t, output.String(), "projected NDJSON (first 1 of 1 valid; 1 invalid)")
	assert.Contains(t, output.String(), "sample result: errors=2, warnings=0")
}
