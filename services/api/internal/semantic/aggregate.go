package semantic

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/scopeql"
)

func (r Registry) compileAggregate(relation RelationSpec, aggregate AggregateSpec) (scopeql.Selection, error) {
	funcName := strings.TrimSpace(aggregate.Op)
	if funcName == "" {
		return scopeql.Selection{}, fmt.Errorf("aggregate op is required")
	}

	var expr scopeql.Expr
	if strings.TrimSpace(aggregate.Field) == "" {
		expr = scopeql.Call(funcName)
	} else {
		fieldExpr, fieldErr := r.compileFieldExpr(relation.Name, aggregate.Field)
		if fieldErr != nil {
			return scopeql.Selection{}, fieldErr
		}
		expr = scopeql.Call(funcName, fieldExpr)
	}

	alias := strings.TrimSpace(aggregate.Alias)
	if alias == "" {
		alias = defaultAggregateAlias(aggregate)
	}
	return scopeql.Select(expr, alias), nil
}

func defaultAggregateAlias(aggregate AggregateSpec) string {
	if strings.TrimSpace(aggregate.Field) == "" {
		return aggregate.Op
	}
	return aggregate.Op + "_" + aggregate.Field
}

func (r Registry) compileGroupBy(relation RelationSpec, groups []GroupBySpec) ([]scopeql.Selection, error) {
	selections := make([]scopeql.Selection, 0, len(groups))
	for _, group := range groups {
		switch {
		case strings.TrimSpace(group.Field) != "":
			fieldName := strings.TrimSpace(group.Field)
			fieldExpr, err := r.compileFieldExpr(relation.Name, fieldName)
			if err != nil {
				return nil, err
			}
			selections = append(selections, scopeql.Select(fieldExpr, fieldName))
		case group.TimeBucket != nil:
			bucket, err := compileTimeBucket(relation, r, *group.TimeBucket)
			if err != nil {
				return nil, err
			}
			selections = append(selections, bucket.Selection)
		default:
			return nil, fmt.Errorf("group_by entry must contain field or time_bucket")
		}
	}
	return selections, nil
}

type bucketSelection struct {
	Alias     string
	Selection scopeql.Selection
}

func compileTimeBucket(relation RelationSpec, registry Registry, spec TimeBucketSpec) (bucketSelection, error) {
	field := strings.TrimSpace(spec.Field)
	if field == "" {
		field = relation.TimeField
	}
	fieldExpr, err := registry.compileFieldExpr(relation.Name, field)
	if err != nil {
		return bucketSelection{}, err
	}

	unit, n, err := bucketIntervalToArgs(spec.Interval)
	if err != nil {
		return bucketSelection{}, err
	}
	alias := field + "_" + bucketAliasSuffix(spec.Interval)
	return bucketSelection{
		Alias:     alias,
		Selection: scopeql.Select(scopeql.Call("floor", fieldExpr, scopeql.Raw(fmt.Sprintf("n => %d", n)), scopeql.Raw("unit => '"+unit+"'")), alias),
	}, nil
}

func bucketIntervalToArgs(interval string) (string, int, error) {
	duration, err := parseBucketDuration(interval)
	if err != nil {
		return "", 0, err
	}
	if duration <= 0 {
		return "", 0, fmt.Errorf("time bucket interval must be positive")
	}

	candidates := []struct {
		unit string
		size time.Duration
	}{
		{unit: "hour", size: time.Hour},
		{unit: "minute", size: time.Minute},
		{unit: "second", size: time.Second},
		{unit: "millisecond", size: time.Millisecond},
		{unit: "microsecond", size: time.Microsecond},
		{unit: "nanosecond", size: time.Nanosecond},
	}

	for _, candidate := range candidates {
		if duration%candidate.size != 0 {
			continue
		}
		n := duration / candidate.size
		if n <= 0 {
			continue
		}
		return candidate.unit, int(n), nil
	}

	return "", 0, fmt.Errorf("unsupported time bucket interval %q", interval)
}

func parseBucketDuration(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("time bucket interval is required")
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return duration, nil
	}
	if strings.HasSuffix(value, "d") {
		dayValue := strings.TrimSuffix(value, "d")
		parsed, err := strconv.ParseFloat(dayValue, 64)
		if err != nil {
			return 0, fmt.Errorf("unsupported time bucket interval %q", raw)
		}
		return time.Duration(parsed * float64(24*time.Hour)), nil
	}
	return 0, fmt.Errorf("unsupported time bucket interval %q", raw)
}

func bucketAliasSuffix(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "bucket"
	}

	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			builder.WriteRune(r)
			lastUnderscore = false
		default:
			if lastUnderscore {
				continue
			}
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}

	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "bucket"
	}
	return result
}
