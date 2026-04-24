package scopedbexporter

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
				"exception_type string",
				"exception_message string",
				"version string",
				"source string",
				"k8s_namespace string",
				"k8s_cluster string",
				"container_name string",
				"status string",
				"severity_number int",
				"PARTITION BY floor(record_timestamp, 24, 'hour')",
				"CLUSTER BY env, service, severity_number, record_timestamp",
			},
		},
		{
			name:   "traces",
			signal: signalTraces,
			table:  "public.vendor_otel_traces_test",
			contains: []string{
				"duration_ns int",
				"span_name string",
				"span_kind string",
				"status_code string",
				"http_method string",
				"http_status_code int",
				"url_path string",
				"http_route string",
				"peer_service string",
				"db_system string",
				"db_operation string",
				"rpc_method string",
				"error_type string",
				"PARTITION BY floor(start_timestamp, 24, 'hour')",
				"CLUSTER BY env, service, status_code, start_timestamp",
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
				"PARTITION BY floor(record_timestamp, 24, 'hour')",
				"CLUSTER BY env, service, metric_name, record_timestamp",
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

func TestCreateIndexStatementsForSignal(t *testing.T) {
	tests := []struct {
		name   string
		signal string
		table  string
		want   []string
	}{
		{
			name:   "logs",
			signal: signalLogs,
			table:  "public.vendor_otel_logs_test",
			want: []string{
				"CREATE RANGE INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (record_timestamp)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (trace_id)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (service)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (service)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (version)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (version)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (instance_id)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (k8s_pod)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (k8s_namespace)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (k8s_namespace)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (k8s_cluster)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (k8s_cluster)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (container_name)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (host_ip)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (host)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (source)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (source)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (status)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (severity_number)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (exception_type)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (exception_type)",
				"CREATE SEARCH INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (message)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (message)",
				"CREATE SEARCH INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (exception_message)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_logs_test` (exception_message)",
			},
		},
		{
			name:   "traces",
			signal: signalTraces,
			table:  "public.vendor_otel_traces_test",
			want: []string{
				"CREATE RANGE INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (start_timestamp)",
				"CREATE RANGE INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (duration_ns)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (trace_id)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (span_id)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (service)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (service)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (version)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (version)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (instance_id)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (k8s_pod)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (k8s_namespace)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (k8s_namespace)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (k8s_cluster)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (k8s_cluster)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (container_name)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (host_ip)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (host)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (span_name)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (status_code)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (http_status_code)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (url_path)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (url_path)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (http_route)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (http_route)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (peer_service)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (peer_service)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (rpc_method)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (error_type)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_traces_test` (error_type)",
			},
		},
		{
			name:   "metrics",
			signal: signalMetrics,
			table:  "public.vendor_otel_metrics_test",
			want: []string{
				"CREATE RANGE INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (record_timestamp)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (metric_name)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (metric_name)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (service)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (service)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (version)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (version)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (instance_id)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (k8s_pod)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (k8s_namespace)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (k8s_namespace)",
				"CREATE POINT INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (k8s_cluster)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (k8s_cluster)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (container_name)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (host_ip)",
				"CREATE PATTERN INDEX IF NOT EXISTS ON `public`.`vendor_otel_metrics_test` (host)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseTableRef(tt.table)
			require.NoError(t, err)

			assert.Equal(t, tt.want, createIndexStatementsForSignal(tt.signal, ref))
		})
	}
}
