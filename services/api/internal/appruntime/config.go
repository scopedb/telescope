package appruntime

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr      string
	ScopeDBEndpoint string
	ScopeDBAPIKey   string
	QueryTimeout    time.Duration
}

func LoadConfigFromEnv() (Config, error) {
	listenAddr := strings.TrimSpace(os.Getenv("TELESCOPE_HTTP_ADDR"))
	if listenAddr == "" {
		port := strings.TrimSpace(os.Getenv("TELESCOPE_PORT"))
		if port != "" {
			listenAddr = ":" + port
		} else {
			listenAddr = ":8080"
		}
	}

	endpoint := strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"))
	if endpoint == "" {
		return Config{}, fmt.Errorf("TELESCOPE_SCOPEDB_ENDPOINT is required")
	}

	apiKey := strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_API_KEY"))
	if apiKey == "" {
		return Config{}, fmt.Errorf("TELESCOPE_SCOPEDB_API_KEY is required")
	}

	queryTimeout := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("TELESCOPE_QUERY_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parse TELESCOPE_QUERY_TIMEOUT: %w", err)
		}
		queryTimeout = parsed
	}

	return Config{
		ListenAddr:      listenAddr,
		ScopeDBEndpoint: endpoint,
		ScopeDBAPIKey:   apiKey,
		QueryTimeout:    queryTimeout,
	}, nil
}
