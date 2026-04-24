package appruntime

import (
	"fmt"

	"github.com/labstack/echo/v4"

	"github.com/scopedb/telescope/services/api/internal/httpapi"
	"github.com/scopedb/telescope/services/api/internal/mcpserver"
	"github.com/scopedb/telescope/services/api/internal/scopedbexec"
	"github.com/scopedb/telescope/services/api/internal/semantic"
)

type App struct {
	Runner  *scopedbexec.Runner
	Service *httpapi.Service
}

func New(config Config, version string) (*App, error) {
	runner := scopedbexec.New(config.ScopeDBEndpoint, config.ScopeDBAPIKey, config.QueryTimeout)

	service, err := httpapi.NewService(semantic.Default, runner, version)
	if err != nil {
		runner.Close()
		return nil, fmt.Errorf("build service: %w", err)
	}

	return &App{
		Runner:  runner,
		Service: service,
	}, nil
}

func (a *App) Close() error {
	if a == nil || a.Runner == nil {
		return nil
	}
	return a.Runner.Close()
}

func (a *App) HTTPServer(version string) (*echo.Echo, error) {
	server, err := httpapi.NewWithService(a.Service)
	if err != nil {
		return nil, fmt.Errorf("build HTTP server: %w", err)
	}

	mcp, err := mcpserver.New(a.Service, "telescope", version)
	if err != nil {
		return nil, fmt.Errorf("build MCP server: %w", err)
	}
	server.Any("/mcp", echo.WrapHandler(mcp))

	return server, nil
}

func (a *App) MCPServer(version string) (*mcpserver.Server, error) {
	server, err := mcpserver.New(a.Service, "telescope", version)
	if err != nil {
		return nil, fmt.Errorf("build MCP server: %w", err)
	}
	return server, nil
}
