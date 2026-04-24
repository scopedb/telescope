package httpapi

import (
	"strings"

	"github.com/scopedb/telescope/services/api/internal/semantic"
)

func toSemanticOrders(items []SortRequest) []semantic.OrderSpec {
	out := make([]semantic.OrderSpec, 0, len(items))
	for _, item := range items {
		out = append(out, item.toSemantic())
	}
	return out
}

func toSemanticMeasures(items []MeasureRequest) []semantic.AggregateSpec {
	out := make([]semantic.AggregateSpec, 0, len(items))
	for _, item := range items {
		spec := item.toSemantic()
		spec.Op = normalizeMeasureOp(spec.Op)
		out = append(out, spec)
	}
	return out
}

func toSemanticGroups(items []GroupByRequest) []semantic.GroupBySpec {
	out := make([]semantic.GroupBySpec, 0, len(items))
	for _, item := range items {
		out = append(out, item.toSemantic())
	}
	return out
}

func normalizeMeasureOp(op string) string {
	return strings.ToLower(strings.TrimSpace(op))
}
