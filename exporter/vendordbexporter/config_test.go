package vendordbexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
)

func TestConfigValidate(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")

	require.NoError(t, cfg.Validate())
}

func TestConfigValidateEmptyEndpoint(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.APIKey = configopaque.String("demo-key")

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "endpoint is required")
}

func TestConfigValidateInvalidEndpoint(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "://bad"
	cfg.APIKey = configopaque.String("demo-key")

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "endpoint must be a valid URL")
}

func TestConfigValidateEmptyAPIKey(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "api_key is required")
}

func TestConfigValidateInvalidCompression(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")
	cfg.Compression = "brotli"

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "unsupported compression")
}

func TestConfigValidatePathWithoutLeadingSlash(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")
	cfg.Path = "v1/otel/ingest"

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "path must start with '/'")
}

func TestCreateDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	assert.Equal(t, defaultPath, cfg.Path)
	assert.Equal(t, defaultDataset, cfg.Dataset)
	assert.Equal(t, defaultLogsTable, cfg.Tables.Logs)
	assert.Equal(t, defaultTracesTable, cfg.Tables.Traces)
	assert.Equal(t, defaultMetricsTable, cfg.Tables.Metrics)
	assert.False(t, cfg.CreateTablesIfNotExist)
	assert.Equal(t, defaultSchemaVersion, cfg.SchemaVersion)
	assert.Equal(t, defaultCompression, cfg.Compression)
	assert.True(t, cfg.RetryOnFailure.Enabled)
	assert.True(t, cfg.SendingQueue.HasValue())
	assert.Equal(t, int64(10_000), cfg.SendingQueue.Get().QueueSize)
	assert.Equal(t, 4, cfg.SendingQueue.Get().NumConsumers)
}

func TestConfigValidateInvalidTable(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")
	cfg.Tables.Logs = "bad-table-name"

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "tables.logs table route must be table, schema.table, or database.schema.table")
}

func TestConfigValidateTableRefVariants(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")
	cfg.Tables.Logs = "otel.logs"
	cfg.Tables.Traces = "scopedb.otel.traces"
	cfg.Tables.Metrics = "metrics"

	require.NoError(t, cfg.Validate())
}

func TestConfigValidateMissingTableRoute(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")
	cfg.Tables.Logs = "otel.logs"
	cfg.Tables.Traces = ""
	cfg.Tables.Metrics = "otel.metrics"

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "tables.traces is required")
}

func TestConfigValidateDuplicateTables(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")
	cfg.Tables.Logs = "otel.shared"
	cfg.Tables.Traces = "otel.shared"
	cfg.Tables.Metrics = ""

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "tables.logs and tables.traces must point to different tables")
	assert.ErrorContains(t, err, "tables.metrics is required")
}

func TestConfigTableRouting(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Tables.Logs = "public.vendor_otel_logs"

	assert.Equal(t, "public.vendor_otel_logs", cfg.tableForSignal(signalLogs))
	assert.Equal(t, defaultTracesTable, cfg.tableForSignal(signalTraces))
	assert.Equal(t, defaultMetricsTable, cfg.tableForSignal(signalMetrics))
	assert.Equal(t, []string{"public.vendor_otel_logs", defaultTracesTable, defaultMetricsTable}, cfg.configuredTables())
}
