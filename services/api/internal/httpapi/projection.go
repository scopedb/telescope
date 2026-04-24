package httpapi

import (
	"strings"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/semantic"
)

func searchInternalProject(relation semantic.RelationSpec, request SearchRequest) []string {
	if len(request.Project) == 0 {
		return nil
	}

	fields := make([]string, 0, len(request.Project)+len(request.Sort)+2)
	seen := make(map[string]struct{})
	appendField := func(field string) {
		field = strings.TrimSpace(field)
		if field == "" {
			return
		}
		if _, ok := seen[field]; ok {
			return
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}

	for _, field := range request.Project {
		appendField(field)
	}

	if len(request.Sort) == 0 {
		appendField(relation.TimeField)
	} else {
		for _, sort := range request.Sort {
			appendField(sort.Field)
		}
	}
	if relationHasField(relation, "row_id") && !containsSortRequestField(request.Sort, "row_id") {
		appendField("row_id")
	}
	return fields
}

func trimRowsToProject(rows []map[string]any, project []string) []map[string]any {
	if len(project) == 0 {
		return rows
	}

	allowed := make(map[string]struct{}, len(project))
	for _, field := range project {
		field = strings.TrimSpace(field)
		if field != "" {
			allowed[field] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return rows
	}

	trimmed := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		next := make(map[string]any, len(allowed))
		for field := range allowed {
			if value, ok := row[field]; ok {
				next[field] = value
			}
		}
		trimmed = append(trimmed, next)
	}
	return trimmed
}

func containsSortRequestField(orders []SortRequest, field string) bool {
	for _, order := range orders {
		if strings.TrimSpace(order.Field) == field {
			return true
		}
	}
	return false
}
