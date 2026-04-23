package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/semantic"
)

const serviceName = "scopedb-otel-debug-api"

type QueryRunner interface {
	Query(ctx context.Context, statement string) ([]map[string]any, error)
	Close() error
}

type Server struct {
	registry semantic.Registry
	runner   QueryRunner
	version  string
}

func New(registry semantic.Registry, runner QueryRunner, version string) (*echo.Echo, error) {
	if err := registry.Validate(); err != nil {
		return nil, fmt.Errorf("validate semantic registry: %w", err)
	}
	if runner == nil {
		return nil, errors.New("query runner is required")
	}

	server := &Server{
		registry: registry,
		runner:   runner,
		version:  strings.TrimSpace(version),
	}

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())

	e.GET("/healthz", server.getHealth)

	v1 := e.Group("/v1")
	v1.GET("/schema", server.getSchema)
	v1.GET("/schema/guide.md", server.getSchemaGuide)
	v1.POST("/search", server.postSearch)
	v1.POST("/aggregate", server.postAggregate)

	return e, nil
}

func (s *Server) getHealth(c echo.Context) error {
	response := HealthResponse{
		Status:  "ok",
		Service: serviceName,
	}
	if s.version != "" {
		response.Version = s.version
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) getSchema(c echo.Context) error {
	return c.JSON(http.StatusOK, schemaResponse(s.registry))
}

func (s *Server) getSchemaGuide(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/markdown; charset=utf-8")
	return c.String(http.StatusOK, renderSchemaGuide(s.registry))
}

func (s *Server) postSearch(c echo.Context) error {
	var request SearchRequest
	if err := c.Bind(&request); err != nil {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "invalid request body", map[string]any{
			"reason": err.Error(),
		})
	}
	if strings.TrimSpace(request.Source) == "" {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "source is required", nil)
	}
	if request.TimeRange == nil {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "time_range is required", nil)
	}

	relation, ok := s.registry.Relation(request.Source)
	if !ok {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "unknown source", map[string]any{"source": request.Source})
	}
	if !supportsTimeTopCursor(request.Sort) && strings.TrimSpace(request.Cursor) != "" {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "cursor is only supported for default time-top sort", nil)
	}

	timeRange := request.TimeRange.toSemantic()
	if timeRange.End == nil {
		now := time.Now().UTC()
		timeRange.End = &now
	}
	var cursor *searchCursor
	if strings.TrimSpace(request.Cursor) != "" {
		decoded, err := decodeSearchCursor(request.Cursor)
		if err != nil {
			return s.writeError(c, http.StatusBadRequest, "bad_request", "invalid cursor", map[string]any{"reason": err.Error()})
		}
		if decoded.Source != request.Source {
			return s.writeError(c, http.StatusBadRequest, "bad_request", "cursor source mismatch", nil)
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
		Fields:    request.Project,
		Filter:    filter,
		OrderBy:   toSemanticOrders(request.Sort),
		Limit:     clampLimit(request.Limit, relation.DefaultLimit, relation.MaxLimit),
	}

	rows, scopeql, err := s.buildAndRun(c.Request().Context(), spec)
	if err != nil {
		return s.writeInternalError(c, err)
	}

	nextCursor := ""
	hasMore := false
	if supportsTimeTopCursor(request.Sort) && len(rows) == spec.Limit {
		cursorValue, err := buildNextCursor(request.Source, timeRange, rows)
		if err == nil && cursorValue != "" {
			nextCursor = cursorValue
			hasMore = true
		}
	}

	return c.JSON(http.StatusOK, SearchResponse{
		Rows: rows,
		Page: SearchPage{
			Limit:      spec.Limit,
			HasMore:    hasMore,
			NextCursor: nextCursor,
		},
		Meta: SearchMeta{
			AppliedSort:      searchAppliedSort(spec, relation),
			GeneratedScopeQL: scopeql,
		},
	})
}

func (s *Server) postAggregate(c echo.Context) error {
	var request AggregateRequest
	if err := c.Bind(&request); err != nil {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "invalid request body", map[string]any{
			"reason": err.Error(),
		})
	}
	if strings.TrimSpace(request.Source) == "" {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "source is required", nil)
	}
	if request.TimeRange == nil {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "time_range is required", nil)
	}

	relation, ok := s.registry.Relation(request.Source)
	if !ok {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "unknown source", map[string]any{"source": request.Source})
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

	rows, scopeql, err := s.buildAndRun(c.Request().Context(), spec)
	if err != nil {
		return s.writeInternalError(c, err)
	}

	return c.JSON(http.StatusOK, AggregateResponse{
		Rows: rows,
		Meta: AggregateMeta{
			GeneratedScopeQL: scopeql,
		},
	})
}

func (s *Server) buildAndRun(ctx context.Context, spec semantic.QuerySpec) ([]map[string]any, string, error) {
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

func (s *Server) writeInternalError(c echo.Context, err error) error {
	return s.writeError(c, http.StatusInternalServerError, "internal_error", "failed to execute query", map[string]any{
		"reason": err.Error(),
	})
}

func (s *Server) writeError(c echo.Context, status int, code string, message string, details map[string]any) error {
	return c.JSON(status, ErrorResponse{
		Error: ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
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
	rawTS, ok := last["ts"].(string)
	if !ok || strings.TrimSpace(rawTS) == "" {
		return "", nil
	}
	rawRowID, ok := last["row_id"].(string)
	if !ok || strings.TrimSpace(rawRowID) == "" {
		return "", nil
	}
	lastTS, err := time.Parse(time.RFC3339Nano, rawTS)
	if err != nil {
		return "", err
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
