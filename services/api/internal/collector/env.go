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

package collector

import (
	"os"
	"path/filepath"
	"strings"
)

func ApplyDefaultEnv() {
	setDefaultEnv("TELESCOPE_OTLP_GRPC_ADDR", "0.0.0.0:4317")
	setDefaultEnv("TELESCOPE_OTLP_HTTP_ADDR", "0.0.0.0:4318")
	setDefaultEnv("TELESCOPE_HEALTH_ADDR", "0.0.0.0:13133")
	setDefaultEnv("TELESCOPE_QUEUE_DIR", defaultQueueDir())
	setDefaultEnv("TELESCOPE_QUEUE_MAX_BYTES", "536870912")
	setDefaultEnv("TELESCOPE_OTEL_BATCH_TIMEOUT", "30s")
	setDefaultEnv("TELESCOPE_OTEL_BATCH_SIZE", "2000")
	setDefaultEnv("TELESCOPE_OTEL_BATCH_MAX_SIZE", "2000")
}

func setDefaultEnv(key string, value string) {
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return
	}
	_ = os.Setenv(key, value)
}

func defaultQueueDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "telescope", "queue")
	}
	return filepath.Join(home, ".telescope", "queue")
}
