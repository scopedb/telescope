package semantic

import (
	"fmt"
	"strings"
	"time"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/scopeql"
)

type AggregateSpec struct {
	Op    string
	Field string
	Alias string
}

type OrderSpec struct {
	Field     string
	Direction string
}

func (o OrderSpec) Desc() bool {
	return strings.EqualFold(strings.TrimSpace(o.Direction), "desc")
}

type GroupBySpec struct {
	Field      string
	TimeBucket *TimeBucketSpec
}

type TimeBucketSpec struct {
	Field    string
	Interval string
}

type QuerySpec struct {
	Relation   string
	TimeRange  *TimeRange
	Fields     []string
	Filter     *FilterExpr
	GroupBy    []GroupBySpec
	Aggregates []AggregateSpec
	OrderBy    []OrderSpec
	Limit      int
}

type TimeRange struct {
	Start *time.Time
	End   *time.Time
}

func (r Registry) BuildQuery(spec QuerySpec) (*scopeql.Query, error) {
	relation, ok := r.Relation(spec.Relation)
	if !ok {
		return nil, fmt.Errorf("unknown relation %q", spec.Relation)
	}

	query := scopeql.New().From(relation.SourceTable)

	whereExprs := make([]scopeql.Expr, 0, 2)
	if strings.TrimSpace(relation.Where) != "" {
		whereExprs = append(whereExprs, scopeql.Raw(relation.Where))
	}
	if spec.Filter != nil {
		filterExpr, err := r.compileFilterExpr(relation, spec.Filter)
		if err != nil {
			return nil, err
		}
		whereExprs = append(whereExprs, filterExpr)
	}
	if spec.TimeRange != nil {
		rangeExprs, err := r.compileTimeRange(relation, *spec.TimeRange)
		if err != nil {
			return nil, err
		}
		whereExprs = append(whereExprs, rangeExprs...)
	}
	if len(whereExprs) == 1 {
		query.Where(whereExprs[0])
	} else if len(whereExprs) > 1 {
		query.Where(scopeql.And(whereExprs...))
	}

	groupSelections, err := r.compileGroupBy(relation, spec.GroupBy)
	if err != nil {
		return nil, err
	}

	selectedFields := selectFieldsForQuery(relation, spec)
	if len(spec.Aggregates) > 0 {
		for _, selection := range groupSelections {
			query.GroupBy(selection)
		}
		for _, aggregate := range spec.Aggregates {
			aggregateSelection, aggregateErr := r.compileAggregate(relation, aggregate)
			if aggregateErr != nil {
				return nil, aggregateErr
			}
			query.Aggregate(aggregateSelection)
		}
		for _, selection := range groupSelections {
			query.Select(scopeql.Select(scopeql.Ref(selection.Alias), ""))
		}
		for _, aggregate := range spec.Aggregates {
			alias := strings.TrimSpace(aggregate.Alias)
			if alias == "" {
				alias = defaultAggregateAlias(aggregate)
			}
			query.Select(scopeql.Select(scopeql.Ref(alias), ""))
		}
	} else {
		for _, fieldName := range selectedFields {
			fieldExpr, fieldErr := r.compileFieldExpr(relation.Name, fieldName)
			if fieldErr != nil {
				return nil, fieldErr
			}
			query.Select(scopeql.Select(fieldExpr, fieldName))
		}
		for _, selection := range groupSelections {
			query.Select(selection)
			query.GroupBy(scopeql.Ref(selection.Alias))
		}
	}

	for _, order := range spec.OrderBy {
		orderExpr, orderErr := r.compileOrderExpr(relation, selectedFields, groupSelections, spec.Aggregates, order.Field)
		if orderErr != nil {
			return nil, orderErr
		}
		query.OrderBy(scopeql.OrderBy(orderExpr, order.Desc()))
	}
	if len(spec.OrderBy) > 0 && len(spec.GroupBy) == 0 && len(spec.Aggregates) == 0 && hasField(relation.Fields, "row_id") && !containsOrderField(spec.OrderBy, "row_id") {
		query.OrderBy(scopeql.OrderBy(scopeql.Ref("row_id"), true))
	}

	if len(spec.OrderBy) == 0 && len(spec.GroupBy) == 0 && len(spec.Aggregates) == 0 {
		if strings.TrimSpace(relation.TimeField) != "" {
			query.OrderBy(scopeql.OrderBy(scopeql.Ref(relation.TimeField), true))
		}
		if hasField(relation.Fields, "row_id") {
			query.OrderBy(scopeql.OrderBy(scopeql.Ref("row_id"), true))
		}
	}

	if spec.Limit > 0 {
		query.Limit(spec.Limit)
	}

	return query, nil
}

