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

package scopedbexporter

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestScopeDBAppendIntegration(t *testing.T) {
	endpoint := os.Getenv("TELESCOPE_SCOPEDB_INTEGRATION_ENDPOINT")
	apiKey := os.Getenv("TELESCOPE_SCOPEDB_INTEGRATION_API_KEY")
	tenantID := os.Getenv("TELESCOPE_SCOPEDB_INTEGRATION_TENANT_ID")
	if endpoint == "" || (apiKey == "" && tenantID == "") {
		t.Skip("set the integration endpoint and either API key or tenant ID")
	}

	requestEndpoint := endpoint
	if tenantID != "" {
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
		defer proxyServer.Close()
		requestEndpoint = proxyServer.URL
		apiKey = "integration-proxy"
	}

	sdkClient, err := scopedb.NewClient(scopedb.Config{
		Endpoint: requestEndpoint,
		APIKey:   apiKey,
	})
	require.NoError(t, err)
	defer sdkClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	table := sdkClient.Table(fmt.Sprintf("telescope_it_%d", time.Now().UnixNano()))
	t.Logf("temporary table: %s", table.Identifier())
	_, err = sdkClient.Statement(fmt.Sprintf(`
		CREATE TABLE %s (
			event_time timestamp,
			service string,
			message string,
			trace_id string,
		)
	`, table.Identifier())).Execute(ctx)
	require.NoError(t, err)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		assert.NoError(t, table.Drop(cleanupCtx))
	}()

	cfg := testClientConfig(requestEndpoint)
	cfg.APIKey = configopaque.String(apiKey)
	cfg.Tables.Logs = table.Name
	cfg.Mappings.Logs = shorthandMapping(map[string]string{
		"event_time": "log.timestamp",
		"service":    `resource.attributes["service.name"]`,
		"message":    "log.message",
		"trace_id":   "log.trace_id",
	})
	require.NoError(t, CheckIngestionDestinations(ctx, requestEndpoint, apiKey, IngestionConfig{
		Signals: IngestionSignalsConfig{
			Logs: SignalIngestionConfig{
				Table:   table.Name,
				Mapping: cfg.Mappings.Logs,
			},
		},
	}))
	client, err := NewClient(cfg, exportertest.NewNopSettings(typeStr))
	require.NoError(t, err)
	defer client.Close()
	require.NoError(t, client.ValidateDestination(ctx, signalLogs))

	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr("service.name", "checkout")
	record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.NewTimestampFromTime(time.Date(2026, 8, 26, 10, 0, 0, 123, time.UTC)))
	record.SetTraceID(pcommon.TraceID([16]byte{1, 2, 3}))
	record.Body().SetStr("telescope append integration")
	record.Attributes().PutStr("not.selected", "must not be stored")

	payload, err := mapLogs(logs)
	require.NoError(t, err)
	require.NoError(t, client.Send(ctx, signalLogs, payload))

	result, err := sdkClient.Query(ctx, fmt.Sprintf("FROM %s", table.Identifier()))
	require.NoError(t, err)
	objects, err := result.ToObjects()
	require.NoError(t, err)
	require.Len(t, objects, 1)
	assert.Equal(t, "checkout", objects[0]["service"])
	assert.Equal(t, "telescope append integration", objects[0]["message"])
	assert.Equal(t, "01020300000000000000000000000000", objects[0]["trace_id"])
	assert.Len(t, objects[0], 4)
}
