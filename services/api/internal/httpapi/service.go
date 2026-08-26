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

package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/scopedb/telescope/services/api/internal/semantic"
)

type QueryRunner interface {
	Query(ctx context.Context, statement string) ([]map[string]any, error)
	Close() error
}

type Service struct {
	registry         semantic.Registry
	runner           QueryRunner
	version          string
	now              func() time.Time
	ingestionRuntime exporterStatusReader
	ingestionMetrics collectorMetricsReader
}

type TelemetryService interface {
	Health(ctx context.Context) HealthResponse
	Schema(ctx context.Context) (SchemaResponse, error)
	SchemaGuide(ctx context.Context) (string, error)
	Search(ctx context.Context, request SearchRequest) (SearchResponse, error)
	Aggregate(ctx context.Context, request AggregateRequest) (AggregateResponse, error)
}

type ServiceError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewService(registry semantic.Registry, runner QueryRunner, version string) (*Service, error) {
	if err := registry.Validate(); err != nil {
		return nil, fmt.Errorf("validate semantic registry: %w", err)
	}
	if runner == nil {
		return nil, fmt.Errorf("query runner is required")
	}
	return &Service{
		registry:         registry,
		runner:           runner,
		version:          strings.TrimSpace(version),
		now:              func() time.Time { return time.Now().UTC() },
		ingestionRuntime: scopedbexporter.DefaultStatusRegistry,
		ingestionMetrics: newPrometheusCollectorMetricsReader(),
	}, nil
}

func (s *Service) Health(_ context.Context) HealthResponse {
	response := HealthResponse{
		Status:  "ok",
		Service: serviceName,
	}
	if s.version != "" {
		response.Version = s.version
	}
	return response
}

func (s *Service) Readiness(ctx context.Context) (HealthResponse, bool) {
	status := s.IngestionStatus(ctx)
	ready := status.InternalTelemetry.Available && len(status.Signals) > 0
	for _, signal := range status.Signals {
		ready = ready && signal.Ready
	}
	response := HealthResponse{
		Status:  "not_ready",
		Service: serviceName,
	}
	if ready {
		response.Status = "ready"
	}
	if s.version != "" {
		response.Version = s.version
	}
	return response, ready
}

func (s *Service) Schema(_ context.Context) (SchemaResponse, error) {
	return schemaResponse(s.registry), nil
}

func (s *Service) SchemaGuide(_ context.Context) (string, error) {
	return renderSchemaGuide(s.registry), nil
}

func (s *Service) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	if strings.TrimSpace(request.Source) == "" {
		return SearchResponse{}, badRequest("source is required", nil)
	}
	if request.TimeRange == nil {
		return SearchResponse{}, badRequest("time_range is required", nil)
	}

	relation, ok := s.registry.Relation(request.Source)
	if !ok {
		return SearchResponse{}, badRequest("unknown source", map[string]any{"source": request.Source})
	}
	if !supportsTimeTopCursor(request.Sort) && strings.TrimSpace(request.Cursor) != "" {
		return SearchResponse{}, badRequest("cursor is only supported for default time-top sort", nil)
	}
	if err := s.validateSearchRequest(relation, request); err != nil {
		return SearchResponse{}, err
	}

	timeRange := request.TimeRange.toSemantic()
	if timeRange.End == nil {
		now := s.now()
		timeRange.End = &now
	}

	var cursor *searchCursor
	if strings.TrimSpace(request.Cursor) != "" {
		decoded, err := decodeSearchCursor(request.Cursor)
		if err != nil {
			return SearchResponse{}, badRequest("invalid cursor", map[string]any{"reason": err.Error()})
		}
		if decoded.Source != request.Source {
			return SearchResponse{}, badRequest("cursor source mismatch", nil)
		}
		timeRange.End = &decoded.FrozenEnd
		cursor = &decoded
	}

	filter := request.Filter
	if cursor != nil {
		filter = semantic.AndExpr(
			filter,
			semantic.OrExpr(
				semantic.LtExpr("ts", cursor.LastTS),
				semantic.AndExpr(
					semantic.EqExpr("ts", cursor.LastTS),
					semantic.LtExpr("row_id", cursor.LastRowID),
				),
			),
		)
	}

	spec := semantic.QuerySpec{
		Relation:  request.Source,
		TimeRange: timeRange,
		Fields:    searchInternalProject(relation, request),
		Filter:    filter,
		OrderBy:   toSemanticOrders(request.Sort),
		Limit:     clampLimit(request.Limit, relation.DefaultLimit, relation.MaxLimit),
	}
	responseLimit := spec.Limit
	if supportsTimeTopCursor(request.Sort) {
		spec.Limit++
	}

	rows, statement, err := s.buildAndRun(ctx, spec)
	if err != nil {
		return SearchResponse{}, internalError(err)
	}

	nextCursor := ""
	hasMore := supportsTimeTopCursor(request.Sort) && len(rows) > responseLimit
	if hasMore {
		rows = rows[:responseLimit]
		cursorValue, err := buildNextCursor(request.Source, timeRange, rows)
		if err != nil {
			return SearchResponse{}, internalError(err)
		}
		if cursorValue != "" {
			nextCursor = cursorValue
		}
		hasMore = nextCursor != ""
	}
	rows = trimRowsToProject(rows, request.Project)

	return SearchResponse{
		Rows: rows,
		Page: SearchPage{
			Limit:      responseLimit,
			HasMore:    hasMore,
			NextCursor: nextCursor,
		},
		Meta: SearchMeta{
			AppliedQuery: searchAppliedQuery(request, timeRange, spec, relation, responseLimit),
			Debug:        debugMeta(request.Debug, statement),
		},
	}, nil
}

