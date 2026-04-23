package semantic

import "testing"

func TestRefExprScopeQL(t *testing.T) {
	expr := Ref("trace_id")
	if got := expr.ScopeQL(); got != "trace_id" {
		t.Fatalf("unexpected ScopeQL: got %q", got)
	}
}

func TestCallExprScopeQL(t *testing.T) {
	expr := Call("coalesce", Ref("service_name"), Ref("record['resource']['service.name']"))
	if got := expr.ScopeQL(); got != "coalesce(service_name, record['resource']['service.name'])" {
		t.Fatalf("unexpected ScopeQL: got %q", got)
	}
}
