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
	"bytes"
	"context"
	"errors"
	"flag"
	"github.com/scopedb/telescope/internal/collector"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var errTestWrite = errors.New("test write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errTestWrite
}

func TestRunCommandWritesVersionToStdout(t *testing.T) {
	originalVersion := version
	version = "v-test"
	t.Cleanup(func() { version = originalVersion })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCommand(context.Background(), []string{"version"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runCommand() error = %v", err)
	}
	if got := stdout.String(); got != "v-test\n" {
		t.Fatalf("runCommand() stdout = %q, want %q", got, "v-test\\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("runCommand() stderr = %q, want none", stderr.String())
	}
}

func TestRunCommandWritesUsageToStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runCommand(context.Background(), nil, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runCommand() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("runCommand() stdout = %q, want none", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("runCommand() stderr = %q, want usage", stderr.String())
	}
}

func TestCommandHelpNeedsNoConfiguration(t *testing.T) {
	clearBootstrapEnv(t)
	t.Chdir(t.TempDir())
	for _, command := range []string{"run", "validate", "preview", "inspect", "plan", "capture", "verify", "status", "query"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runCommand(t.Context(), []string{command, "--help"}, strings.NewReader(""), &stdout, &stderr)
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("help: %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("help must go only to stderr: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunCommandReportsOutputFailure(t *testing.T) {
	err := runCommand(
		context.Background(),
		[]string{"version"},
		strings.NewReader(""),
		failingWriter{},
		io.Discard,
	)
	if !errors.Is(err, errTestWrite) {
		t.Fatalf("runCommand() error = %v, want output failure", err)
	}
}

func TestRunCommandDoesNotHideHelpOutputFailure(t *testing.T) {
	err := runCommand(
		context.Background(),
		[]string{"status", "--help"},
		strings.NewReader(""),
		io.Discard,
		failingWriter{},
	)
	if !errors.Is(err, errTestWrite) {
		t.Fatalf("runCommand() error = %v, want help output failure", err)
	}
	if errors.Is(err, flag.ErrHelp) {
		t.Fatalf("runCommand() error = %v hides the output failure as help", err)
	}
}

func TestLoadEnvFileSetsMissingValues(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, `
# bootstrap config
TELESCOPE_SCOPEDB_ENDPOINT="https://file.scopedb.cloud"
TELESCOPE_SCOPEDB_API_KEY='sk_file'
`)

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("loadEnvFile() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://file.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_file" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestLoadEnvFileDoesNotOverrideExistingEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://env.scopedb.cloud")
	path := writeEnvFile(t, `
TELESCOPE_SCOPEDB_ENDPOINT=https://file.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_file
`)

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("loadEnvFile() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://env.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_file" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsOverrideEnvFile(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, `
TELESCOPE_SCOPEDB_ENDPOINT=https://file.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_file
`)
	endpoint := "https://flag.scopedb.cloud"

	err := applyBootstrapFlags(bootstrapFlags{
		envFile:         &path,
		scopedbEndpoint: &endpoint,
	})
	if err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://flag.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_file" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsUsesSharedScopeDBEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, `
SCOPEDB_ENDPOINT=https://shared.scopedb.cloud
SCOPEDB_API_KEY=sk_shared
`)

	if err := applyBootstrapFlags(bootstrapFlags{envFile: &path}); err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://shared.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_shared" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsPrefersTelescopeEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	t.Setenv("SCOPEDB_ENDPOINT", "https://shared.scopedb.cloud")
	t.Setenv("SCOPEDB_API_KEY", "sk_shared")
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://telescope.scopedb.cloud")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "sk_telescope")

	if err := applyBootstrapFlags(bootstrapFlags{}); err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != "https://telescope.scopedb.cloud" {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != "sk_telescope" {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestApplyBootstrapFlagsPreferFlagsOverEveryEnvironment(t *testing.T) {
	clearBootstrapEnv(t)
	t.Setenv("SCOPEDB_ENDPOINT", "https://shared.scopedb.cloud")
	t.Setenv("SCOPEDB_API_KEY", "sk_shared")
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "https://telescope.scopedb.cloud")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "sk_telescope")
	endpoint := "https://flag.scopedb.cloud"
	apiKey := "sk_flag"

	if err := applyBootstrapFlags(bootstrapFlags{scopedbEndpoint: &endpoint, scopedbAPIKey: &apiKey}); err != nil {
		t.Fatalf("applyBootstrapFlags() error = %v", err)
	}

	if got := os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT"); got != endpoint {
		t.Fatalf("TELESCOPE_SCOPEDB_ENDPOINT = %q", got)
	}
	if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != apiKey {
		t.Fatalf("TELESCOPE_SCOPEDB_API_KEY = %q", got)
	}
}

func TestLoadEnvFileRejectsInvalidLine(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, "TELESCOPE_SCOPEDB_ENDPOINT\n")

	if err := loadEnvFile(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadEnvFileRejectsInvalidEnvironmentValue(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, "TELESCOPE_SCOPEDB_ENDPOINT=https://bad\x00value\n")

	if err := loadEnvFile(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestSetEnvIfValueRejectsInvalidEnvironmentValue(t *testing.T) {
	if err := setEnvIfValue("TELESCOPE_SCOPEDB_ENDPOINT", "https://bad\x00value"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveHTTPListenAddr(t *testing.T) {
	tests := []struct {
		name     string
		flag     string
		addr     string
		expected string
	}{
		{name: "default", expected: "127.0.0.1:8080"},
		{name: "environment", addr: "127.0.0.1:9090", expected: "127.0.0.1:9090"},
		{name: "flag before environment", flag: "127.0.0.1:7070", addr: "127.0.0.1:9090", expected: "127.0.0.1:7070"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TELESCOPE_HTTP_ADDR", tt.addr)
			if got := resolveHTTPListenAddr(tt.flag); got != tt.expected {
				t.Fatalf("resolveHTTPListenAddr() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestReportReadiness(t *testing.T) {
	t.Setenv("TELESCOPE_OTLP_GRPC_ADDR", "127.0.0.1:14317")
	t.Setenv("TELESCOPE_OTLP_HTTP_ADDR", "127.0.0.1:14318")
	checks := 0
	var output bytes.Buffer

	reportReadiness(context.Background(), func(context.Context) bool {
		checks++
		return checks == 2
	}, &output, time.Millisecond, "127.0.0.1:18080", collector.ReceiverConfig{GRPCAddr: "127.0.0.1:14317", HTTPAddr: "127.0.0.1:14318"})

	want := "telescope ready: otlp_grpc=127.0.0.1:14317 otlp_http=127.0.0.1:14318 http=127.0.0.1:18080\n"
	if output.String() != want {
		t.Fatalf("reportReadiness() output = %q, want %q", output.String(), want)
	}
}

func TestReportReadinessStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer

	reportReadiness(ctx, func(context.Context) bool { return true }, &output, time.Millisecond, "127.0.0.1:18080", collector.ReceiverConfig{GRPCAddr: "127.0.0.1:14317", HTTPAddr: "127.0.0.1:14318"})

	if output.Len() != 0 {
		t.Fatalf("reportReadiness() output = %q, want none", output.String())
	}
}

func clearBootstrapEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", "")
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", "")
	t.Setenv("SCOPEDB_ENDPOINT", "")
	t.Setenv("SCOPEDB_API_KEY", "")
}

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestCheckedWriterSerializesConcurrentOutput(t *testing.T) {
	var output bytes.Buffer
	writer := &checkedWriter{name: "stderr", target: &output}
	var writers sync.WaitGroup
	start := make(chan struct{})
	for range 16 {
		writers.Go(func() {
			<-start
			for range 100 {
				_, _ = writer.Write([]byte("ready\n"))
				_ = writer.Err()
			}
		})
	}
	close(start)
	writers.Wait()
	if err := writer.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "ready\n"); got != 1600 {
		t.Fatalf("lost concurrent output: got %d lines", got)
	}
}

func TestLoadEnvFilePreservesLiteralQuotes(t *testing.T) {
	for _, entry := range []struct{ value, want string }{
		{`"'secret'"`, `'secret'`},
		{`'"secret"'`, `"secret"`},
		{`secret'`, `secret'`},
	} {
		t.Run(entry.value, func(t *testing.T) {
			clearBootstrapEnv(t)
			if err := loadEnvFile(writeEnvFile(t, "TELESCOPE_SCOPEDB_API_KEY="+entry.value)); err != nil {
				t.Fatal(err)
			}
			if got := os.Getenv("TELESCOPE_SCOPEDB_API_KEY"); got != entry.want {
				t.Fatalf("credential changed: got %q, want %q", got, entry.want)
			}
		})
	}
	clearBootstrapEnv(t)
	if err := loadEnvFile(writeEnvFile(t, `TELESCOPE_SCOPEDB_API_KEY="secret'`)); err == nil {
		t.Fatal("accepted an unmatched quote")
	}
}
