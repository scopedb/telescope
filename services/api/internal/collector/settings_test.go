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

package collector

import (
	"os"
	"strings"
	"testing"
)

func TestSettingsConfigURI(t *testing.T) {
	tests := []struct {
		name      string
		configURI string
		wantURI   string
		wantYAML  bool
	}{
		{
			name:     "empty config uses embedded yaml",
			wantURI:  "yaml:",
			wantYAML: true,
		},
		{
			name:      "bare path becomes file uri",
			configURI: "/etc/telescope/collector.yaml",
			wantURI:   "file:/etc/telescope/collector.yaml",
		},
		{
			name:      "http uri is preserved",
			configURI: "http://127.0.0.1:8080/collector.yaml",
			wantURI:   "http://127.0.0.1:8080/collector.yaml",
		},
		{
			name:      "yaml uri is preserved",
			configURI: "yaml:receivers: {}",
			wantURI:   "yaml:receivers: {}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := Settings(tt.configURI, "test")
			uris := settings.ConfigProviderSettings.ResolverSettings.URIs
			if len(uris) != 1 {
				t.Fatalf("expected one config URI, got %d", len(uris))
			}
			if !strings.HasPrefix(uris[0], tt.wantURI) {
				t.Fatalf("expected URI prefix %q, got %q", tt.wantURI, uris[0])
			}
			if tt.wantYAML && !strings.Contains(uris[0], "exporters:") {
				t.Fatalf("expected embedded collector config, got %q", uris[0])
			}
		})
	}
}

func TestSettingsGracefulShutdownMode(t *testing.T) {
	standalone := Settings("", "test")
	if standalone.DisableGracefulShutdown {
		t.Fatal("standalone collector settings should preserve graceful shutdown")
	}

	daemon := DaemonSettings("", "test")
	if !daemon.DisableGracefulShutdown {
		t.Fatal("daemon collector settings should let the daemon own shutdown")
	}
}

func TestApplyDefaultEnv(t *testing.T) {
	t.Setenv("HOME", "/tmp/telescope-home")
	t.Setenv("TELESCOPE_OTLP_GRPC_ADDR", "127.0.0.1:14317")
	t.Setenv("TELESCOPE_OTLP_HTTP_ADDR", "")
	t.Setenv("TELESCOPE_HEALTH_ADDR", "")
	t.Setenv("TELESCOPE_QUEUE_DIR", "")
	t.Setenv("TELESCOPE_ENV", "")

	Settings("", "test")

	if got := getenv(t, "TELESCOPE_OTLP_GRPC_ADDR"); got != "127.0.0.1:14317" {
		t.Fatalf("expected existing gRPC addr to be preserved, got %q", got)
	}
	if got := getenv(t, "TELESCOPE_OTLP_HTTP_ADDR"); got != "0.0.0.0:4318" {
		t.Fatalf("expected default OTLP HTTP addr, got %q", got)
	}
	if got := getenv(t, "TELESCOPE_HEALTH_ADDR"); got != "0.0.0.0:13133" {
		t.Fatalf("expected default health addr, got %q", got)
	}
	if got := getenv(t, "TELESCOPE_QUEUE_DIR"); got != "/tmp/telescope-home/.telescope/queue" {
		t.Fatalf("expected default queue dir under HOME, got %q", got)
	}
	if got := getenv(t, "TELESCOPE_ENV"); got != "default" {
		t.Fatalf("expected default telemetry env, got %q", got)
	}
}

func getenv(t *testing.T, key string) string {
	t.Helper()
	return strings.TrimSpace(os.Getenv(key))
}
