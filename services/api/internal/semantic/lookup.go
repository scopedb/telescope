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

package semantic

import "fmt"

func (r Registry) Field(name string) (FieldSpec, bool) {
	for _, field := range r.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return FieldSpec{}, false
}

func (r Registry) Relation(name string) (RelationSpec, bool) {
	for _, relation := range r.Relations {
		if relation.Name == name {
			return relation, true
		}
	}
	return RelationSpec{}, false
}

func (r Registry) Intent(name string) (IntentSpec, bool) {
	for _, intent := range r.Intents {
		if intent.Name == name {
			return intent, true
		}
	}
	return IntentSpec{}, false
}

func (r Registry) WithAttributeFields(specs ...AttributeFieldSpec) (Registry, error) {
	next := Registry{
		Fields:    append([]FieldSpec(nil), r.Fields...),
		Relations: append([]RelationSpec(nil), r.Relations...),
		Intents:   append([]IntentSpec(nil), r.Intents...),
	}

	for _, spec := range specs {
		field, err := fieldFromAttributeSpec(spec)
		if err != nil {
			return Registry{}, err
		}
		if _, exists := next.Field(field.Name); exists {
			return Registry{}, fmt.Errorf("attribute field %q already exists", field.Name)
		}
		next.Fields = append(next.Fields, field)

		for relationName := range field.ExprByRelation {
			relationIndex := -1
			for index, relation := range next.Relations {
				if relation.Name == relationName {
					relationIndex = index
					break
				}
			}
			if relationIndex < 0 {
				return Registry{}, fmt.Errorf("attribute field %q references unknown relation %q", field.Name, relationName)
			}
			next.Relations[relationIndex].Fields = append(next.Relations[relationIndex].Fields, field.Name)
		}
	}

	if err := next.Validate(); err != nil {
		return Registry{}, err
	}
	return next, nil
}

func fieldFromAttributeSpec(spec AttributeFieldSpec) (FieldSpec, error) {
	name := spec.Name
	if name == "" {
		return FieldSpec{}, fmt.Errorf("attribute field name is required")
	}
	attribute := spec.Attribute
	if attribute == "" {
		attribute = name
	}
	fieldType := spec.Type
	if fieldType == "" {
		fieldType = FieldTypeString
	}
	stability := spec.Stability
	if stability == "" {
		stability = StabilityBeta
	}
	if len(spec.Relations) == 0 {
		return FieldSpec{}, fmt.Errorf("attribute field %q requires at least one relation", name)
	}

	exprs := make(map[string]Expr, len(spec.Relations))
	for _, relation := range spec.Relations {
		exprs[relation] = Attribute(attribute)
	}
	return FieldSpec{
		Name:           name,
		Type:           fieldType,
		Role:           FieldRoleDimension,
		Stability:      stability,
		Description:    spec.Description,
		Filterable:     true,
		Searchable:     spec.Searchable,
		Patternable:    spec.Patternable,
		Groupable:      true,
		ExprByRelation: exprs,
	}, nil
}
