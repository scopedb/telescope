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

import (
	"fmt"
	"strings"

	"github.com/scopedb/telescope/services/api/internal/scopeql"
)

func (r Registry) compileFilterExpr(relation RelationSpec, filter *FilterExpr) (scopeql.Expr, error) {
	if filter == nil {
		return nil, fmt.Errorf("nil filter expression")
	}

	switch filter.Kind() {
	case FilterKindAnd:
		return r.compileLogicalFilter(relation, scopeql.And, filter.Children())
	case FilterKindOr:
		return r.compileLogicalFilter(relation, scopeql.Or, filter.Children())
	case FilterKindNot:
		children := filter.Children()
		if len(children) != 1 {
			return nil, fmt.Errorf("not filter must contain exactly one child")
		}
		child, err := r.compileFilterExpr(relation, children[0])
		if err != nil {
			return nil, err
		}
		return scopeql.Not(child), nil
	case FilterKindEq, FilterKindGt, FilterKindGte, FilterKindLt, FilterKindLte:
		left, err := r.compileFieldExpr(relation.Name, filter.Field())
		if err != nil {
			return nil, err
		}
		right, err := scopeql.Literal(filter.Value())
		if err != nil {
			return nil, fmt.Errorf("filter %q: %w", filter.Field(), err)
		}
		switch filter.Kind() {
		case FilterKindEq:
			return scopeql.Eq(left, right), nil
		case FilterKindGt:
			return scopeql.Gt(left, right), nil
		case FilterKindGte:
			return scopeql.Gte(left, right), nil
		case FilterKindLt:
			return scopeql.Lt(left, right), nil
		case FilterKindLte:
			return scopeql.Lte(left, right), nil
		}
	case FilterKindIn:
		left, err := r.compileFieldExpr(relation.Name, filter.Field())
		if err != nil {
			return nil, err
		}
		items := make([]scopeql.Expr, 0, len(filter.Values()))
		for _, value := range filter.Values() {
			literal, literalErr := scopeql.Literal(value)
			if literalErr != nil {
				return nil, fmt.Errorf("filter %q: %w", filter.Field(), literalErr)
			}
			items = append(items, literal)
		}
		return scopeql.In(left, items...), nil
	case FilterKindExists:
		left, err := r.compileFieldExpr(relation.Name, filter.Field())
		if err != nil {
			return nil, err
		}
		return scopeql.IsNotNull(left), nil
	case FilterKindSearch:
		return r.compileStringPredicate(relation, "search", filter.Field(), filter.Value(), "")
	case FilterKindContains:
		return r.compileStringPredicate(relation, "contains", filter.Field(), filter.Value(), "")
	case FilterKindRegexpLike:
		pattern := filter.Pattern()
		if flags := strings.TrimSpace(filter.Flags()); flags != "" {
			pattern = "(?" + flags + ")" + pattern
		}
		return r.compileStringPredicate(relation, "regexp_like", filter.Field(), pattern, "")
	}

	return nil, fmt.Errorf("unsupported filter kind %q", filter.Kind())
}

func (r Registry) compileLogicalFilter(relation RelationSpec, factory func(...scopeql.Expr) scopeql.Expr, children []*FilterExpr) (scopeql.Expr, error) {
	if len(children) == 0 {
		return nil, fmt.Errorf("logical filter requires at least one child")
	}
	exprs := make([]scopeql.Expr, 0, len(children))
	for _, child := range children {
		expr, err := r.compileFilterExpr(relation, child)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}
	return factory(exprs...), nil
}

func (r Registry) compileStringPredicate(relation RelationSpec, fn string, field string, value any, extra string) (scopeql.Expr, error) {
	left, err := r.compileFieldExpr(relation.Name, field)
	if err != nil {
		return nil, err
	}
	right, err := scopeql.Literal(value)
	if err != nil {
		return nil, fmt.Errorf("filter %q: %w", field, err)
	}
	args := []scopeql.Expr{left, right}
	if extra != "" {
		flagExpr, literalErr := scopeql.Literal(extra)
		if literalErr != nil {
			return nil, literalErr
		}
		args = append(args, flagExpr)
	}
	return scopeql.Call(fn, args...), nil
}
