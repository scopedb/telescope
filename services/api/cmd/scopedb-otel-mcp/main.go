package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/scopedb/telescope/services/api/internal/httpapi"
	"github.com/scopedb/telescope/services/api/internal/mcpserver"
	"github.com/scopedb/telescope/services/api/internal/scopedbexec"
	"github.com/scopedb/telescope/services/api/internal/semantic"
)

var version = "dev"

func main() {
	config, err := loadConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	runner := scopedbexec.New(config.ScopeDBEndpoint, config.ScopeDBAPIKey, config.QueryTimeout)
	defer runner.Close()

	service, err := httpapi.NewService(semantic.Default, runner, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build service: %v\n", err)
		os.Exit(1)
	}

	server, err := mcpserver.New(service, "scopedb-otel-mcp", version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build mcp server: %v\n", err)
		os.Exit(1)
	}

	if err := server.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "serve mcp: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	ScopeDBEndpoint string
	ScopeDBAPIKey   string
	QueryTimeout    time.Duration
}

func loadConfigFromEnv() (config, error) {
	endpoint := strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"))
	if endpoint == "" {
		return config{}, fmt.Errorf("TELESCOPE_SCOPEDB_ENDPOINT is required")
	}

	apiKey := strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_API_KEY"))
	if apiKey == "" {
		return config{}, fmt.Errorf("TELESCOPE_SCOPEDB_API_KEY is required")
	}

	queryTimeout := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("TELESCOPE_QUERY_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse TELESCOPE_QUERY_TIMEOUT: %w", err)
		}
		queryTimeout = parsed
	}

	return config{
		ScopeDBEndpoint: endpoint,
		ScopeDBAPIKey:   apiKey,
		QueryTimeout:    queryTimeout,
	}, nil
}
