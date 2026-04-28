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
		"FROM `scopedb`.`otel`.`traces`\n" +
		"WHERE (`trace_id` = 'abc') AND (`status_code` = 'error')\n" +
		"SELECT\n" +
		"  `start_timestamp` AS `ts`,\n" +
		"  `trace_id` AS `trace_id`\n" +
		"ORDER BY `ts` DESC\n" +
		"LIMIT 20"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestAggregateQueryScopeQL(t *testing.T) {
	query := New().
		From("scopedb.otel.logs").
		Where(Eq(Ref("service"), String("checkout"))).
		GroupBy(
			Select(Call("trunc", Ref("record_timestamp"), Raw("unit => 'minute'")), "bucket"),
			Select(Ref("status"), "status"),
		).
		Aggregate(
			Select(Call("count"), "count"),
		).
		Select(
			Select(Ref("bucket"), ""),
			Select(Ref("status"), ""),
			Select(Ref("count"), ""),
		).
		OrderBy(OrderBy(Ref("bucket"), false))

	want := "" +
		"FROM `scopedb`.`otel`.`logs`\n" +
		"WHERE `service` = 'checkout'\n" +
		"GROUP BY trunc(`record_timestamp`, unit => 'minute') AS `bucket`, `status` AS `status`\n" +
		"AGGREGATE\n" +
		"  count() AS `count`\n" +
		"SELECT\n" +
		"  `bucket`,\n" +
		"  `status`,\n" +
		"  `count`\n" +
		"ORDER BY `bucket` ASC"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}

func TestStringLiteralEscapesQuotesAndBackslashes(t *testing.T) {
	expr := String(`path\tail' OR true`)
	want := `'path\\tail'' OR true'`

	if got := expr.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL: got %q, want %q", got, want)
	}
}

func TestIdentifiersEscapeInjectionCharacters(t *testing.T) {
	query := New().
		From("safe.schema.logs; DROP TABLE x").
		Select(
			Select(Ref("field`name"), "alias.with.dot`; DROP TABLE x"),
		).
		OrderBy(OrderBy(Ref("alias.with.dot`; DROP TABLE x"), false))

	want := "" +
		"FROM `safe`.`schema`.`logs; DROP TABLE x`\n" +
		"SELECT\n" +
		"  `field``name` AS `alias.with.dot``; DROP TABLE x`\n" +
		"ORDER BY `alias.with.dot``; DROP TABLE x` ASC"

	if got := query.ScopeQL(); got != want {
		t.Fatalf("unexpected ScopeQL:\n%s", got)
	}
}
