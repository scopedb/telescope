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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

const queryTestStatementID = "01989a4e-4ee2-7e63-87a5-65ac3b5161dc"

func TestRunQueryCommandWritesTypedJSON(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/statements", r.URL.Path)
		require.Equal(t, "Bearer sk_test", r.Header.Get("Authorization"))

		body := decodeQueryRequestBody(t, r)
		var request struct {
			Statement   string `json:"statement"`
			ExecTimeout string `json:"exec_timeout"`
			Format      string `json:"format"`
		}
		require.NoError(t, json.Unmarshal(body, &request))
		require.Equal(t, "FROM scopedb.otel.traces LIMIT 1", request.Statement)
		require.Equal(t, "2s", request.ExecTimeout)
		require.Equal(t, "json", request.Format)

		writeQueryTestJSON(t, w, `{
			"statement_id":"`+queryTestStatementID+`",
			"status":"finished",
			"created_at":"2026-08-08T00:00:00Z",
			"progress":{},
			"result_set":{
				"metadata":{"fields":[
					{"name":"service","data_type":"string"},
					{"name":"duration_ns","data_type":"int"},
					{"name":"sampled","data_type":"boolean"},
					{"name":"labels","data_type":"object"},
					{"name":"missing","data_type":"string"}
				],"num_rows":1},
				"format":"json",
				"rows":[["demo","42","true","{\"team\":\"obs\"}",null]]
			}
		}`)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runQueryCommand(context.Background(), []string{
		"--scopedb-endpoint", server.URL,
		"--scopedb-api-key", "sk_test",
		"--timeout", "2s",
		"--format", "json",
		"FROM scopedb.otel.traces LIMIT 1",
	}, strings.NewReader(""), &stdout, &stderr)
	require.NoError(t, err)
	require.Empty(t, stderr.String())
	require.JSONEq(t, `[
		{
			"service":"demo",
			"duration_ns":42,
			"sampled":true,
			"labels":{"team":"obs"},
			"missing":null
		}
	]`, stdout.String())
}

func TestRunQueryCommandReadsStdinAndWritesTable(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeQueryRequestBody(t, r)
		var request struct {
			Statement string `json:"statement"`
		}
		require.NoError(t, json.Unmarshal(body, &request))
		require.Equal(t, "SELECT service, message FROM scopedb.otel.logs", request.Statement)
		writeQueryTestJSON(t, w, `{
			"statement_id":"`+queryTestStatementID+`",
			"status":"finished",
			"created_at":"2026-08-08T00:00:00Z",
			"progress":{},
			"result_set":{
				"metadata":{"fields":[
					{"name":"service","data_type":"string"},
					{"name":"message","data_type":"string"}
				],"num_rows":2},
				"format":"json",
				"rows":[["demo","ready\nnow"],[null,"idle"]]
			}
		}`)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	err := runQueryCommand(
		context.Background(),
		[]string{"--scopedb-endpoint", server.URL},
		strings.NewReader("  SELECT service, message FROM scopedb.otel.logs\n"),
		&stdout,
		io.Discard,
	)
	require.NoError(t, err)
	require.Equal(t, "service  message\ndemo     ready\\nnow\nNULL     idle\n(2 rows)\n", stdout.String())
}

