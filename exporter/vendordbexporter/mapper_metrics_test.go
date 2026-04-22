package vendordbexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestMapMetrics(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "http://localhost:8080"
	cfg.APIKey = configopaque.String("demo-key")

	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	resourceMetrics.Resource().Attributes().PutStr("service.name", "checkout")

	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()
	scopeMetrics.Scope().SetName("test-scope")
	scopeMetrics.Scope().SetVersion("1.0.0")

	gaugeMetric := scopeMetrics.Metrics().AppendEmpty()
	gaugeMetric.SetName("cpu.utilization")
	gaugeMetric.SetDescription("CPU utilization")
	gaugeMetric.SetUnit("1")
	gaugePoint := gaugeMetric.SetEmptyGauge().DataPoints().AppendEmpty()
	gaugePoint.SetTimestamp(pcommon.Timestamp(100))
	gaugePoint.SetDoubleValue(0.75)
	gaugePoint.Attributes().PutStr("host", "api-1")

	sumMetric := scopeMetrics.Metrics().AppendEmpty()
	sumMetric.SetName("http.requests")
	sumMetric.SetUnit("1")
	sum := sumMetric.SetEmptySum()
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	sum.SetIsMonotonic(true)
	sumPoint := sum.DataPoints().AppendEmpty()
	sumPoint.SetTimestamp(pcommon.Timestamp(200))
	sumPoint.SetStartTimestamp(pcommon.Timestamp(150))
	sumPoint.SetIntValue(42)

	histMetric := scopeMetrics.Metrics().AppendEmpty()
	histMetric.SetName("request.duration")
	hist := histMetric.SetEmptyHistogram()
	hist.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
	histPoint := hist.DataPoints().AppendEmpty()
	histPoint.SetTimestamp(pcommon.Timestamp(300))
	histPoint.SetCount(3)
	histPoint.SetSum(12.5)
	histPoint.ExplicitBounds().FromRaw([]float64{0.5, 1.0})
	histPoint.BucketCounts().FromRaw([]uint64{1, 2, 0})

	payload, err := mapMetrics(cfg, metrics)
	require.NoError(t, err)
	require.Len(t, payload.Records, 3)

	gaugeRecord := findMetricRecord(t, payload.Records, "cpu.utilization")
	assert.Equal(t, "gauge", gaugeRecord["type"])
	assert.Equal(t, 0.75, gaugeRecord["value"])
	assert.Equal(t, map[string]any{"host": "api-1"}, gaugeRecord["attributes"])
	assert.Equal(t, map[string]any{"service.name": "checkout"}, gaugeRecord["resource"])

	sumRecord := findMetricRecord(t, payload.Records, "http.requests")
	assert.Equal(t, "sum", sumRecord["type"])
	assert.Equal(t, "cumulative", sumRecord["temporality"])
	assert.Equal(t, true, sumRecord["is_monotonic"])
	assert.Equal(t, int64(42), sumRecord["value"])

	histRecord := findMetricRecord(t, payload.Records, "request.duration")
	assert.Equal(t, "histogram", histRecord["type"])
	assert.Equal(t, "delta", histRecord["temporality"])
	histogram := histRecord["histogram"].(map[string]any)
	assert.Equal(t, uint64(3), histogram["count"])
	assert.Equal(t, 12.5, histogram["sum"])
	assert.Equal(t, []float64{0.5, 1.0}, histogram["explicit_bounds"])
	assert.Equal(t, []uint64{1, 2, 0}, histogram["bucket_counts"])
}

func findMetricRecord(t *testing.T, records []Record, metricName string) Record {
	t.Helper()
	for _, record := range records {
		if record["metric_name"] == metricName {
			return record
		}
	}
	t.Fatalf("metric %q not found", metricName)
	return nil
}
