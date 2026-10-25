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

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

func TestGoldenMappingContractV1(t *testing.T) {
	for _, signal := range []string{signalLogs, signalTraces, signalMetrics} {
		t.Run(signal, func(t *testing.T) {
			payload, err := mapOTLPSample(signal, readGoldenFile(t, signal+".otlp.json"))
			require.NoError(t, err)
			actual, err := json.MarshalIndent(payload.Records, "", "  ")
			require.NoError(t, err)
			require.JSONEq(t, string(readGoldenFile(t, signal+".records.json")), string(actual))
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
