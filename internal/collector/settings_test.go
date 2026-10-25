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

package collector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettingsConfigURI(t *testing.T) {
	for input, want := range map[string][]string{
		"":                                nil,
		"/etc/telescope/collector.yaml":   {"file:/etc/telescope/collector.yaml"},
		"http://localhost/collector.yaml": {"http://localhost/collector.yaml"},
		"yaml:receivers: {}":              {"yaml:receivers: {}"},
	} {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, want, Settings(input, "test").ConfigProviderSettings.ResolverSettings.URIs)
		})
	}
}

func TestApplyDefaultEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TELESCOPE_OTLP_GRPC_ADDR", "127.0.0.1:14317")
	t.Setenv("TELESCOPE_QUEUE_DIR", "")
	t.Setenv("TELESCOPE_OTEL_BATCH_TIMEOUT", "5s")

	Settings("", "test")

	require.Equal(t, "127.0.0.1:14317", os.Getenv("TELESCOPE_OTLP_GRPC_ADDR"))
	require.Equal(t, "5s", os.Getenv("TELESCOPE_OTEL_BATCH_TIMEOUT"))
	require.Equal(t, filepath.Join(os.Getenv("HOME"), ".telescope", "queue"), os.Getenv("TELESCOPE_QUEUE_DIR"))
}
