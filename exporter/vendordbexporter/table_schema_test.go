package vendordbexporter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateTableStatementForSignal(t *testing.T) {
	tests := []struct {
		name     string
		signal   string
		table    string
		contains []string
	}{
		{
			name:   "logs",
			signal: signalLogs,
			table:  "public.vendor_otel_logs_test",
			contains: []string{
				"message string",
				"severity_text string",
			},
		},
		{
			name:   "traces",
			signal: signalTraces,
			table:  "public.vendor_otel_traces_test",
			contains: []string{
				"duration_ns int",
				"span_name string",
				"status_code string",
			},
		},
		{
			name:   "metrics",
			signal: signalMetrics,
			table:  "public.vendor_otel_metrics_test",
			contains: []string{
				"number_value float",
				"distribution object",
				"metric_type string",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseTableRef(tt.table)
			require.NoError(t, err)

			statement := createTableStatementForSignal(tt.signal, ref)
			for _, expected := range tt.contains {
				assert.Contains(t, statement, expected)
			}
		})
	}
}
