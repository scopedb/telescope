package scopedbexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestMapLogs(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "https://scopedb.invalid"
	cfg.APIKey = configopaque.String("test-api-key")
	cfg.Dataset = "demo"

	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr("service.name", "checkout")
	resourceLogs.Resource().Attributes().PutStr("service.instance.id", "checkout-1")
	resourceLogs.Resource().Attributes().PutStr("k8s.pod.name", "checkout-pod")
	resourceLogs.Resource().Attributes().PutStr("host.name", "checkout-node")
	resourceLogs.Resource().Attributes().PutEmptySlice("host.ip").AppendEmpty().SetStr("10.0.0.10")

	scopeLogs := resourceLogs.ScopeLogs().AppendEmpty()
	scopeLogs.Scope().SetName("test-scope")
	scopeLogs.Scope().SetVersion("1.2.3")

	record := scopeLogs.LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.Timestamp(123))
	record.SetObservedTimestamp(pcommon.Timestamp(456))
	record.SetTraceID(pcommon.TraceID([16]byte{1, 2, 3}))
	record.SetSpanID(pcommon.SpanID([8]byte{4, 5, 6}))
	record.SetSeverityText("INFO")
	record.SetSeverityNumber(plog.SeverityNumberInfo)
	record.Body().SetStr("hello world")
	record.Attributes().PutStr("env", "dev")

	payload, err := mapLogs(cfg, logs)
	require.NoError(t, err)
	require.Len(t, payload.Records, 1)

	mapped := payload.Records[0]
	assert.Equal(t, signalLogs, payload.Signal)
	assert.Equal(t, "123", mapped["timestamp_unix_nano"])
	assert.Equal(t, "456", mapped["observed_timestamp_unix_nano"])
	assert.Equal(t, "01020300000000000000000000000000", mapped["trace_id"])
	assert.Equal(t, "0405060000000000", mapped["span_id"])
	assert.Equal(t, "INFO", mapped["severity_text"])
	assert.Equal(t, int(plog.SeverityNumberInfo), mapped["severity_number"])
	assert.Equal(t, "hello world", mapped["body"])
	assert.Equal(t, "hello world", mapped["message"])
	assert.Equal(t, map[string]any{
		"service.name":        "checkout",
		"service.instance.id": "checkout-1",
		"k8s.pod.name":        "checkout-pod",
		"host.name":           "checkout-node",
		"host.ip":             []any{"10.0.0.10"},
	}, mapped["resource"])
	assert.Equal(t, map[string]any{"name": "test-scope", "version": "1.2.3"}, mapped["scope"])
	assert.Equal(t, map[string]any{"env": "dev"}, mapped["attributes"])
}
