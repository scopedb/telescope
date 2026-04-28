/*
 * Copyright 2026 ScopeDB, Inc.
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

package mcpserver

func toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "health",
			Description: "Return API service health and version.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        "schema",
			Description: "Return the machine-readable semantic telemetry schema. Use this first to discover valid sources, promoted fields, defaults, and advisory query hints.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        "schema_guide",
			Description: "Return the Markdown schema guide for agent planning. Prefer this when bootstrapping without source-code context.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name:        "search",
			Description: "Search detail telemetry rows from events_v1, executions_v1, spans_v1, or measurements_v1. Valid fields come from the schema tool for the selected source; arbitrary record paths are intentionally not accepted.",
			InputSchema: searchInputSchema(),
		},
		{
			Name:        "aggregate",
			Description: "Run grouped or time-bucketed aggregate telemetry queries. Valid fields and measure input types come from the schema tool; sort may also use group aliases or measure aliases. Do not use arbitrary record paths.",
			InputSchema: aggregateInputSchema(),
		},
	}
}

func resourceDefinitions() []resourceDefinition {
	return []resourceDefinition{
		{
			URI:         "scopedb://telemetry/schema",
			Name:        "ScopeDB telemetry schema",
			Description: "Canonical machine-readable schema for telemetry tools.",
			MimeType:    "application/json",
		},
		{
			URI:         "scopedb://telemetry/guide.md",
			Name:        "ScopeDB telemetry guide",
			Description: "Markdown guide generated from the telemetry schema and advisory hints.",
			MimeType:    "text/markdown",
		},
	}
}

func searchInputSchema() map[string]any {
	properties := commonQueryProperties()
	properties["project"] = map[string]any{
		"type":        "array",
		"description": "Fields to return. Valid values are promoted relation.fields from the schema tool for the selected source. If omitted, the source default projection is used.",
		"items":       fieldNameSchema("Projection field name."),
	}
	properties["cursor"] = map[string]any{
		"type":        "string",
		"description": "Opaque next_cursor from a previous default time-top search page. Cursor is only valid with default ts DESC, row_id DESC ordering.",
	}
	properties["debug"] = debugSchema()
	return objectSchema(properties, []string{"source", "time_range"})
}

func aggregateInputSchema() map[string]any {
	properties := commonQueryProperties()
	properties["group_by"] = map[string]any{"type": "array", "items": groupBySchema()}
	properties["measures"] = map[string]any{"type": "array", "items": measureSchema()}
	properties["debug"] = debugSchema()
	return objectSchema(properties, []string{"source", "time_range"})
}

func commonQueryProperties() map[string]any {
	return map[string]any{
		"source": map[string]any{
			"type":        "string",
			"description": "Semantic relation to query. Inspect schema or schema_guide before choosing promoted fields for a source.",
			"enum":        []string{"events_v1", "executions_v1", "spans_v1", "measurements_v1"},
		},
		"time_range": map[string]any{
			"type":                 "object",
			"description":          "Required query time window. Use RFC3339 timestamps.",
			"additionalProperties": false,
			"properties": map[string]any{
				"start": map[string]any{"type": "string", "format": "date-time"},
				"end":   map[string]any{"type": "string", "format": "date-time"},
			},
			"required": []string{"start", "end"},
		},
		"filter": filterSchema(),
		"sort": map[string]any{
			"type":        "array",
			"description": "Sort keys. For search, fields must come from promoted schema relation.fields. For aggregate, fields may also be group aliases or measure aliases.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"field":     fieldNameSchema("Sort field name. Valid fields come from schema; aggregate also accepts output aliases such as count, avg_duration_ns, or ts_5m."),
					"direction": map[string]any{"type": "string", "enum": []string{"asc", "desc"}},
				},
				"required": []string{"field", "direction"},
			},
		},
		"limit": map[string]any{
			"type":        "integer",
			"description": "Maximum rows to return. Server applies source defaults and caps.",
			"minimum":     1,
		},
	}
}

func filterSchema() map[string]any {
	fieldValueTuple := map[string]any{
		"type":     "array",
		"minItems": 2,
		"maxItems": 2,
		"items":    map[string]any{},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Structured FilterExpr. Field names must come from promoted schema relation.fields for the selected source. Arbitrary record paths are not accepted. Use searchable fields with search, and patternable fields with contains/regexp_like.",
		"properties": map[string]any{
			"and":      map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			"or":       map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			"not":      map[string]any{"type": "object"},
			"eq":       fieldValueTuple,
			"in":       fieldValueTuple,
			"gt":       fieldValueTuple,
			"gte":      fieldValueTuple,
			"lt":       fieldValueTuple,
			"lte":      fieldValueTuple,
			"exists":   map[string]any{"type": "string"},
			"search":   fieldValueTuple,
			"contains": fieldValueTuple,
			"regexp_like": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"field":   map[string]any{"type": "string"},
					"pattern": map[string]any{"type": "string"},
					"flags":   map[string]any{"type": "string"},
				},
				"required": []string{"field", "pattern"},
			},
		},
	}
}

func groupBySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Group by a promoted schema field or a time bucket. group_by.field must be groupable in the schema.",
		"properties": map[string]any{
			"field": fieldNameSchema("Groupable field name from schema relation.fields."),
			"time_bucket": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"field":    fieldNameSchema("Timestamp field to bucket. Defaults to the relation time field, usually ts."),
					"interval": map[string]any{"type": "string", "description": "Bucket size accepted by ScopeQL duration parsing, for example 1m, 5m, 15m, 30m, 1h, or 1d."},
				},
				"required": []string{"interval"},
			},
		},
	}
}

func measureSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Aggregate measure. Check schema.measures for valid ops, field_required, input_types, and fields for the selected source. Alias with as when the output name should be stable.",
		"properties": map[string]any{
			"op":    map[string]any{"type": "string", "enum": []string{"count", "count_distinct", "sum", "avg", "min", "max", "p50", "p95", "p99"}},
			"field": fieldNameSchema("Field to aggregate. Valid fields come from schema relation.fields for the selected source."),
			"as":    map[string]any{"type": "string", "description": "Output alias. Sort can reference this alias."},
		},
		"required": []string{"op"},
	}
}

func debugSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"scopeql": map[string]any{"type": "boolean"},
		},
	}
}

func fieldNameSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description + " Field validation errors are returned as pure JSON tool errors with error.details.section, error.details.field, and a hint. Use schema fields, not arbitrary record paths.",
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
