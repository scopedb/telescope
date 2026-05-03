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

package appruntime

import (
	"testing"

	"github.com/scopedb/telescope/services/api/internal/semantic"
)

func TestSemanticRegistryForProfileDefaultOmitsSlockFields(t *testing.T) {
	registry, err := SemanticRegistryForProfile("")
	if err != nil {
		t.Fatalf("SemanticRegistryForProfile(default): %v", err)
	}
	if _, ok := registry.Field("route_pattern"); ok {
		t.Fatal("default registry should not include Slock-specific fields")
	}
}

func TestSemanticRegistryForProfileSlockPromotesAttributeFields(t *testing.T) {
	registry, err := SemanticRegistryForProfile("slock")
	if err != nil {
		t.Fatalf("SemanticRegistryForProfile(slock): %v", err)
	}

	assertField := func(name string, fieldType semantic.FieldType) {
		t.Helper()
		field, ok := registry.Field(name)
		if !ok {
			t.Fatalf("missing field %q", name)
		}
		if field.Type != fieldType {
			t.Fatalf("field %q type = %q, want %q", name, field.Type, fieldType)
		}
		if _, ok := field.ExprForRelation("executions_v1"); !ok {
			t.Fatalf("field %q missing executions_v1 expression", name)
		}
		if _, ok := field.ExprForRelation("spans_v1"); !ok {
			t.Fatalf("field %q missing spans_v1 expression", name)
		}
	}

	assertField("route_pattern", semantic.FieldTypeString)
	assertField("caller_kind", semantic.FieldTypeString)
	assertField("daemon_version", semantic.FieldTypeString)
	assertField("behavior_span", semantic.FieldTypeBool)
	assertField("behavior_event_version", semantic.FieldTypeInt)
	assertField("server_key", semantic.FieldTypeString)
	assertField("agent_key", semantic.FieldTypeString)

	executions, ok := registry.Relation("executions_v1")
	if !ok {
		t.Fatal("missing executions_v1 relation")
	}
	if !containsField(executions.Fields, "route_pattern") || !containsField(executions.Fields, "agent_key") {
		t.Fatalf("executions_v1 missing promoted Slock fields: %#v", executions.Fields)
	}
}

func TestSemanticRegistryForProfileRejectsUnknownProfile(t *testing.T) {
	_, err := SemanticRegistryForProfile("unknown")
	if err == nil {
		t.Fatal("expected error")
	}
}

func containsField(fields []string, target string) bool {
	for _, field := range fields {
		if field == target {
			return true
		}
	}
	return false
}
