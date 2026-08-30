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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestGoldenMappingContractV1(t *testing.T) {
	tests := []struct {
		name       string
		mapRecords func(t *testing.T, input []byte) []Record
	}{
		{
			name: "logs",
			mapRecords: func(t *testing.T, input []byte) []Record {
				logs, err := (&plog.JSONUnmarshaler{}).UnmarshalLogs(input)
				require.NoError(t, err)
				payload, err := mapLogs(logs)
				require.NoError(t, err)
				return payload.Records
			},
		},
		{
			name: "traces",
			mapRecords: func(t *testing.T, input []byte) []Record {
				traces, err := (&ptrace.JSONUnmarshaler{}).UnmarshalTraces(input)
				require.NoError(t, err)
				payload, err := mapTraces(traces)
				require.NoError(t, err)
				return payload.Records
			},
		},
		{
			name: "metrics",
			mapRecords: func(t *testing.T, input []byte) []Record {
				metrics, err := (&pmetric.JSONUnmarshaler{}).UnmarshalMetrics(input)
				require.NoError(t, err)
				payload, err := mapMetrics(metrics)
				require.NoError(t, err)
				return payload.Records
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := readGoldenFile(t, tt.name+".otlp.json")
			expected := readGoldenFile(t, tt.name+".records.json")
			actual, err := json.MarshalIndent(tt.mapRecords(t, input), "", "  ")
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(actual), "mapped records:\n%s", actual)
		})
	}
}

func readGoldenFile(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "golden", "v1", name))
	require.NoError(t, err)
	return contents
}

func goldenOTLPProtobuf(t *testing.T, signal string, sample []byte) []byte {
	t.Helper()
	var encoded []byte
	var err error
	switch signal {
	case signalLogs:
		request := plogotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalJSON(sample))
		encoded, err = request.MarshalProto()
	case signalTraces:
		request := ptraceotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalJSON(sample))
		encoded, err = request.MarshalProto()
	case signalMetrics:
		request := pmetricotlp.NewExportRequest()
		require.NoError(t, request.UnmarshalJSON(sample))
		encoded, err = request.MarshalProto()
	default:
		t.Fatalf("unsupported signal %q", signal)
	}
	require.NoError(t, err)
	return encoded
}
