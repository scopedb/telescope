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
	"fmt"
	"strings"

	"github.com/scopedb/telescope/services/api/internal/semantic"
)

const slockSemanticProfile = "slock"

func SemanticRegistryForProfile(profile string) (semantic.Registry, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "default":
		return semantic.Default, nil
	case slockSemanticProfile:
		return semantic.Default.WithAttributeFields(slockAttributeFields()...)
	default:
		return semantic.Registry{}, fmt.Errorf("unknown TELESCOPE_SEMANTIC_PROFILE %q", profile)
	}
}

func slockAttributeFields() []semantic.AttributeFieldSpec {
	relations := []string{"executions_v1", "spans_v1"}
	return []semantic.AttributeFieldSpec{
		{Name: "route_pattern", Description: "Slock normalized route pattern from request observability spans.", Relations: relations, Searchable: true, Patternable: true},
		{Name: "caller_kind", Description: "Slock caller classification such as human, agent, or system.", Relations: relations},
		{Name: "agent_id_present", Type: semantic.FieldTypeBool, Description: "Whether the Slock trace has an agent identifier in context without exposing the raw id.", Relations: relations},
		{Name: "server_id_present", Type: semantic.FieldTypeBool, Description: "Whether the Slock trace has a server identifier in context without exposing the raw id.", Relations: relations},
		{Name: "user_id_present", Type: semantic.FieldTypeBool, Description: "Whether the Slock trace has a user identifier in context without exposing the raw id.", Relations: relations},
		{Name: "machine_id_present", Type: semantic.FieldTypeBool, Description: "Whether the Slock trace has a machine identifier in context without exposing the raw id.", Relations: relations},
		{Name: "daemon_version_present", Type: semantic.FieldTypeBool, Description: "Whether the Slock trace has daemon version context.", Relations: relations},
		{Name: "daemon_version", Description: "Slock daemon semantic version when available.", Relations: relations},
		{Name: "behavior_span", Type: semantic.FieldTypeBool, Description: "Whether the span is a Slock analytics-grade behavior span.", Relations: relations},
		{Name: "behavior_event", Type: semantic.FieldTypeBool, Description: "Whether top-level trace attributes mark an analytics-grade behavior event.", Relations: relations},
		{Name: "behavior_event_version", Type: semantic.FieldTypeInt, Description: "Slock analytics-grade behavior event contract version.", Relations: relations},
		{Name: "event_type", Description: "Slock analytics-grade behavior event type when promoted as a top-level trace attribute.", Relations: relations, Searchable: true, Patternable: true},
		{Name: "event_source", Description: "Slock behavior event source such as daemon, server, scheduler, user_api, or system.", Relations: relations},
		{Name: "server_key", Description: "Stable Slock server surrogate key for behavior analytics.", Relations: relations, Searchable: true, Patternable: true},
		{Name: "agent_key", Description: "Stable Slock agent surrogate key for behavior analytics.", Relations: relations, Searchable: true, Patternable: true},
	}
}
