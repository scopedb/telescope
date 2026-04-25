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

package semantic

import (
	"errors"
	"fmt"
)

func (r Registry) Validate() error {
	var errs []error

	fieldNames := make(map[string]struct{}, len(r.Fields))
	relationNames := make(map[string]struct{}, len(r.Relations))

	for _, field := range r.Fields {
		if field.Name == "" {
			errs = append(errs, errors.New("field name is required"))
			continue
		}
		if _, exists := fieldNames[field.Name]; exists {
			errs = append(errs, fmt.Errorf("duplicate field %q", field.Name))
		}
		fieldNames[field.Name] = struct{}{}

		if len(field.ExprByRelation) == 0 {
			errs = append(errs, fmt.Errorf("field %q must define at least one expression", field.Name))
			continue
		}
		for relationName, expr := range field.ExprByRelation {
			if expr == nil {
				errs = append(errs, fmt.Errorf("field %q expression for relation %q is nil", field.Name, relationName))
				continue
			}
			if err := expr.Validate(); err != nil {
				errs = append(errs, fmt.Errorf("field %q expression for relation %q: %w", field.Name, relationName, err))
			}
		}
	}

	for _, relation := range r.Relations {
		if relation.Name == "" {
			errs = append(errs, errors.New("relation name is required"))
			continue
		}
		if _, exists := relationNames[relation.Name]; exists {
			errs = append(errs, fmt.Errorf("duplicate relation %q", relation.Name))
		}
		relationNames[relation.Name] = struct{}{}

		relationFieldSet := make(map[string]struct{}, len(relation.Fields))
		for _, fieldName := range relation.Fields {
			if _, ok := fieldNames[fieldName]; !ok {
				errs = append(errs, fmt.Errorf("relation %q references unknown field %q", relation.Name, fieldName))
				continue
			}
			relationFieldSet[fieldName] = struct{}{}
		}

		if relation.TimeField != "" {
			if _, ok := relationFieldSet[relation.TimeField]; !ok {
				errs = append(errs, fmt.Errorf("relation %q time_field %q must be listed in relation fields", relation.Name, relation.TimeField))
			}
		}

		for _, anchor := range relation.Anchors {
			if _, ok := relationFieldSet[anchor]; !ok {
				errs = append(errs, fmt.Errorf("relation %q anchor %q must be listed in relation fields", relation.Name, anchor))
			}
		}
	}

	for _, field := range r.Fields {
		for relationName := range field.ExprByRelation {
			if relationName == "default" {
				continue
			}
			if _, ok := relationNames[relationName]; !ok {
				errs = append(errs, fmt.Errorf("field %q references unknown relation %q", field.Name, relationName))
			}
		}
	}

	for _, intent := range r.Intents {
		if intent.Name == "" {
			errs = append(errs, errors.New("intent name is required"))
			continue
		}
		for _, relationName := range intent.AllowRelations {
			if _, ok := relationNames[relationName]; !ok {
				errs = append(errs, fmt.Errorf("intent %q references unknown relation %q", intent.Name, relationName))
			}
		}
		for _, fieldName := range intent.AllowGroupBy {
			if _, ok := fieldNames[fieldName]; !ok {
				errs = append(errs, fmt.Errorf("intent %q references unknown group_by field %q", intent.Name, fieldName))
			}
		}
	}

	return errors.Join(errs...)
}
