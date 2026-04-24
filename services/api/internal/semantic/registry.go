package semantic

const (
	defaultDetailOrder = "ts DESC, row_id DESC"
	defaultRelationMax = 1000
)

var (
	commonScopedFields = []string{
		"ts",
		"row_id",
		"env",
		"service",
		"version",
		"instance_id",
		"k8s_pod",
		"k8s_namespace",
		"k8s_cluster",
		"container_name",
		"host_ip",
		"host",
	}

	countMeasure         = MeasureDef{Name: "count", Description: "Count rows.", InputTypes: []FieldType{FieldTypeAny}}
	countDistinctMeasure = MeasureDef{Name: "count_distinct", Description: "Count distinct field values.", FieldRequired: true, InputTypes: []FieldType{FieldTypeAny}}
	numericMeasureTypes  = []FieldType{FieldTypeInt, FieldTypeFloat}
)

func numericMeasure(name string, description string) MeasureDef {
	return MeasureDef{
		Name:          name,
		Description:   description,
		FieldRequired: true,
		InputTypes:    numericMeasureTypes,
	}
}

func appendFields(base []string, extra ...string) []string {
	fields := make([]string, 0, len(base)+len(extra))
	fields = append(fields, base...)
	fields = append(fields, extra...)
	return fields
}

func defaultDimensionField(name string, description string, searchable bool, patternable bool) FieldSpec {
	return aliasedDefaultDimensionField(name, name, description, searchable, patternable)
}

func aliasedDefaultDimensionField(name string, source string, description string, searchable bool, patternable bool) FieldSpec {
	return FieldSpec{
		Name:        name,
		Type:        FieldTypeString,
		Role:        FieldRoleDimension,
		Stability:   StabilityCore,
		Description: description,
		Filterable:  true,
		Searchable:  searchable,
		Patternable: patternable,
		Groupable:   true,
		ExprByRelation: map[string]Expr{
			"default": Ref(source),
		},
	}
}

func relationDimensionField(name string, relation string, source string, description string, stability Stability, searchable bool, patternable bool) FieldSpec {
	return relationTypedDimensionField(name, relation, source, description, stability, FieldTypeString, searchable, patternable)
}

func relationTypedDimensionField(name string, relation string, source string, description string, stability Stability, fieldType FieldType, searchable bool, patternable bool) FieldSpec {
	return FieldSpec{
		Name:        name,
		Type:        fieldType,
		Role:        FieldRoleDimension,
		Stability:   stability,
		Description: description,
		Filterable:  true,
		Searchable:  searchable,
		Patternable: patternable,
		Groupable:   true,
		ExprByRelation: map[string]Expr{
			relation: Ref(source),
		},
	}
}

func relationMeasureField(name string, relation string, source string, description string, fieldType FieldType) FieldSpec {
	return FieldSpec{
		Name:        name,
		Type:        fieldType,
		Role:        FieldRoleMeasure,
		Stability:   StabilityCore,
		Description: description,
		Filterable:  true,
		Groupable:   false,
		ExprByRelation: map[string]Expr{
			relation: Ref(source),
		},
	}
}

