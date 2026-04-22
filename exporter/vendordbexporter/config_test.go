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
	assert.Equal(t, defaultTable, cfg.Table)
	assert.False(t, cfg.CreateTableIfNotExists)
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
	cfg.Table = "bad-table-name"

	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "table must be a simple identifier")
}
