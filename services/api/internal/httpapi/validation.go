package httpapi

import (
	"fmt"
	"strings"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/semantic"
)

type fieldCapability string

const (
	fieldCapabilityAny       fieldCapability = ""
	fieldCapabilityFilter    fieldCapability = "filter"
	fieldCapabilitySearch    fieldCapability = "search"
	fieldCapabilityPattern   fieldCapability = "pattern"
	fieldCapabilityGroup     fieldCapability = "group"
	fieldCapabilityAggregate fieldCapability = "aggregate"
)

func (s *Service) validateSearchRequest(relation semantic.RelationSpec, request SearchRequest) *ServiceError {
	for _, field := range request.Project {
		if err := s.validateRelationField(relation, field, "project", fieldCapabilityAny); err != nil {
			return err
		}
	}
	for _, sort := range request.Sort {
		if err := s.validateRelationField(relation, sort.Field, "sort", fieldCapabilityAny); err != nil {
			return err
		}
	}
	return s.validateFilter(relation, request.Filter)
}

func (s *Service) validateAggregateRequest(relation semantic.RelationSpec, request AggregateRequest) *ServiceError {
	if err := s.validateFilter(relation, request.Filter); err != nil {
		return err
	}
	for _, group := range request.GroupBy {
		switch {
		case strings.TrimSpace(group.Field) != "":
			if err := s.validateRelationField(relation, group.Field, "group_by", fieldCapabilityGroup); err != nil {
				return err
			}
		case group.TimeBucket != nil:
			field := strings.TrimSpace(group.TimeBucket.Field)
			if field == "" {
				field = relation.TimeField
			}
			if err := s.validateRelationField(relation, field, "group_by", fieldCapabilityGroup); err != nil {
				return err
			}
		default:
			return validationError("group_by entry must contain field or time_bucket", "group_by", "", "")
		}
	}
	for _, measure := range request.Measures {
		if strings.TrimSpace(measure.Field) == "" {
			continue
		}
		if err := s.validateRelationField(relation, measure.Field, "measure", fieldCapabilityAggregate); err != nil {
			return err
		}
	}

	sortAliases := aggregateSortAliases(relation, request)
	for _, sort := range request.Sort {
		field := strings.TrimSpace(sort.Field)
		if _, ok := sortAliases[field]; ok {
			continue
		}
		if err := s.validateRelationField(relation, field, "sort", fieldCapabilityAny); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateFilter(relation semantic.RelationSpec, filter *semantic.FilterExpr) *ServiceError {
	if filter == nil {
		return nil
	}

	switch filter.Kind() {
	case semantic.FilterKindAnd, semantic.FilterKindOr, semantic.FilterKindNot:
		for _, child := range filter.Children() {
			if err := s.validateFilter(relation, child); err != nil {
				return err
			}
		}
	case semantic.FilterKindEq, semantic.FilterKindIn, semantic.FilterKindGt, semantic.FilterKindGte, semantic.FilterKindLt, semantic.FilterKindLte:
		return s.validateRelationField(relation, filter.Field(), "filter", fieldCapabilityFilter)
	case semantic.FilterKindExists:
		return s.validateRelationField(relation, filter.Field(), "filter", fieldCapabilityAny)
	case semantic.FilterKindSearch:
		return s.validateRelationField(relation, filter.Field(), "filter", fieldCapabilitySearch)
	case semantic.FilterKindContains, semantic.FilterKindRegexpLike:
		return s.validateRelationField(relation, filter.Field(), "filter", fieldCapabilityPattern)
	default:
		return validationError(fmt.Sprintf("unsupported filter operator: %s", filter.Kind()), "filter", filter.Field(), string(filter.Kind()))
	}
	return nil
}

func (s *Service) validateRelationField(relation semantic.RelationSpec, fieldName string, section string, capability fieldCapability) *ServiceError {
	fieldName = strings.TrimSpace(fieldName)
	if fieldName == "" {
		return validationError(section+" field is required", section, "", "")
	}

	field, ok := s.registry.Field(fieldName)
	if !ok || !relationHasField(relation, fieldName) {
		return validationError(fmt.Sprintf("unknown %s field: %s", section, fieldName), section, fieldName, "")
	}
	if _, ok := field.ExprForRelation(relation.Name); !ok {
		return validationError(fmt.Sprintf("field is not available for relation: %s", fieldName), section, fieldName, relation.Name)
	}

	switch capability {
	case fieldCapabilityFilter:
		if !field.Filterable {
			return validationError(fmt.Sprintf("field does not support filter: %s", fieldName), section, fieldName, string(capability))
		}
	case fieldCapabilitySearch:
		if !field.Searchable {
			return validationError(fmt.Sprintf("field does not support search: %s", fieldName), section, fieldName, string(capability))
		}
	case fieldCapabilityPattern:
		if !field.Patternable {
			return validationError(fmt.Sprintf("field does not support pattern filter: %s", fieldName), section, fieldName, string(capability))
		}
	case fieldCapabilityGroup:
		if !field.Groupable {
			return validationError(fmt.Sprintf("field is not groupable: %s", fieldName), section, fieldName, string(capability))
		}
	}
	return nil
}

func aggregateSortAliases(relation semantic.RelationSpec, request AggregateRequest) map[string]struct{} {
	aliases := make(map[string]struct{})
	for _, group := range request.GroupBy {
		if field := strings.TrimSpace(group.Field); field != "" {
			aliases[field] = struct{}{}
			continue
		}
		if group.TimeBucket == nil {
			continue
		}
		field := strings.TrimSpace(group.TimeBucket.Field)
		if field == "" {
			field = relation.TimeField
		}
		if field != "" {
			aliases[field] = struct{}{}
			aliases[field+"_"+bucketAliasSuffix(group.TimeBucket.Interval)] = struct{}{}
		}
	}

	measures := request.Measures
	if len(measures) == 0 {
		measures = []MeasureRequest{{Op: "count", Alias: "count"}}
	}
	for _, measure := range measures {
		alias := strings.TrimSpace(measure.Alias)
		if alias == "" {
			alias = defaultMeasureAlias(measure)
		}
		if alias != "" {
			aliases[alias] = struct{}{}
		}
	}
	return aliases
}

func defaultMeasureAlias(measure MeasureRequest) string {
	op := strings.TrimSpace(measure.Op)
	field := strings.TrimSpace(measure.Field)
	if field == "" {
		return op
	}
	return op + "_" + field
}

func bucketAliasSuffix(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "bucket"
	}

	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			builder.WriteRune(r)
			lastUnderscore = false
		default:
			if lastUnderscore {
				continue
			}
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}

	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "bucket"
	}
	return result
}

func validationError(message string, section string, field string, capability string) *ServiceError {
	details := map[string]any{
		"section": section,
	}
	if field != "" {
		details["field"] = field
		details["hint"] = "Use schema or schema_guide to inspect promoted semantic fields for the selected source. Arbitrary record paths are not accepted by search or aggregate filters; promote or materialize important raw attributes first."
	}
	if capability != "" {
		details["capability"] = capability
	}
	return badRequest(message, details)
}
