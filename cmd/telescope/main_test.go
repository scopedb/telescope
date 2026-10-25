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
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
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
	require.NoError(t, runCommand(context.Background(), []string{"version"}, strings.NewReader(""), &stdout, &stderr))
	require.Equal(t, "v-test\n", stdout.String())
	require.Equal(t, 0, stderr.Len())
}

func TestRunCommandWritesUsageToStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	require.NoError(t, runCommand(context.Background(), nil, strings.NewReader(""), &stdout, &stderr))
	require.Equal(t, 0, stdout.Len())
	require.Contains(t, stderr.String(), "Usage:")
}

func TestCommandHelpNeedsNoConfiguration(t *testing.T) {
	clearBootstrapEnv(t)
	t.Chdir(t.TempDir())
	for _, command := range []string{"run", "validate", "preview", "inspect", "plan", "capture", "verify", "status", "query"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runCommand(t.Context(), []string{command, "--help"}, strings.NewReader(""), &stdout, &stderr)
			require.ErrorIs(t, err, flag.ErrHelp)
			require.Empty(t, stdout.String())
			require.NotEmpty(t, stderr.String())
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
	require.ErrorIs(t, err, errTestWrite)
}

func TestRunCommandDoesNotHideHelpOutputFailure(t *testing.T) {
	err := runCommand(
		context.Background(),
		[]string{"status", "--help"},
		strings.NewReader(""),
		io.Discard,
		failingWriter{},
	)
	require.ErrorIs(t, err, errTestWrite)
	require.NotErrorIs(t, err, flag.ErrHelp)
}

func TestLoadEnvFileRejectsInvalidLine(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, "TELESCOPE_SCOPEDB_ENDPOINT\n")
	require.Error(t, loadEnvFile(path))
}

func TestLoadEnvFileRejectsInvalidEnvironmentValue(t *testing.T) {
	clearBootstrapEnv(t)
	path := writeEnvFile(t, "TELESCOPE_SCOPEDB_ENDPOINT=https://bad\x00value\n")
	require.Error(t, loadEnvFile(path))
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
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

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
	require.NoError(t, writer.Err())
	require.Equal(t, 1600, strings.Count(output.String(), "ready\n"))
}

func TestLoadEnvFilePreservesLiteralQuotes(t *testing.T) {
	for _, entry := range []struct{ value, want string }{
		{`"'secret'"`, `'secret'`},
		{`'"secret"'`, `"secret"`},
		{`secret'`, `secret'`},
	} {
		t.Run(entry.value, func(t *testing.T) {
			clearBootstrapEnv(t)
			require.NoError(t, loadEnvFile(writeEnvFile(t, "TELESCOPE_SCOPEDB_API_KEY="+entry.value)))
			require.Equal(t, entry.want, os.Getenv("TELESCOPE_SCOPEDB_API_KEY"))
		})
	}
	clearBootstrapEnv(t)
	require.Error(t, loadEnvFile(writeEnvFile(t, `TELESCOPE_SCOPEDB_API_KEY="secret'`)))
}

func TestBootstrapPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, shared, telescope, flag, want string
	}{
		{"file fallback", "", "", "", "file"},
		{"shared environment", "shared", "", "", "shared"},
		{"Telescope environment", "shared", "telescope", "", "telescope"},
		{"flag override", "shared", "telescope", "flag", "flag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearBootstrapEnv(t)
			t.Setenv("SCOPEDB_ENDPOINT", tc.shared)
			t.Setenv("SCOPEDB_API_KEY", tc.shared)
			t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", tc.telescope)
			t.Setenv("TELESCOPE_SCOPEDB_API_KEY", tc.telescope)
			path := writeEnvFile(t, "# Credentials\nSCOPEDB_ENDPOINT=\"file\"\nSCOPEDB_API_KEY='file'\n")
			require.NoError(t, applyBootstrapFlags(bootstrapFlags{envFile: &path, scopedbEndpoint: &tc.flag, scopedbAPIKey: &tc.flag}))
			endpoint, key := scopeDBCredentials()
			require.Equal(t, tc.want, endpoint)
			require.Equal(t, tc.want, key)
		})
	}
}

func TestCommandRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"validate", "--sample", "logs=sample.json"}, "flag provided but not defined"},
		{[]string{"validate", "--offline", "--timeout", "0s"}, "--timeout must be greater than zero"},
		{[]string{"preview", "--offline"}, "preview requires at least one --sample"},
		{[]string{"plan", "--format", "scopeql", "--out", "tables.scopeql"}, "--out requires --format human"},
		{[]string{"plan", "--format", "yaml"}, "unsupported plan format"},
		{[]string{"plan", "--timeout", "0s"}, "--timeout must be greater than zero"},
		{[]string{"capture", "--endpoint", "http://localhost:8080", "--listen-http", "localhost:4318", "traces"}, "cannot be used together"},
		{[]string{"capture", "profiles"}, "unsupported capture signal"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			require.ErrorContains(t, runCommand(t.Context(), tc.args, strings.NewReader(""), io.Discard, io.Discard), tc.want)
		})
	}
}
