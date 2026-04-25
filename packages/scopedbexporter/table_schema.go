/*
 * Copyright 2026 ScopeDB contributors
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
	"fmt"
	"strings"
)

type tableColumn struct {
	Name string
	Type string
}

type tablePhysicalLayout struct {
	PartitionBy []string
	ClusterBy   []string
	Indexes     []tableIndex
}

type tableIndex struct {
	Type string
	Expr string
}

var signalTableColumns = map[string][]tableColumn{
	signalLogs: {
		{Name: "ingest_ts", Type: "timestamp"},
		{Name: "record_timestamp", Type: "timestamp"},
		{Name: "observed_timestamp", Type: "timestamp"},
		{Name: "schema_version", Type: "string"},
		{Name: "env", Type: "string"},
		{Name: "row_id", Type: "string"},
		{Name: "service", Type: "string"},
		{Name: "version", Type: "string"},
		{Name: "instance_id", Type: "string"},
		{Name: "k8s_pod", Type: "string"},
		{Name: "k8s_namespace", Type: "string"},
		{Name: "k8s_cluster", Type: "string"},
		{Name: "container_name", Type: "string"},
		{Name: "host_ip", Type: "string"},
		{Name: "host", Type: "string"},
		{Name: "trace_id", Type: "string"},
		{Name: "span_id", Type: "string"},
		{Name: "source", Type: "string"},
		{Name: "status", Type: "string"},
		{Name: "severity_number", Type: "int"},
		{Name: "message", Type: "string"},
		{Name: "exception_type", Type: "string"},
		{Name: "exception_message", Type: "string"},
		{Name: "record", Type: "object"},
	},
	signalTraces: {
		{Name: "ingest_ts", Type: "timestamp"},
		{Name: "start_timestamp", Type: "timestamp"},
		{Name: "end_timestamp", Type: "timestamp"},
		{Name: "duration_ns", Type: "int"},
		{Name: "schema_version", Type: "string"},
		{Name: "env", Type: "string"},
		{Name: "row_id", Type: "string"},
		{Name: "service", Type: "string"},
		{Name: "version", Type: "string"},
		{Name: "instance_id", Type: "string"},
		{Name: "k8s_pod", Type: "string"},
		{Name: "k8s_namespace", Type: "string"},
		{Name: "k8s_cluster", Type: "string"},
		{Name: "container_name", Type: "string"},
		{Name: "host_ip", Type: "string"},
		{Name: "host", Type: "string"},
		{Name: "trace_id", Type: "string"},
		{Name: "span_id", Type: "string"},
		{Name: "parent_span_id", Type: "string"},
		{Name: "span_name", Type: "string"},
		{Name: "span_kind", Type: "string"},
		{Name: "status_code", Type: "string"},
		{Name: "http_method", Type: "string"},
		{Name: "http_status_code", Type: "int"},
		{Name: "url_path", Type: "string"},
		{Name: "http_route", Type: "string"},
		{Name: "peer_service", Type: "string"},
		{Name: "db_system", Type: "string"},
		{Name: "db_operation", Type: "string"},
		{Name: "rpc_method", Type: "string"},
		{Name: "error_type", Type: "string"},
		{Name: "record", Type: "object"},
	},
	signalMetrics: {
		{Name: "ingest_ts", Type: "timestamp"},
		{Name: "record_timestamp", Type: "timestamp"},
		{Name: "start_timestamp", Type: "timestamp"},
		{Name: "schema_version", Type: "string"},
		{Name: "env", Type: "string"},
		{Name: "row_id", Type: "string"},
		{Name: "service", Type: "string"},
		{Name: "version", Type: "string"},
		{Name: "instance_id", Type: "string"},
		{Name: "k8s_pod", Type: "string"},
		{Name: "k8s_namespace", Type: "string"},
		{Name: "k8s_cluster", Type: "string"},
		{Name: "container_name", Type: "string"},
		{Name: "host_ip", Type: "string"},
		{Name: "host", Type: "string"},
		{Name: "metric_name", Type: "string"},
		{Name: "metric_type", Type: "string"},
		{Name: "temporality", Type: "string"},
		{Name: "unit", Type: "string"},
		{Name: "number_value", Type: "float"},
		{Name: "distribution", Type: "object"},
		{Name: "record", Type: "object"},
	},
}

var signalTableLayouts = map[string]tablePhysicalLayout{
	signalLogs: {
		PartitionBy: []string{"floor(record_timestamp, 24, 'hour')"},
		ClusterBy:   []string{"env", "service", "severity_number", "record_timestamp"},
		Indexes: []tableIndex{
			{Type: "RANGE", Expr: "record_timestamp"},
			{Type: "POINT", Expr: "trace_id"},
			{Type: "POINT", Expr: "span_id"},
			{Type: "POINT", Expr: "service"},
			{Type: "PATTERN", Expr: "service"},
			{Type: "POINT", Expr: "version"},
			{Type: "PATTERN", Expr: "version"},
			{Type: "PATTERN", Expr: "instance_id"},
			{Type: "PATTERN", Expr: "k8s_pod"},
			{Type: "POINT", Expr: "k8s_namespace"},
			{Type: "PATTERN", Expr: "k8s_namespace"},
			{Type: "POINT", Expr: "k8s_cluster"},
			{Type: "PATTERN", Expr: "k8s_cluster"},
			{Type: "PATTERN", Expr: "container_name"},
			{Type: "PATTERN", Expr: "host_ip"},
			{Type: "PATTERN", Expr: "host"},
			{Type: "POINT", Expr: "source"},
			{Type: "PATTERN", Expr: "source"},
			{Type: "POINT", Expr: "status"},
			{Type: "POINT", Expr: "severity_number"},
			{Type: "RANGE", Expr: "severity_number"},
			{Type: "POINT", Expr: "exception_type"},
			{Type: "PATTERN", Expr: "exception_type"},
			{Type: "SEARCH", Expr: "message"},
			{Type: "PATTERN", Expr: "message"},
			{Type: "SEARCH", Expr: "exception_message"},
			{Type: "PATTERN", Expr: "exception_message"},
		},
	},
	signalTraces: {
		PartitionBy: []string{"floor(start_timestamp, 24, 'hour')"},
		ClusterBy:   []string{"env", "service", "status_code", "start_timestamp"},
		Indexes: []tableIndex{
			{Type: "RANGE", Expr: "start_timestamp"},
			{Type: "RANGE", Expr: "duration_ns"},
			{Type: "POINT", Expr: "trace_id"},
			{Type: "POINT", Expr: "span_id"},
			{Type: "POINT", Expr: "parent_span_id"},
			{Type: "POINT", Expr: "service"},
			{Type: "PATTERN", Expr: "service"},
			{Type: "POINT", Expr: "version"},
			{Type: "PATTERN", Expr: "version"},
			{Type: "PATTERN", Expr: "instance_id"},
			{Type: "PATTERN", Expr: "k8s_pod"},
			{Type: "POINT", Expr: "k8s_namespace"},
			{Type: "PATTERN", Expr: "k8s_namespace"},
			{Type: "POINT", Expr: "k8s_cluster"},
			{Type: "PATTERN", Expr: "k8s_cluster"},
			{Type: "PATTERN", Expr: "container_name"},
			{Type: "PATTERN", Expr: "host_ip"},
			{Type: "PATTERN", Expr: "host"},
			{Type: "PATTERN", Expr: "span_name"},
			{Type: "POINT", Expr: "status_code"},
			{Type: "POINT", Expr: "http_status_code"},
			{Type: "POINT", Expr: "url_path"},
			{Type: "PATTERN", Expr: "url_path"},
			{Type: "POINT", Expr: "http_route"},
			{Type: "PATTERN", Expr: "http_route"},
			{Type: "POINT", Expr: "peer_service"},
			{Type: "PATTERN", Expr: "peer_service"},
			{Type: "PATTERN", Expr: "rpc_method"},
			{Type: "POINT", Expr: "error_type"},
			{Type: "PATTERN", Expr: "error_type"},
		},
	},
	signalMetrics: {
		PartitionBy: []string{"floor(record_timestamp, 24, 'hour')"},
		ClusterBy:   []string{"env", "service", "metric_name", "record_timestamp"},
		Indexes: []tableIndex{
			{Type: "RANGE", Expr: "record_timestamp"},
			{Type: "POINT", Expr: "metric_name"},
			{Type: "PATTERN", Expr: "metric_name"},
			{Type: "POINT", Expr: "service"},
			{Type: "PATTERN", Expr: "service"},
			{Type: "POINT", Expr: "version"},
			{Type: "PATTERN", Expr: "version"},
			{Type: "PATTERN", Expr: "instance_id"},
			{Type: "PATTERN", Expr: "k8s_pod"},
			{Type: "POINT", Expr: "k8s_namespace"},
			{Type: "PATTERN", Expr: "k8s_namespace"},
			{Type: "POINT", Expr: "k8s_cluster"},
			{Type: "PATTERN", Expr: "k8s_cluster"},
			{Type: "PATTERN", Expr: "container_name"},
			{Type: "PATTERN", Expr: "host_ip"},
			{Type: "PATTERN", Expr: "host"},
		},
	},
}

func createTableStatementForSignal(signal string, table tableRef) string {
	layout := layoutForSignal(signal)
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (\n%s\n) PARTITION BY %s CLUSTER BY %s",
		table.Identifier(),
		strings.Join(columnDefinitionLines(signal), ",\n"),
		strings.Join(layout.PartitionBy, ", "),
		strings.Join(layout.ClusterBy, ", "),
	)
}

func createIndexStatementsForSignal(signal string, table tableRef) []string {
	layout := layoutForSignal(signal)
	statements := make([]string, 0, len(layout.Indexes))
	for _, index := range layout.Indexes {
		statements = append(statements, fmt.Sprintf(
			"CREATE %s INDEX IF NOT EXISTS ON %s (%s)",
			index.Type,
			table.Identifier(),
			index.Expr,
		))
	}
	return statements
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

func layoutForSignal(signal string) tablePhysicalLayout {
	layout, ok := signalTableLayouts[signal]
	if !ok {
		panic(fmt.Sprintf("unsupported signal %q", signal))
	}
	return layout
}
