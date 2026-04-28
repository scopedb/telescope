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
