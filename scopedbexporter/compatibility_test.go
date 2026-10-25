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
	"go.yaml.in/yaml/v3"
)

func TestV02CompatibleMapping(t *testing.T) {
	data, err := os.ReadFile("../deploy/telescope.v0.2-compatible.yaml")
	require.NoError(t, err)
	var cfg IngestionConfig
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	require.NoError(t, cfg.Validate())
	for _, signal := range cfg.EnabledSignals() {
		t.Run(signal, func(t *testing.T) {
			configured, _ := cfg.Signal(signal)
			payload, err := mapOTLPSample(signal, readGoldenFile(t, signal+".otlp.json"))
			require.NoError(t, err)
			plan, err := compileMappingPlan(signal, configured.Table, configured.Mapping)
			require.NoError(t, err)
			rows := make([]map[string]any, 0, len(payload.Records))
			for _, record := range payload.Records {
				row, err := plan.project(record)
				require.NoError(t, err)
				rows = append(rows, row)
			}
			expected, err := os.ReadFile(filepath.Join("testdata", "golden", "v0.2", signal+".rows.json"))
			require.NoError(t, err)
			var oldRows []map[string]any
			require.NoError(t, json.Unmarshal(expected, &oldRows))
			actualJSON, err := json.Marshal(rows)
			require.NoError(t, err)
			var actual []map[string]any
			require.NoError(t, json.Unmarshal(actualJSON, &actual))
			require.Len(t, actual, len(oldRows))
			for i, want := range oldRows {
				require.Len(t, actual[i]["row_id"], 64)
				delete(actual[i], "row_id")
				// The full record preserves old keys and adds OTLP fields that v0.2 omitted.
				wantRecord := want["record"].(map[string]any)
				gotRecord := actual[i]["record"].(map[string]any)
				requireRecordContains(t, wantRecord, gotRecord)
				delete(want, "record")
				delete(actual[i], "record")
				require.Equal(t, want, actual[i])
			}
		})
	}
}

func requireRecordContains(t *testing.T, want, got any) {
	t.Helper()
	switch value := want.(type) {
	case map[string]any:
		require.IsType(t, map[string]any{}, got)
		for key, child := range value {
			require.Contains(t, got, key)
			requireRecordContains(t, child, got.(map[string]any)[key])
		}
	case []any:
		require.IsType(t, []any{}, got)
		actual := got.([]any)
		require.Len(t, actual, len(value))
		for i, child := range value {
			requireRecordContains(t, child, actual[i])
		}
	default:
		require.Equal(t, want, got)
	}
}

func TestRecordIDSurvivesRebatching(t *testing.T) {
	first := telemetryRecord{"attributes": map[string]any{"b": 2, "a": 1}, "body": "message", "timestamp_unix_nano": "100"}
	second := telemetryRecord{"timestamp_unix_nano": "100", "body": "message", "attributes": map[string]any{"a": 1, "b": 2}}
	id, ok := recordID(signalLogs, first)
	require.True(t, ok)
	replay, ok := recordID(signalLogs, second)
	require.True(t, ok)
	require.Equal(t, id, replay)
	second["timestamp_unix_nano"] = "101"
	changed, ok := recordID(signalLogs, second)
	require.True(t, ok)
	require.NotEqual(t, id, changed)
	otherSignal, ok := recordID(signalTraces, first)
	require.True(t, ok)
	require.NotEqual(t, id, otherSignal)
}
