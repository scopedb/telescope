package semantic

import "testing"

func TestRefExprScopeQL(t *testing.T) {
	expr := Ref("trace_id")
	if got := expr.ScopeQL(); got != "`trace_id`" {
		t.Fatalf("unexpected ScopeQL: got %q", got)
	}
}

func TestCallExprScopeQL(t *testing.T) {
	expr := Call("coalesce", Ref("service_name"), Ref("service_name_fallback"))
	if got := expr.ScopeQL(); got != "coalesce(`service_name`, `service_name_fallback`)" {
		t.Fatalf("unexpected ScopeQL: got %q", got)
	}
}
