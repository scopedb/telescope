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
