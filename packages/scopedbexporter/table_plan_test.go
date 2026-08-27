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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanIngestionTablesCreatesMissingTable(t *testing.T) {
	server := missingTablePlanServer(t, "app")
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
			Requirements: []TableColumnRequirement{{
				Signal:     "logs",
				Mapping:    "log.severity_number",
				OutputType: "int",
			}},
		},
		{Name: "message", RequiredType: "string", ActualType: "string", Status: TableColumnExists},
		{Name: "service", RequiredType: "string", Status: TableColumnAdd},
	}, plan.Tables[0].Columns)
	assert.True(t, plan.Blocked())

	_, err = RenderTablePlanScopeQL(plan)
	require.ErrorContains(t, err, "table plan is blocked: app.logs")
}

func TestPlanIngestionTablesDoesNotInferTypeFromSample(t *testing.T) {
	server := missingTablePlanServer(t, "app")
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
	assert.Equal(t, []TableColumnRequirement{{
		Signal:        "logs",
		Mapping:       "log.body",
		OutputType:    "dynamic",
		Sampled:       true,
		Present:       5,
		Total:         5,
		ObservedTypes: []string{"string"},
		SuggestedCast: "string",
	}}, column.Requirements)
}

func TestPlanIngestionTablesPreservesSampleCoverageAndSelections(t *testing.T) {
	server := missingTablePlanServer(t, "app")
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{Logs: SignalIngestionConfig{
		Table: "app.logs",
		Mapping: MappingConfig{
			"service": {
				Sources: []string{`resource.attributes["service.name"]`, `resource.attributes["service"]`},
				Default: "unknown",
				Cast:    "string",
			},
		},
	}}}
	previews := []MappingPreview{{
		Signal: "logs",
		Columns: []MappingColumnPreview{{
			MappingColumnDescription: MappingColumnDescription{
				Column:           "service",
				Source:           `resource.attributes["service.name"] -> resource.attributes["service"] -> default("unknown") | cast=string`,
				OutputType:       "string",
				RuntimeDependent: true,
			},
			ObservedTypes: []string{"string"},
			Present:       10,
			Total:         10,
			Selections: []MappingSelectionPreview{
				{Source: `resource.attributes["service.name"]`, Count: 8},
				{Source: "default", Count: 2},
			},
		}},
	}}

	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, previews)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	require.Len(t, plan.Tables[0].Columns, 1)
	assert.Equal(t, []TableColumnRequirement{{
		Signal:        "logs",
		Mapping:       `resource.attributes["service.name"] -> resource.attributes["service"] -> default("unknown") | cast=string`,
		OutputType:    "string",
		Sampled:       true,
		Present:       10,
		Total:         10,
		ObservedTypes: []string{"string"},
		Selections: []MappingSelectionPreview{
			{Source: `resource.attributes["service.name"]`, Count: 8},
			{Source: "default", Count: 2},
		},
	}}, plan.Tables[0].Columns[0].Requirements)
}

func TestPlanIngestionTablesUsesExplicitStructuredCast(t *testing.T) {
	server := missingTablePlanServer(t, "app")
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
	server := missingTablePlanServer(t, "app")
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
	server := missingTablePlanServer(t, "app")
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

func TestPlanIngestionTablesCreatesMissingDatabaseAndSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/databases/analytics/schemas/otel/tables/logs",
			"/v1/databases/analytics/schemas/otel",
			"/v1/databases/analytics":
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected catalog request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{Logs: SignalIngestionConfig{
		Table:   "analytics.otel.logs",
		Mapping: shorthandMapping(map[string]string{"message": "log.message"}),
	}}}
	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, nil)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	table := plan.Tables[0]
	assert.Equal(t, "analytics", table.Database)
	assert.Equal(t, "otel", table.Schema)
	assert.True(t, table.CreateDatabase)
	assert.True(t, table.CreateSchema)

	scopeql, err := RenderTablePlanScopeQL(plan)
	require.NoError(t, err)
	assert.Equal(t, "CREATE DATABASE `analytics`;\nCREATE SCHEMA `analytics`.`otel`;\nCREATE TABLE `analytics`.`otel`.`logs` (\n    `message` string\n);\n", scopeql)
}

func TestPlanIngestionTablesCreatesMissingSchemaInExistingDatabase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/databases/analytics/schemas/otel/tables/logs", "/v1/databases/analytics/schemas/otel":
			http.NotFound(w, r)
		case "/v1/databases/analytics":
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"name": "analytics", "comment": nil}))
		default:
			t.Fatalf("unexpected catalog request: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	ingestion := IngestionConfig{Signals: IngestionSignalsConfig{Logs: SignalIngestionConfig{
		Table:   "analytics.otel.logs",
		Mapping: shorthandMapping(map[string]string{"message": "log.message"}),
	}}}
	plan, err := PlanIngestionTables(context.Background(), server.URL, "test-key", ingestion, nil)
	require.NoError(t, err)
	require.Len(t, plan.Tables, 1)
	assert.False(t, plan.Tables[0].CreateDatabase)
	assert.True(t, plan.Tables[0].CreateSchema)
}

func TestRenderTablePlanScopeQLDeduplicatesNamespaces(t *testing.T) {
	plan := IngestionTablePlan{Version: TablePlanVersion, Tables: []TablePlan{
		{
			Table:          "analytics.otel.logs",
			Database:       "analytics",
			Schema:         "otel",
			CreateDatabase: true,
			CreateSchema:   true,
			Action:         TableActionCreate,
			Columns:        []TableColumnPlan{{Name: "message", RequiredType: "string", Status: TableColumnCreate}},
		},
		{
			Table:          "analytics.otel.spans",
			Database:       "analytics",
			Schema:         "otel",
			CreateDatabase: true,
			CreateSchema:   true,
			Action:         TableActionCreate,
			Columns:        []TableColumnPlan{{Name: "name", RequiredType: "string", Status: TableColumnCreate}},
		},
	}}

	scopeql, err := RenderTablePlanScopeQL(plan)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(scopeql, "CREATE DATABASE"))
	assert.Equal(t, 1, strings.Count(scopeql, "CREATE SCHEMA"))
	assert.Less(t, strings.Index(scopeql, "CREATE DATABASE"), strings.Index(scopeql, "CREATE SCHEMA"))
	assert.Less(t, strings.Index(scopeql, "CREATE SCHEMA"), strings.Index(scopeql, "CREATE TABLE"))
}

func missingTablePlanServer(t *testing.T, schema string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		schemaPath := "/v1/databases/scopedb/schemas/" + schema
		switch {
		case r.URL.Path == schemaPath:
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"database": "scopedb",
				"name":     schema,
				"comment":  nil,
			}))
		case strings.HasPrefix(r.URL.Path, schemaPath+"/tables/"):
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected catalog request: %s", r.URL.Path)
		}
	}))
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
