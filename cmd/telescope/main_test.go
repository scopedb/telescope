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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrintUsageSeparatesCommandRoles(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)

	for _, expected := range []string{
		"Setup:",
		"Operations:",
		"Diagnostics:",
		"Discover mapping selectors in an OTLP sample",
		"Capture OTLP samples for mapping preview",
		"Preview sample projection without appending",
		"Plan additive ScopeDB table DDL",
		"Run the OTLP-to-ScopeDB data plane",
		"Report local delivery state",
		"Verify synthetic OTLP-to-append delivery",
		"Execute one ScopeQL statement",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("printUsage() output does not contain %q", expected)
		}
	}
}

func TestLoadEnvFileSetsMissingValues(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, `
# bootstrap config
TELESCOPE_SCOPEDB_ENDPOINT="https://file.scopedb.cloud"
TELESCOPE_SCOPEDB_API_KEY='sk_file'
`)

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("loadEnvFile() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://file.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_file" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestLoadEnvFileDoesNotOverrideExistingEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://env.scopedb.cloud")
	path := writeEnvFile(t, `
TELESCOPE_SCOPEDB_ENDPOINT=https://file.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_file
`)

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("loadEnvFile() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://env.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_file" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsOverrideEnvFile(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, `
TELESCOPE_SCOPEDB_ENDPOINT=https://file.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_file
`)
	endpoint := "https://flag.scopedb.cloud"

	err := applyBootstrapFlags(bootstrapFlags{
		envFile:         &path,
		scopedbEndpoint: &endpoint,
	})
	if err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://flag.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_file" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsUsesSharedScopeDBEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, `
SCOPEDB_ENDPOINT=https://shared.scopedb.cloud
SCOPEDB_API_KEY=sk_shared
`)

	if err := applyBootstrapFlags(bootstrapFlags{envFile: &path}); err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://shared.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_shared" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsPrefersTelescopeEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	t.Setenv("SCOPEDB_ENDPOINT", "https://shared.scopedb.cloud")
	t.Setenv("SCOPEDB_API_KEY", "sk_shared")
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://telescope.scopedb.cloud")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "sk_telescope")

	if err := applyBootstrapFlags(bootstrapFlags{}); err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://telescope.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_telescope" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsPreferFlagsOverEveryEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	t.Setenv("SCOPEDB_ENDPOINT", "https://shared.scopedb.cloud")
	t.Setenv("SCOPEDB_API_KEY", "sk_shared")
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://telescope.scopedb.cloud")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "sk_telescope")
	endpoint := "https://flag.scopedb.cloud"
	apiKey := "sk_flag"

	if err := applyBootstrapFlags(bootstrapFlags{scopedbEndpoint: &endpoint, scopedbAPIKey: &apiKey}); err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != endpoint {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != apiKey {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestLoadEnvFileRejectsInvalidLine(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, "TELESCOPE_SCOPEDB_ENDPOINT\n")

	if err := loadEnvFile(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadEnvFileRejectsInvalidEnvironmentValue(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, "TELESCOPE_SCOPEDB_ENDPOINT=https://bad\x00value\n")

	if err := loadEnvFile(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestSetEnvIfValueRejectsInvalidEnvironmentValue(t *testing.T) {
	if err := setEnvIfValue("TELESCOPE_SCOPEDB_ENDPOINT", "https://bad\x00value"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveHTTPListenAddr(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		addr     string
		expected string
	}{
		{name: "default", expected: ":8080"},
		{name: "environment", addr: "127.0.0.1:9090", expected: "127.0.0.1:9090"},
		{name: "flag before environment", flag: "127.0.0.1:7070", addr: "127.0.0.1:9090", expected: "127.0.0.1:7070"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TELESCOPE_HTTP_ADDR", tt.addr)
			if got := resolveHTTPListenAddr(tt.flag); got != tt.expected {
				t.Fatalf("resolveHTTPListenAddr() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestReportReadiness(t *testing.T) {
	t.Setenv("TELESCOPE_OTLP_GRPC_ADDR", "127.0.0.1:14317")
	t.Setenv("TELESCOPE_OTLP_HTTP_ADDR", "127.0.0.1:14318")
	checks := 0
	var output bytes.Buffer

	reportReadiness(context.Background(), func(context.Context) bool {
		checks++
		return checks == 2
	}, &output, time.Millisecond, "127.0.0.1:18080")

	want := "telescope ready: otlp_grpc=127.0.0.1:14317 otlp_http=127.0.0.1:14318 http=127.0.0.1:18080\n"
	if output.String() != want {
		t.Fatalf("reportReadiness() output = %q, want %q", output.String(), want)
	}
}

func TestReportReadinessStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer

	reportReadiness(ctx, func(context.Context) bool { return true }, &output, time.Millisecond, "127.0.0.1:18080")

	if output.Len() != 0 {
		t.Fatalf("reportReadiness() output = %q, want none", output.String())
	}
}

func clearBootstrapEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "")
	t.Setenv("SCOPEDB_ENDPOINT", "")
	t.Setenv("SCOPEDB_API_KEY", "")
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}