func (s *Service) Aggregate(ctx context.Context, request AggregateRequest) (AggregateResponse, error) {
	if strings.TrimSpace(request.Source) == "" {
		return AggregateResponse{}, badRequest("source is required", nil)
	}
	if request.TimeRange == nil {
		return AggregateResponse{}, badRequest("time_range is required", nil)
	}

	relation, ok := s.registry.Relation(request.Source)
	if !ok {
		return AggregateResponse{}, badRequest("unknown source", map[string]any{"source": request.Source})
	}
	if err := s.validateAggregateRequest(relation, request); err != nil {
		return AggregateResponse{}, err
	}

	spec := semantic.QuerySpec{
		Relation:   request.Source,
		TimeRange:  request.TimeRange.toSemantic(),
		Filter:     request.Filter,
		GroupBy:    toSemanticGroups(request.GroupBy),
		Aggregates: toSemanticMeasures(request.Measures),
		OrderBy:    toSemanticOrders(request.Sort),
		Limit:      clampLimit(request.Limit, relation.DefaultLimit, relation.MaxLimit),
	}
	if len(spec.Aggregates) == 0 {
		spec.Aggregates = []semantic.AggregateSpec{{Op: "count", Alias: "count"}}
	}

	rows, statement, err := s.buildAndRun(ctx, spec)
	if err != nil {
		return AggregateResponse{}, internalError(err)
	}

	return AggregateResponse{
		Rows: rows,
		Meta: AggregateMeta{
			AppliedQuery: aggregateAppliedQuery(request, spec),
			Debug:        debugMeta(request.Debug, statement),
		},
	}, nil
}

func (s *Service) buildAndRun(ctx context.Context, spec semantic.QuerySpec) ([]map[string]any, string, error) {
	query, err := s.registry.BuildQuery(spec)
	if err != nil {
		return nil, "", fmt.Errorf("build semantic query: %w", err)
	}

	statement := query.ScopeQL()
	rows, err := s.runner.Query(ctx, statement)
	if err != nil {
		return nil, "", fmt.Errorf("execute scopeql query: %w", err)
	}

	return rows, statement, nil
}

func badRequest(message string, details map[string]any) *ServiceError {
	return &ServiceError{
		Status:  http.StatusBadRequest,
		Code:    "bad_request",
		Message: message,
		Details: details,
	}
}

func internalError(err error) *ServiceError {
	return &ServiceError{
		Status:  http.StatusInternalServerError,
		Code:    "internal_error",
		Message: "failed to execute query",
		Details: map[string]any{"reason": err.Error()},
	}
}

