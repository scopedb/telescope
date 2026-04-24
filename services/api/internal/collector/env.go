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
	setDefaultEnv("TELESCOPE_ENV", "default")
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
