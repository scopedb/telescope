package semantic

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildDefaultSearchQuery(t *testing.T) {
	query, err := Default.BuildQuery(QuerySpec{
		Relation: "events_v1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("build query: %v", err)
	}

	want := "" +
		"FROM `scopedb`.`otel`.`logs`\n" +
		"SELECT\n" +
		"  `record_timestamp` AS `ts`,\n" +
		"  `row_id` AS `row_id`,\n" +
		"  `dataset` AS `dataset`,\n" +
		"  `service_name` AS `service_name`,\n" +
		"  `instance_id` AS `instance_id`,\n" +
		"  `pod_name` AS `pod_name`,\n" +
		"  `host_ip` AS `host_ip`,\n" +
		"  `host_name` AS `host_name`,\n" +
		"  `trace_id` AS `trace_id`,\n" +
		"  `trace_id` AS `execution_id`,\n" +
		"  `span_id` AS `span_id`,\n" +
		"  `severity_text` AS `severity_text`,\n" +
		"  `message` AS `message`,\n" +
		"  `record` AS `record`\n" +
		"ORDER BY `ts` DESC, `row_id` DESC\n" +
		"LIMIT 10"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestBuildAggregateQuery(t *testing.T) {
	filter := mustFilter(t, `{"eq":["service_name","checkout"]}`)

	query, err := Default.BuildQuery(QuerySpec{
		Relation: "executions_v1",
		Filter:   filter,
		GroupBy: []GroupBySpec{
			{Field: "operation"},
			{Field: "status_code"},
		},
		Aggregates: []AggregateSpec{
			{Op: "count", Alias: "count"},
		},
		OrderBy: []OrderSpec{
			{Field: "count", Direction: "desc"},
		},
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("build query: %v", err)
	}

	want := "" +
		"FROM `scopedb`.`otel`.`traces`\n" +
		"WHERE ((parent_span_id IS NULL) OR (parent_span_id = '')) AND (`service_name` = 'checkout')\n" +
		"GROUP BY `span_name` AS `operation`, `status_code` AS `status_code`\n" +
		"AGGREGATE\n" +
		"  count() AS `count`\n" +
		"SELECT\n" +
		"  `operation`,\n" +
		"  `status_code`,\n" +
		"  `count`\n" +
		"ORDER BY `count` DESC\n" +
		"LIMIT 20"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestBuildQueryWithTimeRangeAndRowIDTieBreaker(t *testing.T) {
	start := time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 23, 1, 0, 0, 0, time.UTC)

	query, err := Default.BuildQuery(QuerySpec{
		Relation: "events_v1",
		TimeRange: &TimeRange{
			Start: &start,
			End:   &end,
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("build query: %v", err)
	}

	want := "" +
		"FROM `scopedb`.`otel`.`logs`\n" +
		"WHERE (`record_timestamp` >= '2026-04-23T00:00:00Z'::timestamp) AND (`record_timestamp` < '2026-04-23T01:00:00Z'::timestamp)\n" +
		"SELECT\n" +
		"  `record_timestamp` AS `ts`,\n" +
		"  `row_id` AS `row_id`,\n" +
		"  `dataset` AS `dataset`,\n" +
		"  `service_name` AS `service_name`,\n" +
		"  `instance_id` AS `instance_id`,\n" +
		"  `pod_name` AS `pod_name`,\n" +
		"  `host_ip` AS `host_ip`,\n" +
		"  `host_name` AS `host_name`,\n" +
		"  `trace_id` AS `trace_id`,\n" +
		"  `trace_id` AS `execution_id`,\n" +
		"  `span_id` AS `span_id`,\n" +
		"  `severity_text` AS `severity_text`,\n" +
		"  `message` AS `message`,\n" +
		"  `record` AS `record`\n" +
		"ORDER BY `ts` DESC, `row_id` DESC\n" +
		"LIMIT 10"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestBuildQueryWithSearchAndRegexpFilters(t *testing.T) {
	filter := mustFilter(t, `{
	  "and": [
	    {"eq": ["service_name", "checkout"]},
	    {"search": ["message", "payment timeout"]},
	    {"regexp_like": {"field":"message","pattern":"timeout|deadline","flags":"i"}}
	  ]
	}`)

	query, err := Default.BuildQuery(QuerySpec{
		Relation: "events_v1",
		Filter:   filter,
		Fields:   []string{"ts", "row_id", "message"},
		OrderBy:  []OrderSpec{{Field: "ts", Direction: "desc"}},
		Limit:    5,
	})
	if err != nil {
		t.Fatalf("build query: %v", err)
	}

	want := "" +
		"FROM `scopedb`.`otel`.`logs`\n" +
		"WHERE (`service_name` = 'checkout') AND (search(`message`, 'payment timeout')) AND (regexp_like(`message`, '(?i)timeout|deadline'))\n" +
		"SELECT\n" +
		"  `record_timestamp` AS `ts`,\n" +
		"  `row_id` AS `row_id`,\n" +
		"  `message` AS `message`\n" +
		"ORDER BY `ts` DESC, `row_id` DESC\n" +
		"LIMIT 5"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestBuildAggregateQueryWithFiveMinuteBucket(t *testing.T) {
	filter := mustFilter(t, `{"eq":["service_name","checkout"]}`)

	query, err := Default.BuildQuery(QuerySpec{
		Relation: "executions_v1",
		TimeRange: &TimeRange{
			Start: ptrTime(time.Date(2026, 4, 23, 0, 0, 0, 0, time.UTC)),
			End:   ptrTime(time.Date(2026, 4, 23, 1, 0, 0, 0, time.UTC)),
		},
		Filter: filter,
		GroupBy: []GroupBySpec{
			{TimeBucket: &TimeBucketSpec{Field: "ts", Interval: "5m"}},
			{Field: "service_name"},
		},
		Aggregates: []AggregateSpec{
			{Op: "count", Alias: "count"},
			{Op: "avg", Field: "duration_ns", Alias: "avg_duration_ns"},
		},
		OrderBy: []OrderSpec{
			{Field: "ts", Direction: "desc"},
		},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("build query: %v", err)
	}

	want := "" +
		"FROM `scopedb`.`otel`.`traces`\n" +
		"WHERE ((parent_span_id IS NULL) OR (parent_span_id = '')) AND (`service_name` = 'checkout') AND (`start_timestamp` >= '2026-04-23T00:00:00Z'::timestamp) AND (`start_timestamp` < '2026-04-23T01:00:00Z'::timestamp)\n" +
		"GROUP BY floor(`start_timestamp`, n => 5, unit => 'minute') AS `ts_5m`, `service_name` AS `service_name`\n" +
		"AGGREGATE\n" +
		"  count() AS `count`,\n" +
		"  avg(`duration_ns`) AS `avg_duration_ns`\n" +
		"SELECT\n" +
		"  `ts_5m`,\n" +
		"  `service_name`,\n" +
		"  `count`,\n" +
		"  `avg_duration_ns`\n" +
		"ORDER BY `ts_5m` DESC\n" +
		"LIMIT 10"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestBucketIntervalToArgsParsesArbitraryDurations(t *testing.T) {
	tests := []struct {
		input string
		unit  string
		n     int
	}{
		{input: "7m", unit: "minute", n: 7},
		{input: "90m", unit: "minute", n: 90},
		{input: "2h", unit: "hour", n: 2},
		{input: "24h", unit: "hour", n: 24},
		{input: "1d", unit: "hour", n: 24},
		{input: "1500ms", unit: "millisecond", n: 1500},
		{input: "90s", unit: "second", n: 90},
	}

	for _, tt := range tests {
		unit, n, err := bucketIntervalToArgs(tt.input)
		if err != nil {
			t.Fatalf("bucketIntervalToArgs(%q): %v", tt.input, err)
		}
		if unit != tt.unit || n != tt.n {
			t.Fatalf("bucketIntervalToArgs(%q) = (%q, %d), want (%q, %d)", tt.input, unit, n, tt.unit, tt.n)
		}
	}
}

func TestBucketAliasSuffixSanitizesInterval(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "5m", want: "5m"},
		{input: "1.5h", want: "1_5h"},
		{input: " 24h ", want: "24h"},
		{input: "1/2m", want: "1_2m"},
	}

	for _, tt := range tests {
		if got := bucketAliasSuffix(tt.input); got != tt.want {
			t.Fatalf("bucketAliasSuffix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func mustFilter(t *testing.T, raw string) *FilterExpr {
	t.Helper()
	var expr FilterExpr
	if err := json.Unmarshal([]byte(raw), &expr); err != nil {
		t.Fatalf("unmarshal filter: %v", err)
	}
	return &expr
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
