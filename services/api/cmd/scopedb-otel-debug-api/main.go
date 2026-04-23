package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/httpapi"
	"github.com/your-org/vendor-otel-gateway/services/api/internal/scopedbexec"
	"github.com/your-org/vendor-otel-gateway/services/api/internal/semantic"
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

	server, err := httpapi.New(semantic.Default, runner, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build server: %v\n", err)
		os.Exit(1)
	}

	if err := server.Start(config.ListenAddr); err != nil {
		fmt.Fprintf(os.Stderr, "start server: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	ListenAddr      string
	ScopeDBEndpoint string
	ScopeDBAPIKey   string
	QueryTimeout    time.Duration
}

func loadConfigFromEnv() (config, error) {
	listenAddr := strings.TrimSpace(os.Getenv("HTTP_ADDR"))
	if listenAddr == "" {
		port := strings.TrimSpace(os.Getenv("PORT"))
		if port != "" {
			listenAddr = ":" + port
		} else {
			listenAddr = ":8080"
		}
	}

	endpoint := strings.TrimSpace(os.Getenv("SCOPEDB_ENDPOINT"))
	if endpoint == "" {
		return config{}, fmt.Errorf("SCOPEDB_ENDPOINT is required")
	}

	apiKey := strings.TrimSpace(os.Getenv("SCOPEDB_API_KEY"))
	if apiKey == "" {
		return config{}, fmt.Errorf("SCOPEDB_API_KEY is required")
	}

	queryTimeout := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SCOPEDB_QUERY_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("parse SCOPEDB_QUERY_TIMEOUT: %w", err)
		}
		queryTimeout = parsed
	}

	return config{
		ListenAddr:      listenAddr,
		ScopeDBEndpoint: endpoint,
		ScopeDBAPIKey:   apiKey,
		QueryTimeout:    queryTimeout,
	}, nil
}
