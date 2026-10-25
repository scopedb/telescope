// Copyright 2026 ScopeDB, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build integration

package scopedbexporter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	scopedb "github.com/scopedb/goscopedb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestV02CompatibleAppendIntegration(t *testing.T) {
	client, endpoint, apiKey := scopeDBIntegrationClient(t)
	data, err := os.ReadFile("../deploy/telescope.v0.2-compatible.yaml")
	require.NoError(t, err)
	var cfg IngestionConfig
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	schemaData, err := os.ReadFile("testdata/golden/v0.2/schema.json")
	require.NoError(t, err)
	var schemas map[string]map[string]string
	require.NoError(t, json.Unmarshal(schemaData, &schemas))
	for _, signal := range cfg.EnabledSignals() {
		t.Run(signal, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			table := client.Table(fmt.Sprintf("telescope_compat_%s_%d", signal, time.Now().UnixNano()))
			var columns []string
			for name, dataType := range schemas[signal] {
				columns = append(columns, name+" "+dataType)
			}
			_, err := client.Statement("CREATE TABLE " + table.Identifier() + " (" + strings.Join(columns, ",") + ")").Execute(ctx)
			require.NoError(t, err)
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_, err := table.Catalog().Delete(cleanupCtx, scopedb.CatalogOptions{AllowDestructive: true})
				assert.NoError(t, err)
			}()
			configured, _ := cfg.Signal(signal)
			configured.Table = table.Name
			switch signal {
			case signalLogs:
				cfg.Signals.Logs = configured
			case signalTraces:
				cfg.Signals.Traces = configured
			case signalMetrics:
				cfg.Signals.Metrics = configured
			}
			exporter, err := newIngestionClient(endpoint, apiKey, cfg)
			require.NoError(t, err)
			defer exporter.Close()
			_, validationErr := exporter.inspectDestination(ctx, signal)
			require.NoError(t, validationErr)
			payload, err := mapOTLPSample(signal, readGoldenFile(t, signal+".otlp.json"))
			require.NoError(t, err)
			outcome := exporter.send(ctx, signal, payload, nil)
			require.NoError(t, outcome.err)
			require.Len(t, outcome.committed, len(payload.Records))
			result, err := client.Query(ctx, "FROM "+table.Identifier())
			require.NoError(t, err)
			objects, err := result.ToObjects()
			require.NoError(t, err)
			require.Len(t, objects, len(payload.Records))
			byID := make(map[string]map[string]scopedb.Value, len(objects))
			for _, row := range objects {
				byID[row["row_id"].(string)] = row
			}
			plan, err := compileMappingPlan(signal, table.Name, configured.Mapping)
			require.NoError(t, err)
			for _, record := range payload.Records {
				expected, err := plan.project(record)
				require.NoError(t, err)
				actual, ok := byID[expected["row_id"].(string)]
				require.True(t, ok)
				require.Nil(t, actual["ingest_ts"])
				for column, want := range expected {
					// Normalize SDK number and timestamp representations to their JSON values.
					expectedJSON, err := json.Marshal(want)
					require.NoError(t, err)
					actualJSON, err := json.Marshal(actual[column])
					require.NoError(t, err)
					if schemas[signal][column] == "object" {
						if value, ok := actual[column].(string); ok {
							actualJSON = []byte(value)
						}
					}
					require.JSONEq(t, string(expectedJSON), string(actualJSON), "column %s", column)
				}
			}
		})
	}
}
