package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/scopedb/telescope/services/api/internal/semantic"
)

const serviceName = "scopedb-otel-debug-api"

type Server struct {
	service TelemetryService
}

func New(registry semantic.Registry, runner QueryRunner, version string) (*echo.Echo, error) {
	service, err := NewService(registry, runner, version)
	if err != nil {
		return nil, err
	}
	return NewWithService(service)
}

func NewWithService(service TelemetryService) (*echo.Echo, error) {
	if service == nil {
		return nil, errors.New("service is required")
	}
	server := &Server{service: service}
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
	return c.JSON(http.StatusOK, s.service.Health(c.Request().Context()))
}

func (s *Server) getSchema(c echo.Context) error {
	response, err := s.service.Schema(c.Request().Context())
	if err != nil {
		return s.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) getSchemaGuide(c echo.Context) error {
	guide, err := s.service.SchemaGuide(c.Request().Context())
	if err != nil {
		return s.writeServiceError(c, err)
	}
	c.Response().Header().Set(echo.HeaderContentType, "text/markdown; charset=utf-8")
	return c.String(http.StatusOK, guide)
}

func (s *Server) postSearch(c echo.Context) error {
	var request SearchRequest
	if err := c.Bind(&request); err != nil {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "invalid request body", map[string]any{
			"reason": err.Error(),
		})
	}
	response, err := s.service.Search(c.Request().Context(), request)
	if err != nil {
		return s.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, response)
}

func (s *Server) postAggregate(c echo.Context) error {
	var request AggregateRequest
	if err := c.Bind(&request); err != nil {
		return s.writeError(c, http.StatusBadRequest, "bad_request", "invalid request body", map[string]any{
			"reason": err.Error(),
		})
	}
	response, err := s.service.Aggregate(c.Request().Context(), request)
	if err != nil {
		return s.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, response)
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

func (s *Server) writeServiceError(c echo.Context, err error) error {
	if serviceErr, ok := err.(*ServiceError); ok {
		return s.writeError(c, serviceErr.Status, serviceErr.Code, serviceErr.Message, serviceErr.Details)
	}
	return s.writeError(c, http.StatusInternalServerError, "internal_error", "failed to execute query", map[string]any{
		"reason": err.Error(),
	})
}
