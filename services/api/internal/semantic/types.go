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

type FieldType string

const (
	FieldTypeString    FieldType = "string"
	FieldTypeInt       FieldType = "int"
	FieldTypeFloat     FieldType = "float"
	FieldTypeBool      FieldType = "bool"
	FieldTypeTimestamp FieldType = "timestamp"
	FieldTypeObject    FieldType = "object"
	FieldTypeArray     FieldType = "array"
	FieldTypeAny       FieldType = "any"
)

type FieldRole string

const (
	FieldRoleTime       FieldRole = "time"
	FieldRoleTieBreaker FieldRole = "tie_breaker"
	FieldRoleDimension  FieldRole = "dimension"
	FieldRoleMeasure    FieldRole = "measure"
	FieldRoleValue      FieldRole = "value"
	FieldRoleObject     FieldRole = "object"
)

type Stability string

const (
	StabilityCore Stability = "core"
	StabilityBeta Stability = "beta"
)

type FieldSpec struct {
	Name           string
	Type           FieldType
	Role           FieldRole
	Stability      Stability
	Description    string
	Filterable     bool
	Searchable     bool
	Patternable    bool
	Groupable      bool
	ExprByRelation map[string]Expr
}

func (f FieldSpec) ExprForRelation(relation string) (Expr, bool) {
	if expr, ok := f.ExprByRelation[relation]; ok {
		return expr, true
	}
	expr, ok := f.ExprByRelation["default"]
	return expr, ok
}

type RelationSpec struct {
	Name              string
	Kind              string
	Description       string
	SourceTable       string
	Where             string
	TimeField         string
	DefaultOrderBy    string
	DefaultLimit      int
	MaxLimit          int
	SupportsSearch    bool
	SupportsAggregate bool
	Fields            []string
	Anchors           []string
	Measures          []MeasureDef
	Advisory          RelationAdvisory
}

type MeasureDef struct {
	Name          string
	Description   string
	FieldRequired bool
	InputTypes    []FieldType
}

type RelationAdvisory struct {
	IdentityFields    []string
	AnchorFields      []string
	DefaultProject    []string
	PreferredFilters  []string
	PreferredGroupBy  []string
	PreferredMeasures []string
	CommonPatterns    []string
	Notes             []string
}

type IntentSpec struct {
	Name            string
	Description     string
	AllowRelations  []string
	AllowBucket     bool
	AllowFilters    bool
	AllowGroupBy    []string
	DefaultMeasures []string
}

type Registry struct {
	Fields    []FieldSpec
	Relations []RelationSpec
	Intents   []IntentSpec
}
