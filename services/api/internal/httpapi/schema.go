package httpapi

import (
	"fmt"
	"strings"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/semantic"
)

func schemaResponse(registry semantic.Registry) SchemaResponse {
	response := SchemaResponse{
		Relations: make([]RelationSchema, 0, len(registry.Relations)),
	}

	for _, relation := range registry.Relations {
		response.Relations = append(response.Relations, RelationSchema{
			Name:              relation.Name,
			Kind:              relation.Kind,
			TimeField:         relation.TimeField,
			DefaultSort:       defaultSortForRelation(relation),
			DefaultLimit:      relation.DefaultLimit,
			MaxLimit:          relation.MaxLimit,
			SupportsSearch:    relation.SupportsSearch,
			SupportsAggregate: relation.SupportsAggregate,
			Advisory:          schemaAdvisory(relation.Advisory),
			Fields:            schemaFields(registry, relation),
			Measures:          schemaMeasures(relation.Measures),
		})
	}

	return response
}

func schemaFields(registry semantic.Registry, relation semantic.RelationSpec) []FieldSchema {
	fields := make([]FieldSchema, 0, len(relation.Fields))
	for _, fieldName := range relation.Fields {
		field, ok := registry.Field(fieldName)
		if !ok {
			continue
		}
		fields = append(fields, FieldSchema{
			Name:        field.Name,
			Type:        field.Type,
			Role:        field.Role,
			Filterable:  field.Filterable,
			Searchable:  field.Searchable,
			Patternable: field.Patternable,
			Groupable:   field.Groupable,
		})
	}
	return fields
}

func schemaMeasures(measures []semantic.MeasureDef) []MeasureSchema {
	out := make([]MeasureSchema, 0, len(measures))
	for _, measure := range measures {
		out = append(out, MeasureSchema{
			Name:        measure.Name,
			Description: measure.Description,
		})
	}
	return out
}

func schemaAdvisory(advisory semantic.RelationAdvisory) RelationAdvisory {
	return RelationAdvisory{
		IdentityFields:    append([]string(nil), advisory.IdentityFields...),
		AnchorFields:      append([]string(nil), advisory.AnchorFields...),
		DefaultProject:    append([]string(nil), advisory.DefaultProject...),
		PreferredFilters:  append([]string(nil), advisory.PreferredFilters...),
		PreferredGroupBy:  append([]string(nil), advisory.PreferredGroupBy...),
		PreferredMeasures: append([]string(nil), advisory.PreferredMeasures...),
		CommonPatterns:    append([]string(nil), advisory.CommonPatterns...),
		Notes:             append([]string(nil), advisory.Notes...),
	}
}

func renderSchemaGuide(registry semantic.Registry) string {
	var builder strings.Builder

	builder.WriteString("# ScopeDB OTel Schema Guide\n\n")
	builder.WriteString("This guide is generated from the canonical JSON schema. Use `/v1/schema` for machine-readable introspection and this document for agent-friendly planning hints.\n")

	for _, relation := range registry.Relations {
		builder.WriteString("\n## `")
		builder.WriteString(relation.Name)
		builder.WriteString("`\n\n")
		builder.WriteString("- Kind: `")
		builder.WriteString(relation.Kind)
		builder.WriteString("`\n")
		builder.WriteString("- Time field: `")
		builder.WriteString(relation.TimeField)
		builder.WriteString("`\n")
		builder.WriteString("- Default sort: `")
		builder.WriteString(relation.DefaultOrderBy)
		builder.WriteString("`\n")
		builder.WriteString("- Default limit: `")
		builder.WriteString(fmt.Sprintf("%d", relation.DefaultLimit))
		builder.WriteString("`\n")
		if relation.Description != "" {
			builder.WriteString("- Purpose: ")
			builder.WriteString(relation.Description)
			builder.WriteString("\n")
		}

		writeMarkdownList(&builder, "Identity fields", relation.Advisory.IdentityFields, true)
		writeMarkdownList(&builder, "Anchor fields", relation.Advisory.AnchorFields, true)
		writeMarkdownList(&builder, "Default project", relation.Advisory.DefaultProject, true)
		writeMarkdownList(&builder, "Preferred filters", relation.Advisory.PreferredFilters, true)
		writeMarkdownList(&builder, "Preferred group by", relation.Advisory.PreferredGroupBy, true)
		writeMarkdownList(&builder, "Preferred measures", relation.Advisory.PreferredMeasures, true)
		writeMarkdownList(&builder, "Common patterns", relation.Advisory.CommonPatterns, false)
		writeMarkdownList(&builder, "Notes", relation.Advisory.Notes, false)
	}

	return builder.String()
}

func writeMarkdownList(builder *strings.Builder, title string, values []string, code bool) {
	if len(values) == 0 {
		return
	}
	builder.WriteString("\n### ")
	builder.WriteString(title)
	builder.WriteString("\n\n")
	for _, value := range values {
		builder.WriteString("- ")
		if code {
			builder.WriteString("`")
			builder.WriteString(value)
			builder.WriteString("`")
		} else {
			builder.WriteString(value)
		}
		builder.WriteString("\n")
	}
}

func defaultSortForRelation(relation semantic.RelationSpec) []SortResponse {
	sort := make([]SortResponse, 0, 2)
	if relation.TimeField != "" {
		sort = append(sort, SortResponse{Field: relation.TimeField, Direction: "desc"})
	}
	for _, field := range relation.Fields {
		if field == "row_id" {
			sort = append(sort, SortResponse{Field: "row_id", Direction: "desc"})
			break
		}
	}
	return sort
}

func searchAppliedSort(spec semantic.QuerySpec, relation semantic.RelationSpec) []SortResponse {
	if len(spec.OrderBy) == 0 {
		return defaultSortForRelation(relation)
	}

	out := make([]SortResponse, 0, len(spec.OrderBy)+1)
	for _, order := range spec.OrderBy {
		out = append(out, SortResponse{
			Field:     order.Field,
			Direction: normalizeDirection(order.Direction),
		})
	}
	if !containsSortField(spec.OrderBy, "row_id") && relationHasField(relation, "row_id") {
		out = append(out, SortResponse{Field: "row_id", Direction: "desc"})
	}
	return out
}

func containsSortField(orders []semantic.OrderSpec, field string) bool {
	for _, order := range orders {
		if order.Field == field {
			return true
		}
	}
	return false
}

func relationHasField(relation semantic.RelationSpec, field string) bool {
	for _, candidate := range relation.Fields {
		if candidate == field {
			return true
		}
	}
	return false
}
