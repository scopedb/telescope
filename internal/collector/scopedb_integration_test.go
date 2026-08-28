// Copyright 2026 ScopeDB, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build integration

package collector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"testing"
	"time"

	scopedb "github.com/scopedb/goscopedb"
	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopeDBTelescopeIntegration(t *testing.T) {
	endpoint := os.Getenv("TELESCOPE_SCOPEDB_INTEGRATION_ENDPOINT")
	apiKey := os.Getenv("TELESCOPE_SCOPEDB_INTEGRATION_API_KEY")
	tenantID := os.Getenv("TELESCOPE_SCOPEDB_INTEGRATION_TENANT_ID")
	if endpoint == "" || (apiKey == "" && tenantID == "") {
		t.Skip("set the integration endpoint and either API key or tenant ID")
	}

	requestEndpoint, requestAPIKey := integrationRequestEndpoint(t, endpoint, apiKey, tenantID)
	sdkClient, err := scopedb.NewClient(scopedb.Config{Endpoint: requestEndpoint, APIKey: requestAPIKey})
	require.NoError(t, err)
	defer sdkClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prefix := fmt.Sprintf("telescope_e2e_%d", time.Now().UnixNano())
	tables := map[string]*scopedb.Table{
		"logs":    sdkClient.Table(prefix + "_logs"),
		"traces":  sdkClient.Table(prefix + "_traces"),
		"metrics": sdkClient.Table(prefix + "_metrics"),
	}
	var createdTables []*scopedb.Table
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		for _, table := range createdTables {
			assert.NoError(t, table.Drop(cleanupCtx))
		}
	})
	for _, signal := range []string{"logs", "traces", "metrics"} {
		table := tables[signal]
		_, err := sdkClient.Statement(fmt.Sprintf("CREATE TABLE %s (value string)", table.Identifier())).Execute(ctx)
		require.NoError(t, err)
		createdTables = append(createdTables, table)
		t.Logf("temporary %s table: %s", signal, table.Identifier())
	}

	ingestion := scopedbexporter.IngestionConfig{Signals: scopedbexporter.IngestionSignalsConfig{
		Logs: scopedbexporter.SignalIngestionConfig{
			Table: tables["logs"].Name,
			Mapping: scopedbexporter.MappingConfig{"value": {
				Sources: []string{`resource.attributes["missing"]`, "log.message"},
				Cast:    "string",
			}},
		},
		Traces: scopedbexporter.SignalIngestionConfig{
			Table:   tables["traces"].Name,
			Mapping: scopedbexporter.MappingConfig{"value": {Source: "span.name"}},
		},
		Metrics: scopedbexporter.SignalIngestionConfig{
			Table:   tables["metrics"].Name,
			Mapping: scopedbexporter.MappingConfig{"value": {Source: "metric.name"}},
		},
	}}
	require.NoError(t, scopedbexporter.CheckIngestionDestinations(ctx, requestEndpoint, requestAPIKey, ingestion))
	configURI, err := ConfigURI(ingestion)
	require.NoError(t, err)

	otlpHTTPAddress := freeTCPAddress(t)
	t.Setenv("TELESCOPE_SCOPEDB_ENDPOINT", requestEndpoint)
	t.Setenv("TELESCOPE_SCOPEDB_API_KEY", requestAPIKey)
	t.Setenv("TELESCOPE_OTLP_GRPC_ADDR", freeTCPAddress(t))
	t.Setenv("TELESCOPE_OTLP_HTTP_ADDR", otlpHTTPAddress)
	t.Setenv("TELESCOPE_QUEUE_DIR", t.TempDir())
	t.Setenv("TELESCOPE_OTEL_BATCH_TIMEOUT", "10ms")
	t.Setenv("TELESCOPE_OTEL_BATCH_SIZE", "1")
	t.Setenv("TELESCOPE_OTEL_BATCH_MAX_SIZE", "1")
	runtime := startCollectorFromURI(t, configURI)
	defer runtime.stop(t)

	values := map[string]string{
		"logs":    "telescope live log",
		"traces":  "telescope live span",
		"metrics": "telescope.live.metric",
	}
	for _, signal := range []string{"logs", "traces", "metrics"} {
		require.NoError(t, sendRecoverySignal(otlpHTTPAddress, signal, values[signal]))
	}
	for _, signal := range []string{"logs", "traces", "metrics"} {
		table := tables[signal]
		require.EventuallyWithT(t, func(collect *assert.CollectT) {
			result, err := sdkClient.Query(ctx, fmt.Sprintf("FROM %s", table.Identifier()))
			if !assert.NoError(collect, err) {
				return
			}
			objects, err := result.ToObjects()
			if !assert.NoError(collect, err) || !assert.Len(collect, objects, 1) {
				return
			}
			assert.Equal(collect, values[signal], objects[0]["value"])
		}, 20*time.Second, 200*time.Millisecond)
	}
}

func integrationRequestEndpoint(t *testing.T, endpoint string, apiKey string, tenantID string) (string, string) {
	t.Helper()
	if tenantID == "" {
		return endpoint, apiKey
	}
	target, err := url.Parse(endpoint)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = target.Host
		request.Header.Del("Authorization")
		request.Header.Set("X-ScopeDB-Tenant-Id", tenantID)
	}
	proxyServer := httptest.NewServer(proxy)
	t.Cleanup(proxyServer.Close)
	return proxyServer.URL, "integration-proxy"
}
