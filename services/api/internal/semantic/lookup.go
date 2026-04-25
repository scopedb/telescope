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
