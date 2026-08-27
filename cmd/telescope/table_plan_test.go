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
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

func TestPlanCommandRendersScopeQLForMissingTable(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
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

	err := runPlanCommand([]string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--format", "scopeql",
		configPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runPlanCommand() error = %v", err)
	}
	want := "CREATE TABLE `app`.`logs` (\n    `message` string,\n    `ts` timestamp\n);\n"
	if got := stdout.String(); got != want {
		t.Fatalf("scopeql output = %q, want %q", got, want)
	}
}

func TestPlanCommandPrintsActionableBlockedPlan(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  logs:
    table: app.logs
    mapping:
      body: log.body
`)
	var stdout bytes.Buffer

	err := runPlanCommand([]string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--format", "human",
		"--sample", "logs=-",
		configPath,
	}, strings.NewReader(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"kvlistValue":{"values":[{"key":"request_id","value":{"stringValue":"42"}}]}}}]}]}]}`), &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected blocked plan error")
	}
	for _, expected := range []string{
		"table app.logs [logs]: blocked",
		"output type is runtime-dependent",
		"logs.body <- log.body",
		"coverage=1/1",
		"suggested edit for signals.logs.mapping.body:",
		"cast: object",
		"resolve blocked mappings or table conflicts",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("plan output missing %q: %s", expected, stdout.String())
		}
	}
}

func TestPlanCommandWritesScopeQLWithoutASecondCatalogRead(t *testing.T) {
	clearBootstrapEnv(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
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

	err := runPlanCommand([]string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--out", outputPath,
		configPath,
	}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("runPlanCommand() error = %v", err)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read ScopeQL output: %v", err)
	}
	if got, want := string(contents), "CREATE TABLE `app`.`logs` (\n    `message` string\n);\n"; got != want {
		t.Fatalf("ScopeQL file = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), "scopeql output: "+outputPath) || !strings.Contains(stdout.String(), "scopeql run -f "+outputPath) {
		t.Fatalf("plan output does not identify written ScopeQL: %s", stdout.String())
	}
	if requests != 1 {
		t.Fatalf("catalog requests = %d, want 1", requests)
	}
}

func TestPlanCommandDoesNotOverwriteOutputWhenBlocked(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  logs:
    table: app.logs
    mapping:
      body: log.body
`)
	outputPath := filepath.Join(t.TempDir(), "tables.scopeql")
	if err := os.WriteFile(outputPath, []byte("existing\n"), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	err := runPlanCommand([]string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--out", outputPath,
		configPath,
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected blocked plan error")
	}
	contents, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read existing output: %v", readErr)
	}
	if got := string(contents); got != "existing\n" {
		t.Fatalf("blocked plan overwrote output: %q", got)
	}
}

func TestPlanCommandRejectsOutWithMachineFormat(t *testing.T) {
	err := runPlanCommand(
		[]string{"--format", "scopeql", "--out", "tables.scopeql"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "--out requires --format human") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanCommandRendersVersionedJSON(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	configPath := writeTablePlanConfig(t, `
signals:
  traces:
    table: app.spans
    mapping:
      trace_id: span.trace_id
`)
	var stdout bytes.Buffer

	err := runPlanCommand([]string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "test-key",
		"--format", "json",
		configPath,
	}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("runPlanCommand() error = %v", err)
	}
	var plan scopedbexporter.IngestionTablePlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatalf("decode JSON plan: %v\n%s", err, stdout.String())
	}
	if plan.Version != scopedbexporter.TablePlanVersion {
		t.Fatalf("plan version = %d", plan.Version)
	}
	if len(plan.Tables) != 1 || plan.Tables[0].Action != scopedbexporter.TableActionCreate {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestPlanCommandRejectsUnsupportedFormat(t *testing.T) {
	err := runPlanCommand(
		[]string{"--format", "yaml"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported plan format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlanHelpShowsContractAndScopeQLFormats(t *testing.T) {
	var stderr bytes.Buffer
	err := runPlanCommand(
		[]string{"--help"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
	)
	if err != flag.ErrHelp {
		t.Fatalf("runPlanCommand() error = %v, want %v", err, flag.ErrHelp)
	}
	for _, expected := range []string{
		"Usage: telescope plan [options] [telescope.yaml]",
		"human, json, or scopeql",
		"write additive ScopeQL",
		"scopeql run -f",
	} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("plan help missing %q: %s", expected, stderr.String())
		}
	}
}

func writeTablePlanConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
