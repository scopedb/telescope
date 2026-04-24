package scopedbexporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exportertest"
)

type tableInitStatementRequest struct {
	Statement string `json:"statement"`
	Format    string `json:"format"`
}

type tableInitResultSetMetadata struct {
	Fields  []map[string]any `json:"fields"`
	NumRows uint64           `json:"num_rows"`
}

type tableInitResultSet struct {
	Metadata *tableInitResultSetMetadata `json:"metadata,omitempty"`
	Format   string                      `json:"format,omitempty"`
	Rows     json.RawMessage             `json:"rows,omitempty"`
}

type tableInitStatementResponse struct {
	StatementID string              `json:"statement_id"`
	Status      string              `json:"status"`
	Message     string              `json:"message,omitempty"`
	ResultSet   *tableInitResultSet `json:"result_set,omitempty"`
}

func TestClientSendZstdByDefaultAndBearer(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	cfg.Dataset = "demo"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, defaultPath, r.URL.Path)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		assert.Equal(t, "zstd", r.Header.Get("Content-Encoding"))
		assert.Equal(t, "demo", r.Header.Get("X-Vendor-Dataset"))
		assert.NotEmpty(t, r.Header.Get("X-ScopeDB-Uncompressed-Content-Length"))
		body, err := decodeCompressedRequestBody(r)
		require.NoError(t, err)

		var request scopeDBIngestRequest
		require.NoError(t, json.Unmarshal(body, &request))
		assert.Equal(t, "committed", request.Type)
		assert.Equal(t, "json", request.Data.Format)
		assert.Contains(t, request.Statement, "INSERT INTO `public`.`vendor_otel_logs_test`")
		assert.Contains(t, request.Statement, "record_timestamp")
		assert.Contains(t, request.Statement, "observed_timestamp")
		assert.Contains(t, request.Statement, "severity_text")
		assert.Contains(t, request.Statement, "message")
		assert.NotContains(t, request.Statement, "metric_name")

		lines := strings.Split(strings.TrimSpace(request.Data.Rows), "\n")
		require.Len(t, lines, 1)

		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &row))
		assert.Equal(t, signalLogs, row["signal"])
		assert.Equal(t, "v1", row["schema_version"])
		assert.Equal(t, "demo", row["dataset"])
		assert.NotEmpty(t, row["row_id"])
		assert.Equal(t, "2024-04-23T01:23:45.123456789Z", row["record_timestamp"])
		assert.Equal(t, "2024-04-23T01:23:46.123456789Z", row["observed_timestamp"])
		assert.Equal(t, map[string]any{
			"body":                         "hello",
			"timestamp_unix_nano":          "1713835425123456789",
			"observed_timestamp_unix_nano": "1713835426123456789",
		}, row["record"])

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.Send(context.Background(), signalLogs, &IngestPayload{
		SchemaVersion: "v1",
		Dataset:       "demo",
		Records: []Record{{
			"body":                         "hello",
			"timestamp_unix_nano":          "1713835425123456789",
			"observed_timestamp_unix_nano": "1713835426123456789",
		}},
	})
	require.NoError(t, err)
}

func TestClientSendGzipAndBearer(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	cfg.Compression = "gzip"
	cfg.Dataset = "demo"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, defaultPath, r.URL.Path)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		assert.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
		assert.Equal(t, "demo", r.Header.Get("X-Vendor-Dataset"))
		assert.NotEmpty(t, r.Header.Get("X-ScopeDB-Uncompressed-Content-Length"))

		body, err := decodeCompressedRequestBody(r)
		require.NoError(t, err)

		var request scopeDBIngestRequest
		require.NoError(t, json.Unmarshal(body, &request))
		assert.Equal(t, "committed", request.Type)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.Send(context.Background(), signalLogs, &IngestPayload{
		SchemaVersion: "v1",
		Dataset:       "demo",
		Records: []Record{{
			"body":                         "hello",
			"timestamp_unix_nano":          "1713835425123456789",
			"observed_timestamp_unix_nano": "1713835426123456789",
		}},
	})
	require.NoError(t, err)
}

