package semantic

const (
	defaultDetailOrder = "ts DESC, row_id DESC"
	defaultRelationMax = 1000
)

var (
	commonScopedFields = []string{
		"ts",
		"row_id",
		"dataset",
		"service_name",
		"instance_id",
		"pod_name",
		"host_ip",
		"host_name",
	}

	countMeasure = MeasureDef{Name: "count", Description: "Count rows."}
)

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
	return FieldSpec{
		Name:        name,
		Type:        FieldTypeString,
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
		defaultDimensionField("dataset", "Logical dataset name.", false, false),
		defaultDimensionField("service_name", "Canonical service name.", true, true),
		defaultDimensionField("instance_id", "OTel-native service instance identity when present.", true, true),
		defaultDimensionField("pod_name", "Kubernetes pod name when present.", true, true),
		defaultDimensionField("host_ip", "Host IP when present.", false, true),
		defaultDimensionField("host_name", "Host name when present.", true, true),
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
		relationDimensionField("severity_text", "events_v1", "severity_text", "Log severity text.", StabilityCore, true, true),
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
		relationDimensionField("status_code", "executions_v1", "status_code", "Execution outcome status code.", StabilityCore, false, false),
		relationMeasureField("duration_ns", "executions_v1", "duration_ns", "Execution duration in nanoseconds.", FieldTypeInt),
		relationDimensionField("metric_name", "measurements_v1", "metric_name", "Metric name.", StabilityCore, true, true),
		relationDimensionField("metric_type", "measurements_v1", "metric_type", "Metric type.", StabilityCore, false, false),
		relationDimensionField("temporality", "measurements_v1", "temporality", "Metric aggregation temporality.", StabilityCore, false, false),
		relationMeasureField("number_value", "measurements_v1", "number_value", "Scalar metric value when available.", FieldTypeFloat),
		{
			Name:        "record",
			Type:        FieldTypeObject,
			Role:        FieldRoleObject,
			Stability:   StabilityCore,
			Description: "Original mapped signal payload.",
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
			Fields:            appendFields(commonScopedFields, "trace_id", "execution_id", "span_id", "severity_text", "message", "record"),
			Anchors:           []string{"trace_id", "execution_id"},
			Measures: []MeasureDef{
				countMeasure,
			},
			Advisory: RelationAdvisory{
				IdentityFields:    []string{"trace_id", "span_id"},
				AnchorFields:      []string{"trace_id", "execution_id", "service_name"},
				DefaultProject:    []string{"ts", "row_id", "service_name", "instance_id", "pod_name", "trace_id", "span_id", "severity_text", "message"},
				PreferredFilters:  []string{"service_name", "trace_id", "execution_id", "severity_text", "message"},
				PreferredGroupBy:  []string{"service_name", "severity_text", "instance_id", "pod_name"},
				PreferredMeasures: []string{"count"},
				CommonPatterns: []string{
					"find recent error events for one service",
					"find events near a known trace_id",
					"break down error volume by severity_text or instance_id",
				},
				Notes: []string{
					"events_v1 is row-oriented and best suited for detail search plus lightweight breakdowns",
					"message supports search, contains, and regexp_like filters",
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
			Fields:            appendFields(commonScopedFields, "trace_id", "execution_id", "span_id", "parent_span_id", "operation", "status_code", "duration_ns", "record"),
			Anchors:           []string{"trace_id", "execution_id"},
			Measures: []MeasureDef{
				countMeasure,
				{Name: "p50", Description: "Median duration."},
				{Name: "p95", Description: "95th percentile duration."},
				{Name: "p99", Description: "99th percentile duration."},
			},
			Advisory: RelationAdvisory{
				IdentityFields:    []string{"trace_id"},
				AnchorFields:      []string{"trace_id", "execution_id", "service_name", "instance_id", "pod_name"},
				DefaultProject:    []string{"ts", "row_id", "service_name", "instance_id", "pod_name", "host_name", "host_ip", "trace_id", "operation", "status_code", "duration_ns"},
				PreferredFilters:  []string{"trace_id", "execution_id", "service_name", "instance_id", "pod_name", "status_code"},
				PreferredGroupBy:  []string{"service_name", "operation", "status_code", "instance_id", "pod_name", "host_name"},
				PreferredMeasures: []string{"count", "p95(duration_ns)", "p99(duration_ns)"},
				CommonPatterns: []string{
					"look up one execution by trace_id",
					"find recent failed executions for one service",
					"break down execution failures by operation or instance",
				},
				Notes: []string{
					"executions_v1 currently returns only root spans",
					"root span detection treats NULL and empty parent_span_id as root",
					"detail pagination is guaranteed only for default ts DESC, row_id DESC ordering",
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
			Fields:            appendFields(commonScopedFields, "metric_name", "metric_type", "temporality", "number_value", "record"),
			Anchors:           []string{"service_name", "metric_name"},
			Measures: []MeasureDef{
				countMeasure,
				{Name: "sum", Description: "Sum numeric values."},
				{Name: "avg", Description: "Average numeric values."},
				{Name: "min", Description: "Minimum numeric value."},
				{Name: "max", Description: "Maximum numeric value."},
			},
			Advisory: RelationAdvisory{
				IdentityFields:    []string{"metric_name", "service_name"},
				AnchorFields:      []string{"service_name", "metric_name", "instance_id", "pod_name"},
				DefaultProject:    []string{"ts", "row_id", "service_name", "instance_id", "pod_name", "metric_name", "metric_type", "temporality", "number_value"},
				PreferredFilters:  []string{"service_name", "metric_name", "instance_id", "pod_name"},
				PreferredGroupBy:  []string{"service_name", "metric_name", "instance_id", "pod_name", "host_name"},
				PreferredMeasures: []string{"count", "avg(number_value)", "max(number_value)", "min(number_value)"},
				CommonPatterns: []string{
					"scan recent raw metric points for one metric_name",
					"group metric points by service_name or instance_id",
					"bucket numeric observations over time",
				},
				Notes: []string{
					"measurements_v1 is best used with aggregate and time_bucket for trends",
					"raw point search is available but usually less informative than grouped views",
				},
			},
		},
	},
}
