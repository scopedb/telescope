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

import (
	"fmt"
	"strings"
)

type Selection struct {
	Expr  Expr
	Alias string
}

func Select(expr Expr, alias string) Selection {
	return Selection{Expr: expr, Alias: alias}
}

func (s Selection) ScopeQL() string {
	if strings.TrimSpace(s.Alias) == "" {
		return s.Expr.ScopeQL()
	}
	return fmt.Sprintf("%s AS %s", s.Expr.ScopeQL(), QuoteIdentifier(s.Alias))
}

type Order struct {
	Expr Expr
	Desc bool
	Raw  string
}

func OrderBy(expr Expr, desc bool) Order {
	return Order{Expr: expr, Desc: desc}
}

func RawOrder(value string) Order {
	return Order{Raw: value}
}

func (o Order) ScopeQL() string {
	if strings.TrimSpace(o.Raw) != "" {
		return o.Raw
	}
	if o.Desc {
		return o.Expr.ScopeQL() + " DESC"
	}
	return o.Expr.ScopeQL() + " ASC"
}

type Query struct {
	from       string
	where      Expr
	selects    []Selection
	groupBy    []Expr
	aggregates []Selection
	orderBy    []Order
	limit      *int
}

func New() *Query {
	return &Query{}
}

func (q *Query) From(table string) *Query {
	q.from = table
	return q
}

func (q *Query) Where(expr Expr) *Query {
	q.where = expr
	return q
}

func (q *Query) Select(selects ...Selection) *Query {
	q.selects = append(q.selects, selects...)
	return q
}

func (q *Query) GroupBy(exprs ...Expr) *Query {
	q.groupBy = append(q.groupBy, exprs...)
	return q
}

func (q *Query) Aggregate(selects ...Selection) *Query {
	q.aggregates = append(q.aggregates, selects...)
	return q
}

func (q *Query) OrderBy(orders ...Order) *Query {
	q.orderBy = append(q.orderBy, orders...)
	return q
}

func (q *Query) Limit(n int) *Query {
	q.limit = &n
	return q
}

func (q *Query) ScopeQL() string {
	lines := make([]string, 0, 6)

	if strings.TrimSpace(q.from) != "" {
		lines = append(lines, "FROM "+QuoteIdentifierPath(q.from))
	}
	if q.where != nil {
		lines = append(lines, "WHERE "+q.where.ScopeQL())
	}
	if len(q.aggregates) > 0 {
		if len(q.groupBy) > 0 {
			lines = append(lines, "GROUP BY "+joinExprs(q.groupBy))
		}
		lines = append(lines, "AGGREGATE\n  "+joinSelections(q.aggregates))
		if len(q.selects) > 0 {
			lines = append(lines, "SELECT\n  "+joinSelections(q.selects))
		}
	} else {
		if len(q.selects) > 0 {
			lines = append(lines, "SELECT\n  "+joinSelections(q.selects))
		}
		if len(q.groupBy) > 0 {
			lines = append(lines, "GROUP BY "+joinExprs(q.groupBy))
		}
	}
	if len(q.orderBy) > 0 {
		lines = append(lines, "ORDER BY "+joinOrders(q.orderBy))
	}
	if q.limit != nil {
		lines = append(lines, fmt.Sprintf("LIMIT %d", *q.limit))
	}

	return strings.Join(lines, "\n")
}

func joinSelections(items []Selection) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, item.ScopeQL())
	}
	return strings.Join(lines, ",\n  ")
}

func joinExprs(items []Expr) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.ScopeQL())
	}
	return strings.Join(parts, ", ")
}

func joinOrders(items []Order) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.ScopeQL())
	}
	return strings.Join(parts, ", ")
}
