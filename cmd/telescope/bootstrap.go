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

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	telescopeScopeDBEndpointEnv = "TELESCOPE_SCOPEDB_ENDPOINT"
	telescopeScopeDBAPIKeyEnv   = "TELESCOPE_SCOPEDB_API_KEY"
	sharedScopeDBEndpointEnv    = "SCOPEDB_ENDPOINT"
	sharedScopeDBAPIKeyEnv      = "SCOPEDB_API_KEY"
)

type bootstrapFlags struct {
	envFile         *string
	scopedbEndpoint *string
	scopedbAPIKey   *string
}

func addBootstrapFlags(flags *flag.FlagSet) bootstrapFlags {
	return bootstrapFlags{
		envFile:         flags.String("env-file", "", "load bootstrap environment variables from a KEY=VALUE file"),
		scopedbEndpoint: flags.String("scopedb-endpoint", "", "ScopeDB physical region endpoint; overrides Telescope and shared ScopeDB environment"),
		scopedbAPIKey:   flags.String("scopedb-api-key", "", "ScopeDB API key; overrides Telescope and shared ScopeDB environment"),
	}
}

func applyBootstrapFlags(flags bootstrapFlags) error {
	if flags.envFile != nil && strings.TrimSpace(*flags.envFile) != "" {
		if err := loadEnvFile(*flags.envFile); err != nil {
			return err
		}
	}
	if err := setEnvFallback(telescopeScopeDBEndpointEnv, sharedScopeDBEndpointEnv); err != nil {
		return err
	}
	if err := setEnvFallback(telescopeScopeDBAPIKeyEnv, sharedScopeDBAPIKeyEnv); err != nil {
		return err
	}
	if err := setEnvIfValue(telescopeScopeDBEndpointEnv, valueOf(flags.scopedbEndpoint)); err != nil {
		return err
	}
	if err := setEnvIfValue(telescopeScopeDBAPIKeyEnv, valueOf(flags.scopedbAPIKey)); err != nil {
		return err
	}
	return nil
}

func setEnvFallback(target string, fallback string) error {
	if strings.TrimSpace(os.Getenv(target)) != "" {
		return nil
	}
	return setEnvIfValue(target, os.Getenv(fallback))
}

func scopeDBCredentials() (string, string) {
	endpoint := strings.TrimSpace(os.Getenv(telescopeScopeDBEndpointEnv))
	apiKey := strings.TrimSpace(os.Getenv(telescopeScopeDBAPIKeyEnv))
	return endpoint, apiKey
}

func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("load env file %s: %w", path, err)
	}
	for index, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("load env file %s: line %d is not KEY=VALUE", path, index+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("load env file %s: line %d has an empty key", path, index+1)
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if strings.TrimSpace(os.Getenv(key)) == "" {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("load env file %s: line %d: set %q: %w", path, index+1, key, err)
			}
		}
	}
	return nil
}

func setEnvIfValue(key string, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if err := os.Setenv(key, value); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return nil
}

func valueOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func resolveHTTPListenAddr(flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("TELESCOPE_HTTP_ADDR")); value != "" {
		return value
	}
	return ":8080"
}
