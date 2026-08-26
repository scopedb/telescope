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
	"strings"
	"testing"
)

func TestResolveDaemonConfigUsesFullCollectorConfig(t *testing.T) {
	clearIngestionEnv(t)
	t.Setenv("TELESCOPE_COLLECTOR_CONFIG", "file:/from-env.yaml")

	got, err := resolveDaemonConfig("file:/from-flag.yaml", true, "", false, "", false)
	if err != nil {
		t.Fatalf("resolveDaemonConfig() error = %v", err)
	}
	if got != "file:/from-flag.yaml" {
		t.Fatalf("resolveDaemonConfig() = %q", got)
	}
}

func TestResolveDaemonConfigRequiresExplicitIngestionChoice(t *testing.T) {
	clearIngestionEnv(t)

	_, err := resolveDaemonConfig("", false, "", false, "", false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveDaemonConfigRendersStarterProfile(t *testing.T) {
	clearIngestionEnv(t)
	t.Setenv("TELESCOPE_COLLECTOR_CONFIG", "file:/stale-env.yaml")

	got, err := resolveDaemonConfig("", false, "", false, "starter", true)
	if err != nil {
		t.Fatalf("resolveDaemonConfig() error = %v", err)
	}
	if !strings.HasPrefix(got, "yaml:") || !strings.Contains(got, "record_timestamp: log.timestamp") {
		t.Fatalf("resolveDaemonConfig() did not render starter mappings: %q", got)
	}
}

func TestResolveDaemonConfigRejectsMixedConfigModes(t *testing.T) {
	clearIngestionEnv(t)

	_, err := resolveDaemonConfig("file:/collector.yaml", true, "", false, "starter", true)
	if err == nil {
		t.Fatal("expected error")
	}
}

func clearIngestionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELESCOPE_COLLECTOR_CONFIG", "")
	t.Setenv("TELESCOPE_INGESTION_CONFIG", "")
	t.Setenv("TELESCOPE_INGESTION_PROFILE", "")
}
