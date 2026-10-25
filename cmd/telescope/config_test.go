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
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateReportsEffectiveConfigurationWithoutSecrets(t *testing.T) {
	t.Setenv("TELESCOPE_QUEUE_DIR", "/var/lib/telescope/override")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "private-key")
	t.Setenv("TEST_OTLP_AUTH", "private-header")
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
destination:
  api_key: ignored-private-key
delivery:
  queue:
    directory: /var/lib/telescope/configured
telemetry:
  otlp:
    headers:
      Authorization: ${env:TEST_OTLP_AUTH}
signals:
  logs: {table: logs, mapping: {message: log.message}}
`), 0o600))
	var stdout, stderr bytes.Buffer
	require.NoError(t, runConfigCommand(t.Context(), "validate", []string{"--offline", path}, strings.NewReader(""), &stdout, &stderr))
	require.Contains(t, stdout.String(), `runtime.delivery.queue.directory: "/var/lib/telescope/override" (source: TELESCOPE_QUEUE_DIR)`)
	require.Contains(t, stdout.String(), `runtime.delivery.queue.type: "file" (source: default)`)
	require.Contains(t, stdout.String(), `runtime.delivery.retry.max_elapsed_time: "0s"`)
	require.Contains(t, stdout.String(), `runtime.telemetry.otlp.headers.Authorization: "<redacted>"`)
	require.Contains(t, stderr.String(), "TELESCOPE_QUEUE_DIR overrides YAML field delivery.queue.directory")
	for _, secret := range []string{"private-key", "private-header", "ignored-private-key"} {
		require.NotContains(t, stdout.String()+stderr.String(), secret)
	}
}

func TestTelescopeConfigPath(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "default", want: defaultTelescopeConfigPath},
		{name: "explicit", args: []string{"custom.yaml"}, want: "custom.yaml"},
		{name: "too many", args: []string{"one.yaml", "two.yaml"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			require.NoError(t, flags.Parse(tt.args))

			got, err := telescopeConfigPath(flags)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestValidateAndRunRejectRetryBudgetBelowBackoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
delivery:
  retry: {max_elapsed_time: 30s}
signals:
  logs: {table: logs, mapping: {message: log.message}}
`), 0o600))
	err := runConfigCommand(t.Context(), "validate", []string{"--offline", path}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	require.ErrorContains(t, err, "'max_elapsed_time' must not be less than 'max_interval'")
	err = runTelescopeCommand(t.Context(), []string{path}, &bytes.Buffer{})
	require.ErrorContains(t, err, "'max_elapsed_time' must not be less than 'max_interval'")
}

func TestPreviewUsesStdinSampleWithoutWriting(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
signals:
  logs:
    table: app.logs
    mapping:
      message: log.message
`), 0o600))

	sample := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"}}]}]}]}`
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runConfigCommand(
		context.Background(),
		"preview",
		[]string{"--offline", "--sample", "logs=-", configPath},
		strings.NewReader(sample),
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	require.Contains(t, stdout.String(), "sample logs <- - (1 records)")
	require.Contains(t, stdout.String(), `{"message":"hello"}`)
}

func TestPreviewStrictFailsIncompleteSample(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "telescope.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
signals:
  logs:
    table: app.logs
    mapping:
      message: log.message
      tenant: resource.attributes["tenant"]
`), 0o600))

	sample := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"}}]}]}]}`
	var stdout bytes.Buffer

	err := runConfigCommand(
		context.Background(),
		"preview",
		[]string{"--offline", "--strict", "--sample", "logs=-", configPath},
		strings.NewReader(sample),
		&stdout,
		&bytes.Buffer{},
	)
	require.ErrorContains(t, err, "warnings under --strict")
	require.Contains(t, stdout.String(), "WARN unobserved")
}
