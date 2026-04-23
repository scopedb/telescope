package scopeql

import "testing"

func TestQueryScopeQL(t *testing.T) {
	query := New().
		From("scopedb.otel.traces").
		Where(And(
			Eq(Ref("trace_id"), String("abc")),
			Eq(Ref("status_code"), String("error")),
		)).
		Select(
			Select(Ref("start_timestamp"), "ts"),
			Select(Ref("trace_id"), "trace_id"),
		).
		OrderBy(OrderBy(Ref("ts"), true)).
		Limit(20)

	want := "" +
		"FROM scopedb.otel.traces\n" +
		"WHERE (trace_id = 'abc') AND (status_code = 'error')\n" +
		"SELECT\n" +
		"  start_timestamp AS ts,\n" +
		"  trace_id AS trace_id\n" +
		"ORDER BY ts DESC\n" +
		"LIMIT 20"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestAggregateQueryScopeQL(t *testing.T) {
	query := New().
		From("scopedb.otel.logs").
		Where(Eq(Ref("service_name"), String("checkout"))).
		GroupBy(
			Select(Call("trunc", Ref("record_timestamp"), Raw("unit => 'minute'")), "bucket"),
			Select(Ref("severity_text"), "severity_text"),
		).
		Aggregate(
			Select(Call("count"), "count"),
		).
		Select(
			Select(Ref("bucket"), ""),
			Select(Ref("severity_text"), ""),
			Select(Ref("count"), ""),
		).
		OrderBy(OrderBy(Ref("bucket"), false))

	want := "" +
		"FROM scopedb.otel.logs\n" +
		"WHERE service_name = 'checkout'\n" +
		"GROUP BY trunc(record_timestamp, unit => 'minute') AS bucket, severity_text AS severity_text\n" +
		"AGGREGATE\n" +
		"  count() AS count\n" +
		"SELECT\n" +
		"  bucket,\n" +
		"  severity_text,\n" +
		"  count\n" +
		"ORDER BY bucket ASC"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}
