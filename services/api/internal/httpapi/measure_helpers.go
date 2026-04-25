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
