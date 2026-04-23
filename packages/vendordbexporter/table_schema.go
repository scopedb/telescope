package vendordbexporter

import (
	"fmt"
	"strings"
)

type tableColumn struct {
	Name string
	Type string
}

var signalTableColumns = map[string][]tableColumn{
	signalLogs: {
		{Name: "ingest_ts", Type: "timestamp"},
		{Name: "record_timestamp", Type: "timestamp"},
		{Name: "observed_timestamp", Type: "timestamp"},
		{Name: "schema_version", Type: "string"},
		{Name: "dataset", Type: "string"},
		{Name: "row_id", Type: "string"},
		{Name: "service_name", Type: "string"},
		{Name: "instance_id", Type: "string"},
		{Name: "pod_name", Type: "string"},
		{Name: "host_ip", Type: "string"},
		{Name: "host_name", Type: "string"},
		{Name: "trace_id", Type: "string"},
		{Name: "span_id", Type: "string"},
		{Name: "severity_text", Type: "string"},
		{Name: "message", Type: "string"},
		{Name: "record", Type: "object"},
	},
	signalTraces: {
		{Name: "ingest_ts", Type: "timestamp"},
		{Name: "start_timestamp", Type: "timestamp"},
		{Name: "end_timestamp", Type: "timestamp"},
		{Name: "duration_ns", Type: "int"},
		{Name: "schema_version", Type: "string"},
		{Name: "dataset", Type: "string"},
		{Name: "row_id", Type: "string"},
		{Name: "service_name", Type: "string"},
		{Name: "instance_id", Type: "string"},
		{Name: "pod_name", Type: "string"},
		{Name: "host_ip", Type: "string"},
		{Name: "host_name", Type: "string"},
		{Name: "trace_id", Type: "string"},
		{Name: "span_id", Type: "string"},
		{Name: "parent_span_id", Type: "string"},
		{Name: "span_name", Type: "string"},
		{Name: "span_kind", Type: "string"},
		{Name: "status_code", Type: "string"},
		{Name: "record", Type: "object"},
	},
	signalMetrics: {
		{Name: "ingest_ts", Type: "timestamp"},
		{Name: "record_timestamp", Type: "timestamp"},
		{Name: "start_timestamp", Type: "timestamp"},
		{Name: "schema_version", Type: "string"},
		{Name: "dataset", Type: "string"},
		{Name: "row_id", Type: "string"},
		{Name: "service_name", Type: "string"},
		{Name: "instance_id", Type: "string"},
		{Name: "pod_name", Type: "string"},
		{Name: "host_ip", Type: "string"},
		{Name: "host_name", Type: "string"},
		{Name: "metric_name", Type: "string"},
		{Name: "metric_type", Type: "string"},
		{Name: "temporality", Type: "string"},
		{Name: "unit", Type: "string"},
		{Name: "number_value", Type: "float"},
		{Name: "distribution", Type: "object"},
		{Name: "record", Type: "object"},
	},
}

func createTableStatementForSignal(signal string, table tableRef) string {
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (\n%s\n)",
		table.Identifier(),
		strings.Join(columnDefinitionLines(signal), ",\n"),
	)
}

func ingestStatementForSignal(signal string, table tableRef) string {
	return fmt.Sprintf(
		"SELECT\n%s\nINSERT INTO %s (\n%s\n)",
		strings.Join(columnSelectLines(signal), ",\n"),
		table.Identifier(),
		strings.Join(columnNameLines(signal), ",\n"),
	)
}

func columnDefinitionLines(signal string) []string {
	columns := columnsForSignal(signal)
	lines := make([]string, 0, len(columns))
	for _, column := range columns {
		lines = append(lines, fmt.Sprintf("  %s %s", column.Name, column.Type))
	}
	return lines
}

func columnSelectLines(signal string) []string {
	columns := columnsForSignal(signal)
	lines := make([]string, 0, len(columns))
	for _, column := range columns {
		lines = append(lines, fmt.Sprintf("  $0[%q]::%s AS %s", column.Name, column.Type, column.Name))
	}
	return lines
}

func columnNameLines(signal string) []string {
	columns := columnsForSignal(signal)
	lines := make([]string, 0, len(columns))
	for _, column := range columns {
		lines = append(lines, fmt.Sprintf("  %s", column.Name))
	}
	return lines
}

func columnsForSignal(signal string) []tableColumn {
	columns, ok := signalTableColumns[signal]
	if !ok {
		panic(fmt.Sprintf("unsupported signal %q", signal))
	}
	return columns
}