func TestClientEnsureTable(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	ref, err := parseTableRef(cfg.Tables.Logs)
	require.NoError(t, err)
	expectedStatements := []string{
		"CREATE SCHEMA IF NOT EXISTS `public`",
		"CREATE TABLE IF NOT EXISTS `public`.`vendor_otel_logs_test` (\n  ingest_ts timestamp,\n  record_timestamp timestamp,\n  observed_timestamp timestamp,\n  schema_version string,\n  dataset string,\n  row_id string,\n  service_name string,\n  instance_id string,\n  pod_name string,\n  host_ip string,\n  host_name string,\n  trace_id string,\n  span_id string,\n  severity_text string,\n  message string,\n  record object\n)",
	}
	var statements []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/statements":
			var request tableInitStatementRequest
			body, err := decodeCompressedRequestBody(r)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &request))
			assert.Equal(t, "json", request.Format)
			statements = append(statements, request.Statement)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(tableInitStatementResponse{
				StatementID: "11111111-1111-1111-1111-111111111111",
				Status:      "finished",
				ResultSet: &tableInitResultSet{
					Metadata: &tableInitResultSetMetadata{
						Fields:  []map[string]any{},
						NumRows: 0,
					},
					Format: "json",
					Rows:   json.RawMessage(`[]`),
				},
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.EnsureTable(context.Background(), signalLogs, ref)
	require.NoError(t, err)
	assert.Equal(t, expectedStatements, statements)
}

func TestClientEnsureTableCreatesDatabaseSchemaAndTable(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	cfg.Tables.Logs = "scopedb.otel.logs"
	ref, err := parseTableRef(cfg.Tables.Logs)
	require.NoError(t, err)

	expectedStatements := []string{
		"CREATE DATABASE IF NOT EXISTS `scopedb`",
		"CREATE SCHEMA IF NOT EXISTS `scopedb`.`otel`",
		"CREATE TABLE IF NOT EXISTS `scopedb`.`otel`.`logs` (\n  ingest_ts timestamp,\n  record_timestamp timestamp,\n  observed_timestamp timestamp,\n  schema_version string,\n  dataset string,\n  row_id string,\n  service_name string,\n  instance_id string,\n  pod_name string,\n  host_ip string,\n  host_name string,\n  trace_id string,\n  span_id string,\n  severity_text string,\n  message string,\n  record object\n)",
	}
	var statements []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/statements", r.URL.Path)

		var request tableInitStatementRequest
		body, err := decodeCompressedRequestBody(r)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &request))
		statements = append(statements, request.Statement)

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(tableInitStatementResponse{
			StatementID: "44444444-4444-4444-4444-444444444444",
			Status:      "finished",
			ResultSet: &tableInitResultSet{
				Metadata: &tableInitResultSetMetadata{
					Fields:  []map[string]any{},
					NumRows: 0,
				},
				Format: "json",
				Rows:   json.RawMessage(`[]`),
			},
		}))
	}))
	defer server.Close()

	cfg.Endpoint = server.URL
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.EnsureTable(context.Background(), signalLogs, ref)
	require.NoError(t, err)
	assert.Equal(t, expectedStatements, statements)
}

func TestClientEnsureTableFailedStatement(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	ref, err := parseTableRef(cfg.Tables.Logs)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/statements", r.URL.Path)
		assert.Equal(t, "zstd", r.Header.Get("Content-Encoding"))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(tableInitStatementResponse{
			StatementID: "22222222-2222-2222-2222-222222222222",
			Status:      "failed",
			Message:     "permission denied",
		}))
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.EnsureTable(context.Background(), signalLogs, ref)
	require.Error(t, err)
	assert.ErrorContains(t, err, "permission denied")
}

func TestClientSendUsesSignalSpecificTable(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	cfg.Tables.Logs = "public.vendor_otel_logs_test"
	cfg.Compression = "none"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request scopeDBIngestRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		assert.Contains(t, request.Statement, "INSERT INTO `public`.`vendor_otel_logs_test`")
		assert.Contains(t, request.Statement, "severity_text")
		assert.Contains(t, request.Statement, "message")
		assert.NotContains(t, request.Statement, "parent_span_id")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.Send(context.Background(), signalLogs, &IngestPayload{
		SchemaVersion: "v1",
		Dataset:       cfg.Dataset,
		Records:       []Record{{"body": "hello"}},
	})
	require.NoError(t, err)
}

