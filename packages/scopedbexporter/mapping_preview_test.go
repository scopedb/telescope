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
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
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
				Mapping: tt.mapping,
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
		signal  string
		source  string
		marshal func(t *testing.T, sample []byte) []byte
	}{
		{
			signal: signalLogs,
			source: "log.message",
			marshal: func(t *testing.T, sample []byte) []byte {
				request := plogotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(sample))
				encoded, err := request.MarshalProto()
				require.NoError(t, err)
				return encoded
			},
		},
		{
			signal: signalTraces,
			source: "span.name",
			marshal: func(t *testing.T, sample []byte) []byte {
				request := ptraceotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(sample))
				encoded, err := request.MarshalProto()
				require.NoError(t, err)
				return encoded
			},
		},
		{
			signal: signalMetrics,
			source: "metric.name",
			marshal: func(t *testing.T, sample []byte) []byte {
				request := pmetricotlp.NewExportRequest()
				require.NoError(t, request.UnmarshalJSON(sample))
				encoded, err := request.MarshalProto()
				require.NoError(t, err)
				return encoded
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.signal, func(t *testing.T) {
			sample := tt.marshal(t, readGoldenFile(t, tt.signal+".otlp.json"))
			preview, err := PreviewMapping(tt.signal, SignalIngestionConfig{
				Table:   "analytics." + tt.signal,
				Mapping: map[string]string{"value": tt.source},
			}, sample)
			require.NoError(t, err)
			assert.NotZero(t, preview.Records)
			assert.NotEmpty(t, preview.Rows[0]["value"])
		})
	}
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
