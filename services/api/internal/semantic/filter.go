package semantic

import (
	"encoding/json"
	"fmt"
)

type FilterKind string

const (
	FilterKindAnd        FilterKind = "and"
	FilterKindOr         FilterKind = "or"
	FilterKindNot        FilterKind = "not"
	FilterKindEq         FilterKind = "eq"
	FilterKindIn         FilterKind = "in"
	FilterKindGt         FilterKind = "gt"
	FilterKindGte        FilterKind = "gte"
	FilterKindLt         FilterKind = "lt"
	FilterKindLte        FilterKind = "lte"
	FilterKindExists     FilterKind = "exists"
	FilterKindSearch     FilterKind = "search"
	FilterKindContains   FilterKind = "contains"
	FilterKindRegexpLike FilterKind = "regexp_like"
)

type FilterExpr struct {
	kind     FilterKind
	children []*FilterExpr
	field    string
	value    any
	values   []any
	pattern  string
	flags    string
}

func (f *FilterExpr) Kind() FilterKind {
	if f == nil {
		return ""
	}
	return f.kind
}

func (f *FilterExpr) Field() string {
	if f == nil {
		return ""
	}
	return f.field
}

func (f *FilterExpr) Value() any {
	if f == nil {
		return nil
	}
	return f.value
}

func (f *FilterExpr) Values() []any {
	if f == nil {
		return nil
	}
	return f.values
}

func (f *FilterExpr) Pattern() string {
	if f == nil {
		return ""
	}
	return f.pattern
}

func (f *FilterExpr) Flags() string {
	if f == nil {
		return ""
	}
	return f.flags
}

func (f *FilterExpr) Children() []*FilterExpr {
	if f == nil {
		return nil
	}
	return f.children
}

func (f *FilterExpr) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 1 {
		return fmt.Errorf("filter expression must contain exactly one operator")
	}

	for key, value := range raw {
		switch FilterKind(key) {
		case FilterKindAnd, FilterKindOr:
			var children []*FilterExpr
			if err := json.Unmarshal(value, &children); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			f.kind = FilterKind(key)
			f.children = children
			return nil
		case FilterKindNot:
			var child FilterExpr
			if err := json.Unmarshal(value, &child); err != nil {
				return fmt.Errorf("not: %w", err)
			}
			f.kind = FilterKindNot
			f.children = []*FilterExpr{&child}
			return nil
		case FilterKindEq, FilterKindGt, FilterKindGte, FilterKindLt, FilterKindLte, FilterKindSearch, FilterKindContains:
			field, arg, err := decodeFieldValueTuple(value)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			f.kind = FilterKind(key)
			f.field = field
			f.value = arg
			return nil
		case FilterKindIn:
			field, values, err := decodeFieldValuesTuple(value)
			if err != nil {
				return fmt.Errorf("in: %w", err)
			}
			f.kind = FilterKindIn
			f.field = field
			f.values = values
			return nil
		case FilterKindExists:
			var field string
			if err := json.Unmarshal(value, &field); err != nil {
				return fmt.Errorf("exists: %w", err)
			}
			if field == "" {
				return fmt.Errorf("exists: field is required")
			}
			f.kind = FilterKindExists
			f.field = field
			return nil
		case FilterKindRegexpLike:
			var arg struct {
				Field   string `json:"field"`
				Pattern string `json:"pattern"`
				Flags   string `json:"flags,omitempty"`
			}
			if err := json.Unmarshal(value, &arg); err != nil {
				return fmt.Errorf("regexp_like: %w", err)
			}
			if arg.Field == "" || arg.Pattern == "" {
				return fmt.Errorf("regexp_like: field and pattern are required")
			}
			f.kind = FilterKindRegexpLike
			f.field = arg.Field
			f.pattern = arg.Pattern
			f.flags = arg.Flags
			return nil
		default:
			return fmt.Errorf("unsupported filter operator %q", key)
		}
	}

	return fmt.Errorf("empty filter expression")
}

func decodeFieldValueTuple(data json.RawMessage) (string, any, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(data, &parts); err != nil {
		return "", nil, err
	}
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("expected [field, value]")
	}

	var field string
	if err := json.Unmarshal(parts[0], &field); err != nil {
		return "", nil, fmt.Errorf("decode field: %w", err)
	}
	if field == "" {
		return "", nil, fmt.Errorf("field is required")
	}

	var value any
	if err := json.Unmarshal(parts[1], &value); err != nil {
		return "", nil, fmt.Errorf("decode value: %w", err)
	}
	return field, value, nil
}

func decodeFieldValuesTuple(data json.RawMessage) (string, []any, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal(data, &parts); err != nil {
		return "", nil, err
	}
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("expected [field, values]")
	}

	var field string
	if err := json.Unmarshal(parts[0], &field); err != nil {
		return "", nil, fmt.Errorf("decode field: %w", err)
	}
	if field == "" {
		return "", nil, fmt.Errorf("field is required")
	}

	var values []any
	if err := json.Unmarshal(parts[1], &values); err != nil {
		return "", nil, fmt.Errorf("decode values: %w", err)
	}
	return field, values, nil
}

func AndExpr(children ...*FilterExpr) *FilterExpr {
	items := make([]*FilterExpr, 0, len(children))
	for _, child := range children {
		if child != nil {
			items = append(items, child)
		}
	}
	switch len(items) {
	case 0:
		return nil
	case 1:
		return items[0]
	default:
		return &FilterExpr{kind: FilterKindAnd, children: items}
	}
}

func OrExpr(children ...*FilterExpr) *FilterExpr {
	items := make([]*FilterExpr, 0, len(children))
	for _, child := range children {
		if child != nil {
			items = append(items, child)
		}
	}
	switch len(items) {
	case 0:
		return nil
	case 1:
		return items[0]
	default:
		return &FilterExpr{kind: FilterKindOr, children: items}
	}
}

func EqExpr(field string, value any) *FilterExpr {
	return &FilterExpr{kind: FilterKindEq, field: field, value: value}
}

func LtExpr(field string, value any) *FilterExpr {
	return &FilterExpr{kind: FilterKindLt, field: field, value: value}
}