func TestRunQueryCommandReportsStructuredFailure(t *testing.T) {
	clearBootstrapEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeQueryTestJSON(t, w, `{
			"statement_id":"`+queryTestStatementID+`",
			"status":"failed",
			"created_at":"2026-08-08T00:00:00Z",
			"progress":{},
			"message":"query preparation failed",
			"error":{
				"code":"prepare_error",
				"message":"unknown table scopedb.missing",
				"details":{"line":1,"column":6}
			}
		}`)
	}))
	defer server.Close()

	err := runQueryCommand(
		context.Background(),
		[]string{"--scopedb-endpoint", server.URL, "FROM scopedb.missing"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	require.EqualError(t, err, "statement "+queryTestStatementID+" failed [prepare_error]: unknown table scopedb.missing; details={\"line\":1,\"column\":6}")
}

func TestRunQueryCommandReadsFileAndWritesJSONL(t *testing.T) {
	clearBootstrapEnv(t)
	path := filepath.Join(t.TempDir(), "query.scopeql")
	require.NoError(t, os.WriteFile(path, []byte(" SELECT value FROM scopedb.values \n"), 0o600))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeQueryRequestBody(t, r)
		var request struct {
			Statement string `json:"statement"`
		}
		require.NoError(t, json.Unmarshal(body, &request))
		require.Equal(t, "SELECT value FROM scopedb.values", request.Statement)
		writeQueryTestJSON(t, w, `{
			"statement_id":"`+queryTestStatementID+`",
			"status":"finished",
			"created_at":"2026-08-08T00:00:00Z",
			"progress":{},
			"result_set":{
				"metadata":{"fields":[{"name":"value","data_type":"float"}],"num_rows":2},
				"format":"json",
				"rows":[["1.5"],["NaN"]]
			}
		}`)
	}))
	defer server.Close()

	var stdout bytes.Buffer
	err := runQueryCommand(
		context.Background(),
		[]string{"--scopedb-endpoint", server.URL, "--format", "jsonl", "--file", path},
		strings.NewReader(""),
		&stdout,
		io.Discard,
	)
	require.NoError(t, err)
	require.Equal(t, "{\"value\":1.5}\n{\"value\":null}\n", stdout.String())
}

func TestRunQueryCommandCancelsInterruptedStatement(t *testing.T) {
	clearBootstrapEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	polled := make(chan struct{})
	var polledOnce sync.Once
	var cancelled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/statements":
			writeQueryTestJSON(t, w, runningQueryResponse())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/statements/"+queryTestStatementID:
			polledOnce.Do(func() { close(polled) })
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/v1/statements/"+queryTestStatementID+"/cancel":
			cancelled.Store(true)
			writeQueryTestJSON(t, w, `{
				"statement_id":"`+queryTestStatementID+`",
				"created_at":"2026-08-08T00:00:00Z",
				"status":"cancelled",
				"message":"statement is cancelled"
			}`)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	go func() {
		<-polled
		cancel()
	}()

	err := runQueryCommand(
		ctx,
		[]string{"--scopedb-endpoint", server.URL, "FROM scopedb.otel.traces"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "did not complete: context canceled")
	require.ErrorContains(t, err, "ScopeDB status is cancelled")
	require.True(t, cancelled.Load(), "expected the remote statement to be cancelled")
}

func TestRunQueryCommandValidatesInputBeforeConnecting(t *testing.T) {
	clearBootstrapEnv(t)
	tests := []struct {
		name    string
		args    []string
		stdin   string
		wantErr string
	}{
		{name: "empty", stdin: " \n", wantErr: "ScopeQL statement is empty"},
		{name: "multiple arguments", args: []string{"SELECT 1", "SELECT 2"}, wantErr: "query accepts one ScopeQL argument, got 2"},
		{name: "invalid format", args: []string{"--format", "csv", "SELECT 1"}, wantErr: `unsupported query format "csv"`},
		{name: "invalid timeout", args: []string{"--timeout", "0s", "SELECT 1"}, wantErr: "--timeout must be greater than zero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runQueryCommand(context.Background(), test.args, strings.NewReader(test.stdin), io.Discard, io.Discard)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestRunQueryCommandRejectsFileAndArgument(t *testing.T) {
	clearBootstrapEnv(t)
	err := runQueryCommand(
		context.Background(),
		[]string{"--file", "query.scopeql", "SELECT 2"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	require.EqualError(t, err, "--file and a ScopeQL argument cannot be used together")
}

func decodeQueryRequestBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	require.Equal(t, "zstd", r.Header.Get("Content-Encoding"))
	decoder, err := zstd.NewReader(r.Body)
	require.NoError(t, err)
	defer decoder.Close()
	body, err := io.ReadAll(decoder)
	require.NoError(t, err)
	return body
}

func writeQueryTestJSON(t *testing.T, w http.ResponseWriter, value string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, err := io.WriteString(w, value)
	require.NoError(t, err)
}

func runningQueryResponse() string {
	return `{
		"statement_id":"` + queryTestStatementID + `",
		"status":"running",
		"created_at":"2026-08-08T00:00:00Z",
		"progress":{}
	}`
}
