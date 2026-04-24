package httpapi

import (
	"time"

	"github.com/scopedb/telescope/services/api/internal/semantic"
)

type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version,omitempty"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type TimeRangeRequest struct {
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
}

func (r *TimeRangeRequest) toSemantic() *semantic.TimeRange {
	if r == nil {
		return nil
	}
	return &semantic.TimeRange{
		Start: r.Start,
		End:   r.End,
	}
}

type SearchRequest struct {
	Source    string               `json:"source"`
	TimeRange *TimeRangeRequest    `json:"time_range,omitempty"`
	Filter    *semantic.FilterExpr `json:"filter,omitempty"`
	Project   []string             `json:"project,omitempty"`
	Sort      []SortRequest        `json:"sort,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
	Cursor    string               `json:"cursor,omitempty"`
	Debug     DebugRequest         `json:"debug,omitempty"`
}

type AggregateRequest struct {
	Source    string               `json:"source"`
	TimeRange *TimeRangeRequest    `json:"time_range,omitempty"`
	Filter    *semantic.FilterExpr `json:"filter,omitempty"`
	GroupBy   []GroupByRequest     `json:"group_by,omitempty"`
	Measures  []MeasureRequest     `json:"measures,omitempty"`
	Sort      []SortRequest        `json:"sort,omitempty"`
	Limit     int                  `json:"limit,omitempty"`
	Debug     DebugRequest         `json:"debug,omitempty"`
}

type DebugRequest struct {
	ScopeQL bool `json:"scopeql,omitempty"`
}

type SortRequest struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

func (r SortRequest) toSemantic() semantic.OrderSpec {
	return semantic.OrderSpec{
		Field:     r.Field,
		Direction: r.Direction,
	}
}

type MeasureRequest struct {
	Op    string `json:"op"`
	Field string `json:"field,omitempty"`
	Alias string `json:"as,omitempty"`
}

func (r MeasureRequest) toSemantic() semantic.AggregateSpec {
	return semantic.AggregateSpec{
		Op:    r.Op,
		Field: r.Field,
		Alias: r.Alias,
	}
}

type GroupByRequest struct {
	Field      string             `json:"field,omitempty"`
	TimeBucket *TimeBucketRequest `json:"time_bucket,omitempty"`
}

type TimeBucketRequest struct {
	Field    string `json:"field,omitempty"`
	Interval string `json:"interval"`
}

func (r GroupByRequest) toSemantic() semantic.GroupBySpec {
	spec := semantic.GroupBySpec{
		Field: r.Field,
	}
	if r.TimeBucket != nil {
		spec.TimeBucket = &semantic.TimeBucketSpec{
			Field:    r.TimeBucket.Field,
			Interval: r.TimeBucket.Interval,
		}
	}
	return spec
}

type SchemaResponse struct {
	Relations []RelationSchema `json:"relations"`
}

type RelationSchema struct {
	Name              string           `json:"name"`
	Kind              string           `json:"kind"`
	TimeField         string           `json:"time_field"`
	DefaultSort       []SortResponse   `json:"default_sort"`
	DefaultLimit      int              `json:"default_limit"`
	MaxLimit          int              `json:"max_limit"`
	SupportsSearch    bool             `json:"supports_search"`
	SupportsAggregate bool             `json:"supports_aggregate"`
	Advisory          RelationAdvisory `json:"advisory"`
	Fields            []FieldSchema    `json:"fields"`
	Measures          []MeasureSchema  `json:"measures"`
}

type RelationAdvisory struct {
	IdentityFields    []string `json:"identity_fields"`
	AnchorFields      []string `json:"anchor_fields"`
	DefaultProject    []string `json:"default_project"`
	PreferredFilters  []string `json:"preferred_filters"`
	PreferredGroupBy  []string `json:"preferred_group_by"`
	PreferredMeasures []string `json:"preferred_measures"`
	CommonPatterns    []string `json:"common_patterns"`
	Notes             []string `json:"notes"`
}

type FieldSchema struct {
	Name        string             `json:"name"`
	Type        semantic.FieldType `json:"type"`
	Role        semantic.FieldRole `json:"role"`
	Filterable  bool               `json:"filterable"`
	Searchable  bool               `json:"searchable"`
	Patternable bool               `json:"patternable"`
	Groupable   bool               `json:"groupable"`
}

type MeasureSchema struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SearchResponse struct {
	Rows []map[string]any `json:"rows"`
	Page SearchPage       `json:"page"`
	Meta SearchMeta       `json:"meta"`
}

type SearchPage struct {
	Limit      int    `json:"limit"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type SearchMeta struct {
	AppliedQuery AppliedQuery `json:"applied_query"`
	Warnings     []string     `json:"warnings,omitempty"`
	Debug        *DebugMeta   `json:"debug,omitempty"`
}

type AggregateResponse struct {
	Rows []map[string]any `json:"rows"`
	Meta AggregateMeta    `json:"meta"`
}

type AggregateMeta struct {
	AppliedQuery AppliedQuery `json:"applied_query"`
	Warnings     []string     `json:"warnings,omitempty"`
	Debug        *DebugMeta   `json:"debug,omitempty"`
}

type AppliedQuery struct {
	Source    string               `json:"source"`
	TimeRange *TimeRangeRequest    `json:"time_range,omitempty"`
	Filter    *semantic.FilterExpr `json:"filter,omitempty"`
	Project   []string             `json:"project,omitempty"`
	Sort      []SortResponse       `json:"sort,omitempty"`
	GroupBy   []GroupByRequest     `json:"group_by,omitempty"`
	Measures  []MeasureRequest     `json:"measures,omitempty"`
	Limit     int                  `json:"limit"`
	HasCursor bool                 `json:"has_cursor,omitempty"`
}

type DebugMeta struct {
	GeneratedScopeQL string `json:"generated_scopeql,omitempty"`
}

type SortResponse struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}
