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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/exporter/exportertest"
)

func TestClientValidateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/catalog/databases/scopedb/schemas/public/tables/vendor_otel_logs_test", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"1"`)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name":         "vendor_otel_logs_test",
			"columns":      []map[string]any{{"name": "message", "data_type": "string"}},
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct":     map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{"message": "log.message"})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	_, validationErr := client.inspectDestination(context.Background(), signalLogs)

	require.NoError(t, validationErr)
	validation, err := client.inspectDestination(context.Background(), signalLogs)
	require.NoError(t, err)
	require.Len(t, validation.Columns, 1)
	assert.Equal(t, "string", validation.Columns[0].TargetType)
	assert.Equal(t, MappingCompatible, validation.Columns[0].Compatibility)
	cfg.Mappings.Logs["service"] = MappingRule{Source: `resource.attributes["service.name"]`}
	client, err = newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()
	_, err = client.inspectDestination(context.Background(), signalLogs)
	require.Error(t, err)
	assert.ErrorContains(t, err, "missing mapped columns: service")
}

func TestClientValidateDestinationRejectsKnownTypeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"1"`)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name": "vendor_otel_logs_test",
			"columns": []map[string]any{
				{"name": "severity", "data_type": "string"},
				{"name": "tenant", "data_type": "string"},
			},
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct":     map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = shorthandMapping(map[string]string{
		"severity": "log.severity_number",
		"tenant":   `resource.attributes["tenant.id"]`,
	})
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	validation, err := client.inspectDestination(context.Background(), signalLogs)
	require.Error(t, err)
	assert.ErrorContains(t, err, "severity (log.severity_number produces int, table has string)")
	assert.NotContains(t, err.Error(), "tenant")
	require.Len(t, validation.Columns, 2)
	assert.Equal(t, MappingIncompatible, validation.Columns[0].Compatibility)
	assert.Equal(t, MappingRuntimeDependent, validation.Columns[1].Compatibility)
}

func TestClientValidateDestinationUsesCastOutputType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTableDescription(t, w, "vendor_otel_logs_test", []string{"severity"})
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.Mappings.Logs = MappingConfig{
		"severity": {Source: "log.severity_number", Cast: "string"},
	}
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()

	validation, err := client.inspectDestination(context.Background(), signalLogs)
	require.NoError(t, err)
	require.Len(t, validation.Columns, 1)
	assert.Equal(t, MappingCompatible, validation.Columns[0].Compatibility)
	assert.Equal(t, "string", validation.Columns[0].OutputType)
	assert.False(t, validation.Columns[0].RuntimeDependent)
}

func testClientConfig(endpoint string) *Config {
	cfg := validTestConfig()
	cfg.Endpoint = endpoint
	cfg.Tables = TableRoutingConfig{
		Logs:    "public.vendor_otel_logs_test",
		Traces:  "public.vendor_otel_traces_test",
		Metrics: "public.vendor_otel_metrics_test",
	}
	cfg.Timeout.Timeout = 0
	return cfg
}

func decodeSDKRequestBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	switch r.Header.Get("Content-Encoding") {
	case "":
		return body, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	case "zstd":
		reader, err := zstd.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	default:
		return nil, errors.New("unsupported content encoding")
	}
}
