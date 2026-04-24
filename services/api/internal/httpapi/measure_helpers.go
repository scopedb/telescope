package httpapi

import "github.com/scopedb/telescope/services/api/internal/semantic"

func relationMeasure(relation semantic.RelationSpec, op string) (semantic.MeasureDef, bool) {
	normalized := normalizeMeasureOp(op)
	for _, measure := range relation.Measures {
		if normalizeMeasureOp(measure.Name) == normalized {
			return measure, true
		}
	}
	return semantic.MeasureDef{}, false
}

func relationMeasureNames(relation semantic.RelationSpec) []string {
	names := make([]string, 0, len(relation.Measures))
	for _, measure := range relation.Measures {
		names = append(names, normalizeMeasureOp(measure.Name))
	}
	return names
}

func measureFields(registry semantic.Registry, relation semantic.RelationSpec, measure semantic.MeasureDef) []string {
	if len(measure.InputTypes) == 0 || (acceptsMeasureType(measure, semantic.FieldTypeAny) && !measure.FieldRequired) {
		return nil
	}

	fields := make([]string, 0)
	for _, fieldName := range relation.Fields {
		field, ok := registry.Field(fieldName)
		if !ok {
			continue
		}
		if measureAcceptsField(measure, field) {
			fields = append(fields, fieldName)
		}
	}
	return fields
}

func (s *Service) measureFieldNames(relation semantic.RelationSpec, measure semantic.MeasureDef) []string {
	return measureFields(s.registry, relation, measure)
}

func measureAcceptsField(measure semantic.MeasureDef, field semantic.FieldSpec) bool {
	if acceptsMeasureType(measure, semantic.FieldTypeAny) {
		return field.Role != semantic.FieldRoleObject
	}
	return field.Role == semantic.FieldRoleMeasure && acceptsMeasureType(measure, field.Type)
}

func acceptsMeasureType(measure semantic.MeasureDef, fieldType semantic.FieldType) bool {
	for _, inputType := range measure.InputTypes {
		if inputType == semantic.FieldTypeAny || inputType == fieldType {
			return true
		}
	}
	return len(measure.InputTypes) == 0
}
