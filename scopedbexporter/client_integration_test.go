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

package scopedbexporter

import (
	"context"
	"fmt"
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
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func scopeDBIntegrationClient(t *testing.T) (*scopedb.Client, string, string) {
	t.Helper()
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
		proxy := &httputil.ReverseProxy{
			Rewrite: func(request *httputil.ProxyRequest) {
				request.SetURL(target)
				request.Out.Header.Del("Authorization")
				request.Out.Header.Set("X-ScopeDB-Tenant-Id", tenantID)
			},
		}
		proxyServer := httptest.NewServer(proxy)
		t.Cleanup(proxyServer.Close)
		requestEndpoint = proxyServer.URL
		apiKey = "integration-proxy"
	}

	sdkClient, err := scopedb.NewClient(scopedb.Config{
		Endpoint: requestEndpoint,
		APIKey:   apiKey,
	})
	require.NoError(t, err)
	t.Cleanup(sdkClient.Close)

	return sdkClient, requestEndpoint, apiKey
}

func TestScopeDBAppendIntegration(t *testing.T) {
	sdkClient, requestEndpoint, apiKey := scopeDBIntegrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	table := sdkClient.Table(fmt.Sprintf("telescope_it_%d", time.Now().UnixNano()))
	t.Logf("temporary table: %s", table.Identifier())
	_, err := sdkClient.Statement(fmt.Sprintf(`
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
		_, cleanupErr := table.Catalog().Delete(cleanupCtx, scopedb.CatalogOptions{AllowDestructive: true})
		assert.NoError(t, cleanupErr)
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
	client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
	require.NoError(t, err)
	defer client.Close()
	_, validationErr := client.plans[signalLogs].inspectDestination(ctx, client.Client)
	require.NoError(t, validationErr)

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
	outcome := client.send(ctx, ctx, signalLogs, payload, nil)
	require.NoError(t, outcome.err)
	require.Len(t, outcome.committed, len(payload.Records))

	result, err := sdkClient.Query(ctx, fmt.Sprintf("FROM %s", table.Identifier()))
	require.NoError(t, err)
	objects, err := result.ToObjects()
	require.NoError(t, err)
	require.Len(t, objects, 1)
	assert.Equal(t, "checkout", objects[0]["service"])
	assert.Equal(t, "telescope append integration", objects[0]["message"])
	assert.Equal(t, "01020300000000000000000000000000", objects[0]["trace_id"])
	assert.Len(t, objects[0], 4)

	outcome = client.send(ctx, ctx, signalLogs, &ingestPayload{Records: []telemetryRecord{
		{"message": "before invalid row"},
		{"message": 42},
		{"message": "after invalid row"},
	}}, nil)
	require.NoError(t, outcome.err)
	assert.Equal(t, []int{0, 2}, outcome.committed)
	require.Len(t, outcome.rejected, 1)
	assert.Equal(t, 1, outcome.rejected[0].index)

	result, err = sdkClient.Query(ctx, fmt.Sprintf("FROM %s", table.Identifier()))
	require.NoError(t, err)
	objects, err = result.ToObjects()
	require.NoError(t, err)
	require.Len(t, objects, 3)
	messages := make([]string, 0, len(objects))
	for _, object := range objects {
		messages = append(messages, object["message"].(string))
	}
	assert.ElementsMatch(t, []string{
		"telescope append integration",
		"before invalid row",
		"after invalid row",
	}, messages)
}

func TestScopeDBTraceAndMetricAppendIntegration(t *testing.T) {
	sdkClient, endpoint, apiKey := scopeDBIntegrationClient(t)
	traces, err := (&ptrace.JSONUnmarshaler{}).UnmarshalTraces(readGoldenFile(t, "traces.otlp.json"))
	require.NoError(t, err)
	tracePayload, err := mapTraces(traces)
	require.NoError(t, err)
	metrics, err := (&pmetric.JSONUnmarshaler{}).UnmarshalMetrics(readGoldenFile(t, "metrics.otlp.json"))
	require.NoError(t, err)
	// An integer beyond float64's exact range must retain its value in an int column.
	const integerValue int64 = 1<<53 + 1
	metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(1).Sum().DataPoints().At(0).SetIntValue(integerValue)
	metricPayload, err := mapMetrics(metrics)
	require.NoError(t, err)

	for _, signal := range []string{signalTraces, signalMetrics} {
		t.Run(signal, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			table := sdkClient.Table(fmt.Sprintf("telescope_%s_it_%d", signal, time.Now().UnixNano()))
			cfg := createDefaultConfig().(*Config)
			cfg.Endpoint = endpoint
			cfg.APIKey = configopaque.String(apiKey)
			columns := "event_time timestamp, trace_id string, parent_span_id string, duration_ns int, attributes object, events array"
			payload := tracePayload
			if signal == signalTraces {
				cfg.Tables.Traces = table.Name
				cfg.Mappings.Traces = shorthandMapping(map[string]string{
					"event_time": "span.start_time", "trace_id": "span.trace_id",
					"parent_span_id": "span.parent_span_id", "duration_ns": "span.duration_ns",
					"attributes": "span.attributes", "events": "span.events",
				})
			} else {
				columns = "event_time timestamp, name string, int_value int, double_value float, distribution object"
				payload = metricPayload
				cfg.Tables.Metrics = table.Name
				cfg.Mappings.Metrics = shorthandMapping(map[string]string{
					"event_time": "datapoint.timestamp", "name": "metric.name",
					"int_value": "datapoint.int_value", "double_value": "datapoint.double_value",
					"distribution": "datapoint.distribution",
				})
			}
			_, err := sdkClient.Query(ctx, fmt.Sprintf("CREATE TABLE %s (%s)", table.Identifier(), columns))
			require.NoError(t, err)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_, err := table.Catalog().Delete(ctx, scopedb.CatalogOptions{AllowDestructive: true})
				assert.NoError(t, err)
			})
			client, err := newClient(cfg, exportertest.NewNopSettings(typeStr).Logger)
			require.NoError(t, err)
			defer client.Close()
			_, validationErr := client.plans[signal].inspectDestination(ctx, client.Client)
			require.NoError(t, validationErr)
			outcome := client.send(ctx, ctx, signal, payload, nil)
			require.NoError(t, outcome.err)
			require.Len(t, outcome.committed, len(payload.Records))

			query := "FROM " + table.Identifier()
			if signal == signalTraces {
				query += ` SELECT event_time, trace_id, parent_span_id, duration_ns,
					attributes['http.request.method']::string AS method,
					events[0]['name']::string AS event_name`
			} else {
				query += ` SELECT event_time, name, int_value, double_value,
					distribution['count']::int AS count ORDER BY event_time`
			}
			result, err := sdkClient.Query(ctx, query)
			require.NoError(t, err)
			rows, err := result.ToObjects()
			require.NoError(t, err)
			if signal == signalTraces {
				require.Len(t, rows, 1)
				assert.Equal(t, time.Unix(0, 2000).UTC(), rows[0]["event_time"])
				assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", rows[0]["trace_id"])
				assert.Equal(t, "0807060504030201", rows[0]["parent_span_id"])
				assert.Equal(t, int64(500), rows[0]["duration_ns"])
				assert.Equal(t, "POST", rows[0]["method"])
				assert.Equal(t, "validation.complete", rows[0]["event_name"])
			} else {
				require.Len(t, rows, 5)
				assert.Equal(t, time.Unix(0, 2000).UTC(), rows[0]["event_time"])
				assert.Equal(t, "system.load", rows[0]["name"])
				assert.Equal(t, 0.75, rows[0]["double_value"])
				assert.Nil(t, rows[0]["int_value"])
				assert.Equal(t, integerValue, rows[1]["int_value"])
				assert.Nil(t, rows[1]["double_value"])
				assert.Equal(t, int64(3), rows[2]["count"])
				assert.Equal(t, int64(4), rows[3]["count"])
				assert.Equal(t, int64(2), rows[4]["count"])
			}
		})
	}
}
