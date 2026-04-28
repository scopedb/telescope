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

import "testing"

func TestRefExprScopeQL(t *testing.T) {
	expr := Ref("trace_id")
	if got := expr.ScopeQL(); got != "`trace_id`" {
		t.Fatalf("unexpected ScopeQL: got %q", got)
	}
}

func TestCallExprScopeQL(t *testing.T) {
	expr := Call("coalesce", Ref("service"), Ref("service_fallback"))
	if got := expr.ScopeQL(); got != "coalesce(`service`, `service_fallback`)" {
		t.Fatalf("unexpected ScopeQL: got %q", got)
	}
}