var Default = Registry{
	Fields: []FieldSpec{
		{
			Name:        "ts",
			Type:        FieldTypeTimestamp,
			Role:        FieldRoleTime,
			Stability:   StabilityCore,
			Description: "Canonical event time across semantic relations.",
			Filterable:  true,
			Groupable:   true,
			ExprByRelation: map[string]Expr{
				"events_v1":       Ref("record_timestamp"),
				"executions_v1":   Ref("start_timestamp"),
				"measurements_v1": Ref("record_timestamp"),
			},
		},
		{
			Name:        "row_id",
			Type:        FieldTypeString,
			Role:        FieldRoleTieBreaker,
			Stability:   StabilityCore,
			Description: "Landing-row tie-breaker for detail ordering and pagination.",
			Filterable:  true,
			Groupable:   false,
			ExprByRelation: map[string]Expr{
				"default": Ref("row_id"),
			},
		},
		defaultDimensionField("env", "Logical deployment environment.", false, false),
		defaultDimensionField("service", "Canonical service name.", true, true),
		defaultDimensionField("version", "Service deployment version.", true, true),
		defaultDimensionField("instance_id", "OTel-native service instance identity when present.", true, true),
		defaultDimensionField("k8s_pod", "Kubernetes pod name when present.", true, true),
		defaultDimensionField("k8s_namespace", "Kubernetes namespace when present.", true, true),
		defaultDimensionField("k8s_cluster", "Kubernetes cluster name when present.", true, true),
		defaultDimensionField("container_name", "Container name when present.", true, true),
		defaultDimensionField("host_ip", "Host IP when present.", false, true),
		defaultDimensionField("host", "Host name when present.", true, true),
		defaultDimensionField("trace_id", "Execution-scoped anchor when present.", true, true),
		aliasedDefaultDimensionField("execution_id", "trace_id", "Semantic execution identifier. In v1 it aliases trace_id.", true, true),
		defaultDimensionField("span_id", "Current span identifier when present.", true, true),
		{
			Name:        "parent_span_id",
			Type:        FieldTypeString,
			Role:        FieldRoleDimension,
			Stability:   StabilityCore,
			Description: "Parent span identifier for trace-derived relations.",
			Filterable:  true,
			Groupable:   true,
			ExprByRelation: map[string]Expr{
				"executions_v1": Ref("parent_span_id"),
			},
		},
		relationDimensionField("source", "events_v1", "source", "Event source or integration name when present.", StabilityCore, true, true),
		relationDimensionField("status", "events_v1", "status", "Log severity or event status.", StabilityCore, true, true),
		relationTypedDimensionField("severity_number", "events_v1", "severity_number", "OTel log severity number for stable severity filtering and physical clustering.", StabilityCore, FieldTypeInt, false, false),
		relationDimensionField("exception_type", "events_v1", "exception_type", "Structured exception class or type when present.", StabilityCore, true, true),
		relationDimensionField("exception_message", "events_v1", "exception_message", "Structured exception message when present.", StabilityCore, true, true),
		{
			Name:        "message",
			Type:        FieldTypeString,
			Role:        FieldRoleValue,
			Stability:   StabilityCore,
			Description: "Human-readable log message.",
			Filterable:  true,
			Searchable:  true,
			Patternable: true,
			Groupable:   false,
			ExprByRelation: map[string]Expr{
				"events_v1": Ref("message"),
			},
		},
		relationDimensionField("operation", "executions_v1", "span_name", "Developer-facing operation name.", StabilityBeta, true, true),
		relationDimensionField("span_kind", "executions_v1", "span_kind", "OTel span kind such as server, client, producer, or consumer.", StabilityCore, false, false),
		relationDimensionField("status_code", "executions_v1", "status_code", "Execution outcome status code.", StabilityCore, false, false),
		relationDimensionField("http_method", "executions_v1", "http_method", "HTTP request method when present.", StabilityCore, false, false),
		relationTypedDimensionField("http_status_code", "executions_v1", "http_status_code", "HTTP response status code when present.", StabilityCore, FieldTypeInt, false, false),
		relationDimensionField("url_path", "executions_v1", "url_path", "HTTP URL path when present.", StabilityCore, true, true),
		relationDimensionField("http_route", "executions_v1", "http_route", "Low-cardinality HTTP route template when present.", StabilityCore, true, true),
		relationDimensionField("peer_service", "executions_v1", "peer_service", "Named downstream peer service when present.", StabilityCore, true, true),
		relationDimensionField("db_system", "executions_v1", "db_system", "Database system name when present.", StabilityCore, false, false),
		relationDimensionField("db_operation", "executions_v1", "db_operation", "Database operation name when present.", StabilityCore, false, false),
		relationDimensionField("rpc_method", "executions_v1", "rpc_method", "RPC method when present.", StabilityCore, true, true),
		relationDimensionField("error_type", "executions_v1", "error_type", "Structured execution error type when present.", StabilityCore, true, true),
		relationMeasureField("duration_ns", "executions_v1", "duration_ns", "Execution duration in nanoseconds.", FieldTypeInt),
		relationDimensionField("metric_name", "measurements_v1", "metric_name", "Metric name.", StabilityCore, true, true),
		relationDimensionField("metric_type", "measurements_v1", "metric_type", "Metric type.", StabilityCore, false, false),
		relationDimensionField("temporality", "measurements_v1", "temporality", "Metric aggregation temporality.", StabilityCore, false, false),
		relationDimensionField("unit", "measurements_v1", "unit", "Metric unit.", StabilityCore, false, false),
		relationMeasureField("number_value", "measurements_v1", "number_value", "Scalar metric value when available.", FieldTypeFloat),
		{
			Name:        "record",
			Type:        FieldTypeObject,
			Role:        FieldRoleObject,
			Stability:   StabilityCore,
			Description: "Original mapped signal payload for evidence and human inspection.",
			Filterable:  false,
			Groupable:   false,
			ExprByRelation: map[string]Expr{
				"default": Ref("record"),
			},
		},
	},
	Relations: []RelationSpec{
		{
			Name:              "events_v1",
			Kind:              "event",
			Description:       "Discrete events for debugging, initially backed by log rows.",
			SourceTable:       "scopedb.otel.logs",
			TimeField:         "ts",
			DefaultOrderBy:    defaultDetailOrder,
			DefaultLimit:      100,
			MaxLimit:          defaultRelationMax,
			SupportsSearch:    true,
			SupportsAggregate: true,
			Fields:            appendFields(commonScopedFields, "trace_id", "execution_id", "span_id", "source", "status", "severity_number", "exception_type", "exception_message", "message", "record"),
			Anchors:           []string{"trace_id", "execution_id"},
			Measures: []MeasureDef{
				countMeasure,
				countDistinctMeasure,
			},
			Advisory: RelationAdvisory{
				IdentityFields:    []string{"trace_id", "span_id"},
				AnchorFields:      []string{"trace_id", "execution_id", "service"},
				DefaultProject:    []string{"ts", "row_id", "service", "version", "host", "source", "trace_id", "span_id", "status", "severity_number", "exception_type", "message"},
				PreferredFilters:  []string{"env", "service", "version", "host", "source", "trace_id", "execution_id", "status", "severity_number", "exception_type", "message", "exception_message"},
				PreferredGroupBy:  []string{"service", "version", "source", "status", "severity_number", "exception_type", "host", "k8s_namespace", "k8s_cluster"},
				PreferredMeasures: []string{"count"},
				CommonPatterns: []string{
					"find recent error events for one service",
					"find events near a known trace_id",
					"break down error volume by status, exception_type, or instance_id",
				},
				Notes: []string{
					"events_v1 is row-oriented and best suited for detail search plus lightweight breakdowns",
					"message supports search, contains, and regexp_like filters",
					"record is an evidence payload, not the default query surface; promote or materialize important raw attributes before filtering",
				},
			},
		},
		{
			Name:              "executions_v1",
			Kind:              "execution",
			Description:       "Execution-oriented trace view for request-scoped debugging.",
			SourceTable:       "scopedb.otel.traces",
			Where:             "(parent_span_id IS NULL) OR (parent_span_id = '')",
			TimeField:         "ts",
			DefaultOrderBy:    defaultDetailOrder,
			DefaultLimit:      100,
			MaxLimit:          defaultRelationMax,
			SupportsSearch:    true,
			SupportsAggregate: true,
			Fields: appendFields(
				commonScopedFields,
				"trace_id", "execution_id", "span_id", "parent_span_id",
				"operation", "span_kind", "status_code", "http_method", "http_status_code", "url_path",
				"http_route", "peer_service", "db_system", "db_operation", "rpc_method", "error_type",
				"duration_ns", "record",
			),
			Anchors: []string{"trace_id", "execution_id"},
			Measures: []MeasureDef{
				countMeasure,
				countDistinctMeasure,
				numericMeasure("p50", "Median duration."),
				numericMeasure("p95", "95th percentile duration."),
				numericMeasure("p99", "99th percentile duration."),
			},
			Advisory: RelationAdvisory{
				IdentityFields:    []string{"trace_id"},
				AnchorFields:      []string{"trace_id", "execution_id", "service", "instance_id", "k8s_pod"},
				DefaultProject:    []string{"ts", "row_id", "service", "version", "host", "trace_id", "operation", "status_code", "http_status_code", "http_route", "url_path", "peer_service", "error_type", "duration_ns"},
				PreferredFilters:  []string{"trace_id", "execution_id", "env", "service", "version", "instance_id", "k8s_pod", "status_code", "http_status_code", "http_route", "url_path", "peer_service", "error_type"},
				PreferredGroupBy:  []string{"service", "version", "operation", "status_code", "http_status_code", "http_route", "peer_service", "error_type", "k8s_namespace", "k8s_cluster", "host"},
				PreferredMeasures: []string{"count", "p95(duration_ns)", "p99(duration_ns)"},
				CommonPatterns: []string{
					"look up one execution by trace_id",
					"find recent failed executions for one service",
					"break down execution failures by operation, http_route, peer_service, error_type, or instance",
				},
				Notes: []string{
					"executions_v1 currently returns only root spans",
					"root span detection treats NULL and empty parent_span_id as root",
					"detail pagination is guaranteed only for default ts DESC, row_id DESC ordering",
					"record is an evidence payload, not the default query surface; promote or materialize important raw attributes before filtering",
				},
			},
		},
		{
			Name:              "measurements_v1",
			Kind:              "measurement",
			Description:       "Numeric observations for anomaly and regression debugging.",
			SourceTable:       "scopedb.otel.metrics",
			TimeField:         "ts",
			DefaultOrderBy:    defaultDetailOrder,
			DefaultLimit:      100,
			MaxLimit:          defaultRelationMax,
			SupportsSearch:    true,
			SupportsAggregate: true,
			Fields:            appendFields(commonScopedFields, "metric_name", "metric_type", "temporality", "unit", "number_value", "record"),
			Anchors:           []string{"service", "metric_name"},
			Measures: []MeasureDef{
				countMeasure,
				countDistinctMeasure,
				numericMeasure("sum", "Sum numeric values."),
				numericMeasure("avg", "Average numeric values."),
				numericMeasure("min", "Minimum numeric value."),
				numericMeasure("max", "Maximum numeric value."),
			},
			Advisory: RelationAdvisory{
				IdentityFields:    []string{"metric_name", "service"},
				AnchorFields:      []string{"service", "metric_name", "instance_id", "k8s_pod"},
				DefaultProject:    []string{"ts", "row_id", "service", "version", "host", "metric_name", "metric_type", "temporality", "unit", "number_value"},
				PreferredFilters:  []string{"env", "service", "version", "metric_name", "unit", "instance_id", "k8s_pod"},
				PreferredGroupBy:  []string{"service", "version", "metric_name", "unit", "k8s_namespace", "k8s_cluster", "host"},
				PreferredMeasures: []string{"count", "avg(number_value)", "max(number_value)", "min(number_value)"},
				CommonPatterns: []string{
					"scan recent raw metric points for one metric_name",
					"group metric points by service or instance_id",
					"bucket numeric observations over time",
				},
				Notes: []string{
					"measurements_v1 is best used with aggregate and time_bucket for trends",
					"raw point search is available but usually less informative than grouped views",
					"record is an evidence payload, not the default query surface; promote or materialize important raw attributes before filtering",
				},
			},
		},
	},
}
