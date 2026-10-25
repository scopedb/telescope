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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scopedb/telescope/scopedbexporter"
	"github.com/stretchr/testify/require"
)

func TestPlanCommandRendersScopeQLForMissingTable(t *testing.T) {
	clearBootstrapEnv(t)
	server := missingTableCommandServer(t, "app")
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  logs:
    table: app.logs
    mapping:
      message: log.message
      ts: log.timestamp
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runPlanCommand(context.Background(), []string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--format", "scopeql",
		configPath,
	}, strings.NewReader(""), &stdout, &stderr)
	require.NoError(t, err)

	want := "CREATE TABLE `app`.`logs` (\n    `message` string,\n    `ts` timestamp\n);\n"
	require.Equal(t, want, stdout.String())
}

func TestPlanCommandPrintsActionableBlockedPlan(t *testing.T) {
	clearBootstrapEnv(t)
	server := missingTableCommandServer(t, "app")
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  logs:
    table: app.logs
    mapping:
      body: log.body
`)
	var stdout bytes.Buffer

	err := runPlanCommand(context.Background(), []string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--format", "human",
		"--sample", "logs=-",
		configPath,
	}, strings.NewReader(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"kvlistValue":{"values":[{"key":"request_id","value":{"stringValue":"42"}}]}}}]}]}]}`), &stdout, &bytes.Buffer{})
	require.Error(t, err)

	for _, expected := range []string{
		"table app.logs [logs]: blocked",
		"output type is runtime-dependent",
		"logs.body <- log.body",
		"coverage=1/1",
		"suggested edit for signals.logs.mapping.body:",
		"cast: object",
		"resolve blocked mappings or table conflicts",
	} {
		require.Contains(t, stdout.String(), expected)
	}
}

func TestPlanCommandWritesScopeQLWithoutASecondCatalogRead(t *testing.T) {
	clearBootstrapEnv(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/v1/catalog/databases/scopedb/schemas/app" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"1"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "app", "comment": nil})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  logs:
    table: app.logs
    mapping:
      message: log.message
`)
	outputPath := filepath.Join(t.TempDir(), "tables.scopeql")
	var stdout bytes.Buffer

	err := runPlanCommand(context.Background(), []string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--out", outputPath,
		configPath,
	}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	require.NoError(t, err)

	contents, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, "CREATE TABLE `app`.`logs` (\n    `message` string\n);\n", string(contents))
	require.Contains(t, stdout.String(), "scopeql output: "+outputPath)
	require.Equal(t, 2, requests)
}

func TestPlanCommandDoesNotOverwriteOutputWhenBlocked(t *testing.T) {
	clearBootstrapEnv(t)
	server := missingTableCommandServer(t, "app")
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  logs:
    table: app.logs
    mapping:
      body: log.body
`)
	outputPath := filepath.Join(t.TempDir(), "tables.scopeql")
	require.NoError(t, os.WriteFile(outputPath, []byte("existing\n"), 0o600))

	err := runPlanCommand(context.Background(), []string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--out", outputPath,
		configPath,
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	require.Error(t, err)

	contents, readErr := os.ReadFile(outputPath)
	require.NoError(t, readErr)
	require.Equal(t, "existing\n", string(contents))
}

func TestPlanCommandRendersVersionedJSON(t *testing.T) {
	clearBootstrapEnv(t)
	server := missingTableCommandServer(t, "app")
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  traces:
    table: app.spans
    mapping:
      trace_id: span.trace_id
`)
	var stdout bytes.Buffer

	err := runPlanCommand(context.Background(), []string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--format", "json",
		configPath,
	}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	require.NoError(t, err)

	var plan scopedbexporter.IngestionTablePlan
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &plan))
	require.Equal(t, scopedbexporter.TablePlanVersion, plan.Version)
	require.Len(t, plan.Tables, 1)
	require.Equal(t, scopedbexporter.TableActionCreate, plan.Tables[0].Action)
}

func TestPlanCommandShowsMissingNamespaceAndPhysicalPolicy(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  logs:
    table: analytics.otel.logs
    mapping:
      message: log.message
`)
	outputPath := filepath.Join(t.TempDir(), "tables.scopeql")
	var stdout bytes.Buffer

	err := runPlanCommand(context.Background(), []string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--out", outputPath,
		configPath,
	}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	require.NoError(t, err)

	for _, expected := range []string{
		"namespace: create database analytics",
		"namespace: create schema analytics.otel",
		"physical policy: unspecified; review retention, clustering, distinct keys, and indexes",
	} {
		require.Contains(t, stdout.String(), expected)
	}
	contents, readErr := os.ReadFile(outputPath)
	require.NoError(t, readErr)

	if !strings.HasPrefix(string(contents), "CREATE DATABASE `analytics`;\nCREATE SCHEMA `analytics`.`otel`;\nCREATE TABLE") {
		t.Fatalf("unexpected ScopeQL output: %s", contents)
	}
}

func writeTablePlanConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))

	return path
}

func missingTableCommandServer(t *testing.T, schema string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		schemaPath := "/v1/catalog/databases/scopedb/schemas/" + schema
		switch {
		case r.URL.Path == schemaPath:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"1"`)
			if err := json.NewEncoder(w).Encode(map[string]any{"name": schema, "comment": nil}); err != nil {
				t.Errorf("encode schema: %v", err)
			}
		case strings.HasPrefix(r.URL.Path, schemaPath+"/tables/"):
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected catalog request: %s", r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
}
