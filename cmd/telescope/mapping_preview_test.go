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

func TestPrintMappingPreviewRejectsObservedTypeMismatch(t *testing.T) {
	preview := scopedbexporter.MappingPreview{
		Signal:  "logs",
		Records: 1,
		Columns: []scopedbexporter.MappingColumnPreview{{
			MappingColumnDescription: scopedbexporter.MappingColumnDescription{
				Column:           "body",
				Source:           "log.body",
				SelectorType:     "dynamic",
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
	err := printMappingPreview(&output, mappingSample{path: "sample.json", preview: preview}, []scopedbexporter.SignalDestinationValidation{destination})
	require.Error(t, err)
	assert.ErrorContains(t, err, "incompatible with destination columns: body")
	assert.Contains(t, output.String(), "sample-mismatch")
	assert.Contains(t, output.String(), `{"body":{"message":"hello"}}`)
}

func TestPreviewColumnResultDoesNotTreatMissingSampleValuesAsValidated(t *testing.T) {
	destination := scopedbexporter.DestinationColumnValidation{
		MappingColumnDescription: scopedbexporter.MappingColumnDescription{
			Column:       "trace_id",
			SelectorType: "string",
		},
		TargetType:    "string",
		Compatibility: scopedbexporter.MappingCompatible,
	}

	assert.Equal(t, "unobserved", previewColumnResult(scopedbexporter.MappingColumnPreview{
		MappingColumnDescription: destination.MappingColumnDescription,
		Total:                    2,
	}, destination))
	assert.Equal(t, "partial", previewColumnResult(scopedbexporter.MappingColumnPreview{
		MappingColumnDescription: destination.MappingColumnDescription,
		Present:                  1,
		Total:                    2,
		ObservedTypes:            []string{"string"},
	}, destination))
}
