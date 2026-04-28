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

package appruntime

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantAddr string
		wantTime time.Duration
	}{
		{
			name:     "defaults listen addr and timeout",
			env:      map[string]string{},
			wantAddr: ":8080",
			wantTime: 15 * time.Second,
		},
		{
			name: "uses port when listen addr is unset",
			env: map[string]string{
				"TELESCOPE_PORT": "18080",
			},
			wantAddr: ":18080",
			wantTime: 15 * time.Second,
		},
		{
			name: "listen addr takes precedence over port",
			env: map[string]string{
				"TELESCOPE_HTTP_ADDR": "127.0.0.1:9090",
				"TELESCOPE_PORT":      "18080",
			},
			wantAddr: "127.0.0.1:9090",
			wantTime: 15 * time.Second,
		},
		{
			name: "parses query timeout",
			env: map[string]string{
				"TELESCOPE_QUERY_TIMEOUT": "45s",
			},
			wantAddr: ":8080",
			wantTime: 45 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			cfg, err := LoadConfigFromEnv()
			if err != nil {
				t.Fatalf("LoadConfigFromEnv() error = %v", err)
			}

			if cfg.ListenAddr != tt.wantAddr {
				t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, tt.wantAddr)
			}
			if cfg.ScopeDBEndpoint != "https://example.scopedb.cloud" {
				t.Fatalf("ScopeDBEndpoint = %q", cfg.ScopeDBEndpoint)
			}
			if cfg.ScopeDBAPIKey != "sk_test" {
				t.Fatalf("ScopeDBAPIKey = %q", cfg.ScopeDBAPIKey)
			}
			if cfg.QueryTimeout != tt.wantTime {
				t.Fatalf("QueryTimeout = %v, want %v", cfg.QueryTimeout, tt.wantTime)
			}
		})
	}
}

func TestLoadConfigFromEnvErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name: "requires endpoint",
			env: map[string]string{
				"TELESCOPE_SCOPEDB_ENDPOINT": "",
				"TELESCOPE_SCOPEDB_API_KEY":  "sk_test",
			},
			wantErr: "TELESCOPE_SCOPEDB_ENDPOINT is required",
		},
		{
			name: "requires api key",
			env: map[string]string{
				"TELESCOPE_SCOPEDB_ENDPOINT": "https://example.scopedb.cloud",
				"TELESCOPE_SCOPEDB_API_KEY":  "",
			},
			wantErr: "TELESCOPE_SCOPEDB_API_KEY is required",
		},
		{
			name: "rejects invalid query timeout",
			env: map[string]string{
				"TELESCOPE_SCOPEDB_ENDPOINT": "https://example.scopedb.cloud",
				"TELESCOPE_SCOPEDB_API_KEY":  "sk_test",
				"TELESCOPE_QUERY_TIMEOUT":    "not-a-duration",
			},
			wantErr: "parse TELESCOPE_QUERY_TIMEOUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			_, err := LoadConfigFromEnv()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func setBaseEnv(t *testing.T) {
	t.Helper()
	clearConfigEnv(t)
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://example.scopedb.cloud")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "sk_test")
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELESCOPE_HTTP_ADDR", "")
	t.Setenv("TELESCOPE_PORT", "")
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "")
	t.Setenv("TELESCOPE_QUERY_TIMEOUT", "")
}
