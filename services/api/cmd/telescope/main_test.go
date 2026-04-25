/*
 * Copyright 2026 ScopeDB contributors
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
	"os"
	"path/filepath"
	"testing"
)

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

func TestResolveCollectorConfigPrefersFlagOverEnvironment(t *testing.T) {
	t.Setenv("TELESCOPE_COLLECTOR_CONFIG", "file:/from-env.yaml")

	if got := resolveCollectorConfig("file:/from-flag.yaml", true); got != "file:/from-flag.yaml" {
		t.Fatalf("resolveCollectorConfig() = %q", got)
	}
}

func TestResolveCollectorConfigAllowsEmptyFlagOverride(t *testing.T) {
	t.Setenv("TELESCOPE_COLLECTOR_CONFIG", "file:/from-env.yaml")

	if got := resolveCollectorConfig("", true); got != "" {
		t.Fatalf("resolveCollectorConfig() = %q", got)
	}
}

func TestResolveCollectorConfigFallsBackToEnvironment(t *testing.T) {
	t.Setenv("TELESCOPE_COLLECTOR_CONFIG", "file:/from-env.yaml")

	if got := resolveCollectorConfig("", false); got != "file:/from-env.yaml" {
		t.Fatalf("resolveCollectorConfig() = %q", got)
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

func clearBootstrapEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "")
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}