func clampLimit(limit int, fallback int, max int) int {
	if limit <= 0 {
		limit = fallback
	}
	if max > 0 && limit > max {
		limit = max
	}
	return limit
}

func normalizeDirection(direction string) string {
	if strings.EqualFold(strings.TrimSpace(direction), "asc") {
		return "asc"
	}
	return "desc"
}

func debugMeta(request DebugRequest, statement string) *DebugMeta {
	if !request.ScopeQL {
		return nil
	}
	return &DebugMeta{GeneratedScopeQL: statement}
}

func searchAppliedQuery(request SearchRequest, timeRange *semantic.TimeRange, spec semantic.QuerySpec, relation semantic.RelationSpec, responseLimit int) AppliedQuery {
	return AppliedQuery{
		Source:    request.Source,
		TimeRange: semanticTimeRangeToRequest(timeRange),
		Filter:    request.Filter,
		Project:   request.Project,
		Sort:      searchAppliedSort(spec, relation),
		Limit:     responseLimit,
		HasCursor: strings.TrimSpace(request.Cursor) != "",
	}
}

func aggregateAppliedQuery(request AggregateRequest, spec semantic.QuerySpec) AppliedQuery {
	return AppliedQuery{
		Source:    request.Source,
		TimeRange: semanticTimeRangeToRequest(spec.TimeRange),
		Filter:    spec.Filter,
		GroupBy:   request.GroupBy,
		Measures:  measureRequests(spec.Aggregates),
		Sort:      sortResponses(spec.OrderBy),
		Limit:     spec.Limit,
	}
}

func semanticTimeRangeToRequest(timeRange *semantic.TimeRange) *TimeRangeRequest {
	if timeRange == nil {
		return nil
	}
	return &TimeRangeRequest{
		Start: timeRange.Start,
		End:   timeRange.End,
	}
}

func sortResponses(orders []semantic.OrderSpec) []SortResponse {
	responses := make([]SortResponse, 0, len(orders))
	for _, order := range orders {
		responses = append(responses, SortResponse{
			Field:     order.Field,
			Direction: normalizeDirection(order.Direction),
		})
	}
	return responses
}

func measureRequests(aggregates []semantic.AggregateSpec) []MeasureRequest {
	requests := make([]MeasureRequest, 0, len(aggregates))
	for _, aggregate := range aggregates {
		requests = append(requests, MeasureRequest{
			Op:    aggregate.Op,
			Field: aggregate.Field,
			Alias: aggregate.Alias,
		})
	}
	return requests
}

func supportsTimeTopCursor(sort []SortRequest) bool {
	if len(sort) == 0 {
		return true
	}
	if len(sort) > 2 {
		return false
	}
	if sort[0].Field != "ts" || normalizeDirection(sort[0].Direction) != "desc" {
		return false
	}
	if len(sort) == 2 && (sort[1].Field != "row_id" || normalizeDirection(sort[1].Direction) != "desc") {
		return false
	}
	return true
}

func buildNextCursor(source string, timeRange *semantic.TimeRange, rows []map[string]any) (string, error) {
	if len(rows) == 0 {
		return "", nil
	}
	last := rows[len(rows)-1]
	lastTS, ok, err := timestampFromRowValue(last["ts"])
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	rawRowID, ok := last["row_id"].(string)
	if !ok || strings.TrimSpace(rawRowID) == "" {
		return "", nil
	}

	var start *time.Time
	var end time.Time
	if timeRange != nil {
		start = timeRange.Start
		if timeRange.End != nil {
			end = timeRange.End.UTC()
		}
	}
	if end.IsZero() {
		return "", nil
	}

	return encodeSearchCursor(searchCursor{
		Source:    source,
		Start:     start,
		FrozenEnd: end,
		LastTS:    lastTS.UTC(),
		LastRowID: rawRowID,
	})
}

func timestampFromRowValue(value any) (time.Time, bool, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), true, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return time.Time{}, false, nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		if err != nil {
			return time.Time{}, false, err
		}
		return parsed.UTC(), true, nil
	default:
		return time.Time{}, false, nil
	}
}
