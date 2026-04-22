package vendordbexporter

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestClientSendGzipAndBearer(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")
	cfg.Compression = "gzip"
	cfg.Dataset = "demo"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, defaultPath, r.URL.Path)
		assert.Equal(t, "Bearer demo-key", r.Header.Get("Authorization"))
		assert.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
		assert.Equal(t, "demo", r.Header.Get("X-Vendor-Dataset"))

		reader, err := gzip.NewReader(r.Body)
		require.NoError(t, err)
		defer reader.Close()

		body, err := io.ReadAll(reader)
		require.NoError(t, err)

		var request scopeDBIngestRequest
		require.NoError(t, json.Unmarshal(body, &request))
		assert.Equal(t, "committed", request.Type)
		assert.Equal(t, "json", request.Data.Format)
		assert.Contains(t, request.Statement, "INSERT INTO "+cfg.Table)

		lines := strings.Split(strings.TrimSpace(request.Data.Rows), "\n")
		require.Len(t, lines, 1)

		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &row))
		assert.Equal(t, signalLogs, row["signal"])
		assert.Equal(t, "v1", row["schema_version"])
		assert.Equal(t, "demo", row["dataset"])
		assert.Equal(t, map[string]any{"body": "hello"}, row["record"])

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg.Endpoint = server.URL

	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)

	err = client.Send(context.Background(), signalLogs, &IngestPayload{
		SchemaVersion: "v1",
		Dataset:       "demo",
		Records:       []Record{{"body": "hello"}},
	})
	require.NoError(t, err)
}

func TestClientEnsureTable(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer demo-key", r.Header.Get("Authorization"))

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/statements":
			assert.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
			reader, err := gzip.NewReader(r.Body)
			require.NoError(t, err)
			defer reader.Close()

			var request tableInitStatementRequest
			require.NoError(t, json.NewDecoder(reader).Decode(&request))
			assert.Equal(t, "json", request.Format)
			assert.Contains(t, request.Statement, "CREATE TABLE IF NOT EXISTS "+cfg.Table)
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

	err = client.EnsureTable(context.Background())
	require.NoError(t, err)
}

func TestClientEnsureTableFailedStatement(t *testing.T) {
	cfg := testClientConfig("http://example.invalid")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/statements", r.URL.Path)
		assert.Equal(t, "gzip", r.Header.Get("Content-Encoding"))
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

	err = client.EnsureTable(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "permission denied")
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
	cfg.APIKey = configopaque.String("demo-key")
	cfg.Dataset = "demo"
	cfg.Table = "public.vendor_otel_raw_test"
	cfg.Timeout.Timeout = 0
	return cfg
}

func TestNewClientUsesNopSettings(t *testing.T) {
	cfg := testClientConfig("http://localhost:8080")
	settings := exportertest.NewNopSettings(typeStr)
	settings.TelemetrySettings = componenttest.NewNopTelemetrySettings()

	client, err := NewClient(cfg, settings)
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.NotNil(t, client.logger)
	assert.NotNil(t, client.httpClient)
	assert.Equal(t, component.NewID(typeStr).Type(), settings.ID.Type())
}