func (r Registry) compileTimeRange(relation RelationSpec, timeRange TimeRange) ([]scopeql.Expr, error) {
	timeExpr, err := r.compileFieldExpr(relation.Name, relation.TimeField)
	if err != nil {
		return nil, fmt.Errorf("compile time field %q: %w", relation.TimeField, err)
	}

	var exprs []scopeql.Expr
	if timeRange.Start != nil {
		startExpr, literalErr := scopeql.Literal(timeRange.Start.UTC())
		if literalErr != nil {
			return nil, fmt.Errorf("compile time range start: %w", literalErr)
		}
		exprs = append(exprs, scopeql.Gte(timeExpr, startExpr))
	}
	if timeRange.End != nil {
		endExpr, literalErr := scopeql.Literal(timeRange.End.UTC())
		if literalErr != nil {
			return nil, fmt.Errorf("compile time range end: %w", literalErr)
		}
		exprs = append(exprs, scopeql.Lt(timeExpr, endExpr))
	}
	return exprs, nil
}

func selectFieldsForQuery(relation RelationSpec, spec QuerySpec) []string {
	selectedFields := make([]string, 0)
	seen := make(map[string]struct{})

	appendField := func(field string) {
		if field == "" {
			return
		}
		if _, ok := seen[field]; ok {
			return
		}
		seen[field] = struct{}{}
		selectedFields = append(selectedFields, field)
	}

	switch {
	case len(spec.Fields) > 0:
		for _, field := range spec.Fields {
			appendField(field)
		}
	case len(spec.GroupBy) == 0 && len(spec.Aggregates) == 0:
		for _, field := range relation.Fields {
			appendField(field)
		}
	}

	for _, group := range spec.GroupBy {
		appendField(group.Field)
	}

	return selectedFields
}

func (r Registry) compileFieldExpr(relationName string, fieldName string) (scopeql.Expr, error) {
	field, ok := r.Field(fieldName)
	if !ok {
		return nil, fmt.Errorf("unknown field %q", fieldName)
	}

	expr, ok := field.ExprForRelation(relationName)
	if !ok {
		return nil, fmt.Errorf("field %q has no expression for relation %q", fieldName, relationName)
	}
	return expr, nil
}

func (r Registry) compileOrderExpr(relation RelationSpec, selectedFields []string, groupSelections []scopeql.Selection, aggregates []AggregateSpec, fieldName string) (scopeql.Expr, error) {
	for _, selected := range selectedFields {
		if selected == fieldName {
			return scopeql.Ref(fieldName), nil
		}
	}
	for _, selection := range groupSelections {
		if selection.Alias == fieldName {
			return scopeql.Ref(fieldName), nil
		}
		if strings.HasPrefix(selection.Alias, fieldName+"_") {
			return scopeql.Ref(selection.Alias), nil
		}
	}
	for _, aggregate := range aggregates {
		alias := strings.TrimSpace(aggregate.Alias)
		if alias == "" {
			alias = defaultAggregateAlias(aggregate)
		}
		if alias == fieldName {
			return scopeql.Ref(fieldName), nil
		}
	}
	return r.compileFieldExpr(relation.Name, fieldName)
}

func hasField(fields []string, field string) bool {
	for _, candidate := range fields {
		if candidate == field {
			return true
		}
	}
	return false
}

func containsOrderField(orders []OrderSpec, field string) bool {
	for _, order := range orders {
		if order.Field == field {
			return true
		}
	}
	return false
}
