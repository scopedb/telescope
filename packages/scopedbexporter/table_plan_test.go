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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanIngestionTablesCreatesMissingTable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/databases/scopedb/schemas/app/tables/logs", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{Logs: SignalIngestionConfig{
		Table: "app.logs",
		Mapping: shorthandMapping(map[string]string{
			"message": "log.message",
			"ts":      "log.timestamp",
		}),
	}}}

	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, nil)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	assert.Equal(t, TablePlanVersion, plan.Version)
	assert.Equal(t, TableActionCreate, plan.Tables[0].Action)
	assert.False(t, plan.Tables[0].Exists)
	assert.Equal(t, []string{"logs"}, plan.Tables[0].Signals)
	assert.Equal(t, []TableColumnPlan{
		{Name: "message", RequiredType: "string", Status: TableColumnCreate},
		{Name: "ts", RequiredType: "timestamp", Status: TableColumnCreate},
	}, plan.Tables[0].Columns)
	assert.False(t, plan.Blocked())

	scopeql, err := RenderTablePlanScopeQL(plan)
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE `app`.`logs` (\n    `message` string,\n    `ts` timestamp\n);\n", scopeql)
}

func TestPlanIngestionTablesClassifiesExistingColumns(t *testing.T) {
	server := tablePlanServer(t, "app", "logs", []map[string]any{
		{"name": "count", "data_type": "string"},
		{"name": "message", "data_type": "string"},
	})
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{Logs: SignalIngestionConfig{
		Table: "app.logs",
		Mapping: shorthandMapping(map[string]string{
			"count":   "log.severity_number",
			"message": "log.message",
			"service": "resource.schema_url",
		}),
	}}}

	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, nil)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	assert.Equal(t, TableActionBlocked, plan.Tables[0].Action)
	assert.Equal(t, []TableColumnPlan{
		{
			Name:         "count",
			RequiredType: "int",
			ActualType:   "string",
			Status:       TableColumnConflict,
			Reason:       "mapping requires int but the table has string",
		},
		{Name: "message", RequiredType: "string", ActualType: "string", Status: TableColumnExists},
		{Name: "service", RequiredType: "string", Status: TableColumnAdd},
	}, plan.Tables[0].Columns)
	assert.True(t, plan.Blocked())

	_, err = RenderTablePlanScopeQL(plan)
	require.ErrorContains(t, err, "table plan is blocked: app.logs")
}

func TestPlanIngestionTablesDoesNotInferTypeFromSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{Logs: SignalIngestionConfig{
		Table: "app.logs",
		Mapping: shorthandMapping(map[string]string{
			"body": "log.body",
		}),
	}}}
	previews := []MappingPreview{{
		Signal: "logs",
		Columns: []MappingColumnPreview{{
			MappingColumnDescription: MappingColumnDescription{Column: "body", OutputType: "dynamic", RuntimeDependent: true},
			ObservedTypes:            []string{"string"},
			Present:                  5,
			Total:                    5,
		}},
	}}

	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, previews)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	require.Len(t, plan.Tables[0].Columns, 1)
	column := plan.Tables[0].Columns[0]
	assert.Equal(t, TableColumnBlocked, column.Status)
	assert.Empty(t, column.RequiredType)
	assert.Equal(t, []string{"string"}, column.ObservedTypes)
	assert.Equal(t, "output type is runtime-dependent; add an explicit cast to the mapping", column.Reason)
}

func TestPlanIngestionTablesUsesExplicitStructuredCast(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{Logs: SignalIngestionConfig{
		Table: "app.logs",
		Mapping: MappingConfig{
			"body": {Source: "log.body", Cast: "object"},
		},
	}}}

	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, nil)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	require.Len(t, plan.Tables[0].Columns, 1)
	assert.Equal(t, TableActionCreate, plan.Tables[0].Action)
	assert.Equal(t, TableColumnPlan{Name: "body", RequiredType: "object", Status: TableColumnCreate}, plan.Tables[0].Columns[0])
}

func TestPlanIngestionTablesMergesSignalsSharingATable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{
		Logs: SignalIngestionConfig{
			Table: "app.events",
			Mapping: shorthandMapping(map[string]string{
				"message": "log.message",
				"service": "resource.schema_url",
			}),
		},
		Traces: SignalIngestionConfig{
			Table: "app.events",
			Mapping: shorthandMapping(map[string]string{
				"span_name": "span.name",
				"service":   "resource.schema_url",
			}),
		},
	}}

	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, nil)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	assert.Equal(t, []string{"logs", "traces"}, plan.Tables[0].Signals)
	assert.Equal(t, []string{"message", "service", "span_name"}, tablePlanColumnNames(plan.Tables[0].Columns))
}

func TestPlanIngestionTablesBlocksConflictingSharedColumn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{
		Logs: SignalIngestionConfig{
			Table:   "app.events",
			Mapping: shorthandMapping(map[string]string{"value": "log.message"}),
		},
		Traces: SignalIngestionConfig{
			Table:   "app.events",
			Mapping: shorthandMapping(map[string]string{"value": "span.duration_ns"}),
		},
	}}

	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, nil)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	require.Len(t, plan.Tables[0].Columns, 1)
	assert.Equal(t, TableActionBlocked, plan.Tables[0].Action)
	assert.Equal(t, TableColumnConflict, plan.Tables[0].Columns[0].Status)
	assert.Equal(t, "signals require different output types: logs=string, traces=int", plan.Tables[0].Columns[0].Reason)
}

func TestRenderTablePlanScopeQLAddsColumns(t *testing.T) {
	plan := IngestionTablePlan{Version: TablePlanVersion, Tables: []TablePlan{{
		Table:   "app.logs",
		Signals: []string{"logs"},
		Exists:  true,
		Action:  TableActionAlter,
		Columns: []TableColumnPlan{
			{Name: "message", RequiredType: "string", ActualType: "string", Status: TableColumnExists},
			{Name: "service", RequiredType: "string", Status: TableColumnAdd},
			{Name: "ts", RequiredType: "timestamp", Status: TableColumnAdd},
		},
	}}}

	scopeql, err := RenderTablePlanScopeQL(plan)
	require.NoError(t, err)
	assert.Equal(t, "ALTER TABLE `app`.`logs` ADD COLUMN `service` string;\nALTER TABLE `app`.`logs` ADD COLUMN `ts` timestamp;\n", scopeql)
}

func tablePlanServer(t *testing.T, schema string, table string, columns []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"database":     "scopedb",
			"schema":       schema,
			"name":         table,
			"columns":      columns,
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct_on":  map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
}

func tablePlanColumnNames(columns []TableColumnPlan) []string {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.Name)
	}
	return names
}