func TestClientSendUsesTraceSchema(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	cfg.Compression = "none"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request scopeDBIngestRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		assert.Contains(t, request.Statement, "INSERT INTO `public`.`vendor_otel_traces_test`")
		assert.Contains(t, request.Statement, "span_name")
		assert.Contains(t, request.Statement, "span_kind")
		assert.Contains(t, request.Statement, "status_code")
		assert.Contains(t, request.Statement, "duration_ns")
		assert.NotContains(t, request.Statement, "severity_text")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.Send(context.Background(), signalTraces, &IngestPayload{
		SchemaVersion: "v1",
		Dataset:       cfg.Dataset,
		Records: []Record{{
			"name":                 "GET /checkout",
			"kind":                 "server",
			"status_code":          "error",
			"start_time_unix_nano": "1713835425123456789",
			"end_time_unix_nano":   "1713835426123456789",
		}},
	})
	require.NoError(t, err)
}

func TestClientSendUsesMetricSchema(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	cfg.Compression = "none"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request scopeDBIngestRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		assert.Contains(t, request.Statement, "INSERT INTO `public`.`vendor_otel_metrics_test`")
		assert.Contains(t, request.Statement, "metric_type")
		assert.Contains(t, request.Statement, "temporality")
		assert.Contains(t, request.Statement, "unit")
		assert.Contains(t, request.Statement, "number_value")
		assert.Contains(t, request.Statement, "distribution")
		assert.NotContains(t, request.Statement, "span_name")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.Send(context.Background(), signalMetrics, &IngestPayload{
		SchemaVersion: "v1",
		Dataset:       cfg.Dataset,
		Records: []Record{{
			"metric_name":               "cpu.utilization",
			"type":                      "gauge",
			"unit":                      "1",
			"temporality":               "delta",
			"timestamp_unix_nano":       "1713835425123456789",
			"start_timestamp_unix_nano": "1713835424123456789",
		}},
	})
	require.NoError(t, err)
}

func TestClientSendRetryableStatuses(t *testing.T) {
	for _, statusCode := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "temporary", statusCode)
			}))
			defer server.Close()

			cfg := testClientConfig(server.URL)
			client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
			require.NoError(t, err)

			err = client.Send(context.Background(), signalLogs, &IngestPayload{
				SchemaVersion: "v1",
				Dataset:       cfg.Dataset,
				Records:       []Record{{"body": "hello"}},
			})
			require.Error(t, err)
			assert.False(t, consumererror.IsPermanent(err))
		})
	}
}

func TestClientSendPermanentStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.Send(context.Background(), signalLogs, &IngestPayload{
		SchemaVersion: "v1",
		Dataset:       cfg.Dataset,
		Records:       []Record{{"body": "hello"}},
	})
	require.Error(t, err)
	assert.True(t, consumererror.IsPermanent(err))
}

func testClientConfig(endpoint string) *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = endpoint
	cfg.APIKey = configopaque.String("test-api-key")
	cfg.Dataset = "demo"
	cfg.Tables = TableRoutingConfig{
		Logs:    "public.vendor_otel_logs_test",
		Traces:  "public.vendor_otel_traces_test",
		Metrics: "public.vendor_otel_metrics_test",
	}
	cfg.Timeout.Timeout = 0
	return cfg
}

func TestNewClientUsesNopSettings(t *testing.T) {
	cfg := testClientConfig("https://scopedb.invalid")
	settings := exportertest.NewNopSettings(typeStr)
	settings.TelemetrySettings = componenttest.NewNopTelemetrySettings()

	client, err := NewClient(cfg, settings)
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NotNil(t, client.logger)
	assert.NotNil(t, client.httpClient)
	assert.Equal(t, component.NewID(typeStr).Type(), settings.ID.Type())
}

func decodeCompressedRequestBody(r *http.Request) ([]byte, error) {
	compressedBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "":
		return compressedBody, nil
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(compressedBody))
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		return io.ReadAll(gr)
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(compressedBody))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	default:
		return nil, io.ErrUnexpectedEOF
	}
}
