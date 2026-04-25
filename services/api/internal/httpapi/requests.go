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

import (
	"strings"

	"github.com/scopedb/telescope/services/api/internal/semantic"
)

func toSemanticOrders(items []SortRequest) []semantic.OrderSpec {
	out := make([]semantic.OrderSpec, 0, len(items))
	for _, item := range items {
		out = append(out, item.toSemantic())
	}
	return out
}

func toSemanticMeasures(items []MeasureRequest) []semantic.AggregateSpec {
	out := make([]semantic.AggregateSpec, 0, len(items))
	for _, item := range items {
		spec := item.toSemantic()
		spec.Op = normalizeMeasureOp(spec.Op)
		out = append(out, spec)
	}
	return out
}

func toSemanticGroups(items []GroupByRequest) []semantic.GroupBySpec {
	out := make([]semantic.GroupBySpec, 0, len(items))
	for _, item := range items {
		out = append(out, item.toSemantic())
	}
	return out
}

func normalizeMeasureOp(op string) string {
	return strings.ToLower(strings.TrimSpace(op))
}
