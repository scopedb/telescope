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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestMapTraces(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Endpoint = "https://scopedb.invalid"
	cfg.APIKey = configopaque.String("test-api-key")

	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	resourceSpans.Resource().Attributes().PutStr("service.name", "checkout")
	resourceSpans.Resource().Attributes().PutStr("service.instance.id", "checkout-1")
	resourceSpans.Resource().Attributes().PutStr("k8s.pod.name", "checkout-pod")
	resourceSpans.Resource().Attributes().PutStr("host.name", "checkout-node")
	resourceSpans.Resource().Attributes().PutEmptySlice("host.ip").AppendEmpty().SetStr("10.0.0.10")

	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	scopeSpans.Scope().SetName("test-scope")
	scopeSpans.Scope().SetVersion("1.0.0")

	span := scopeSpans.Spans().AppendEmpty()
	span.SetTraceID(pcommon.TraceID([16]byte{1, 2, 3}))
	span.SetSpanID(pcommon.SpanID([8]byte{4, 5, 6}))
	span.SetParentSpanID(pcommon.SpanID([8]byte{7, 8, 9}))
	span.SetName("GET /checkout")
	span.SetKind(ptrace.SpanKindServer)
	span.SetStartTimestamp(pcommon.Timestamp(100))
	span.SetEndTimestamp(pcommon.Timestamp(200))
	span.Status().SetCode(ptrace.StatusCodeError)
	span.Status().SetMessage("boom")
	span.Attributes().PutStr("http.method", "GET")

	event := span.Events().AppendEmpty()
	event.SetName("db.query")
	event.SetTimestamp(pcommon.Timestamp(150))
	event.Attributes().PutStr("db.system", "postgresql")

	link := span.Links().AppendEmpty()
	link.SetTraceID(pcommon.TraceID([16]byte{9, 9, 9}))
	link.SetSpanID(pcommon.SpanID([8]byte{8, 8, 8}))
	link.Attributes().PutStr("link.kind", "follows_from")

	payload, err := mapTraces(cfg, traces)
	require.NoError(t, err)
	require.Len(t, payload.Records, 1)

	mapped := payload.Records[0]
	assert.Equal(t, signalTraces, payload.Signal)
	assert.Equal(t, "01020300000000000000000000000000", mapped["trace_id"])
	assert.Equal(t, "0405060000000000", mapped["span_id"])
	assert.Equal(t, "0708090000000000", mapped["parent_span_id"])
	assert.Equal(t, "GET /checkout", mapped["name"])
	assert.Equal(t, "server", mapped["kind"])
	assert.Equal(t, "100", mapped["start_time_unix_nano"])
	assert.Equal(t, "200", mapped["end_time_unix_nano"])
	assert.Equal(t, int64(100), mapped["duration_ns"])
	assert.Equal(t, "error", mapped["status_code"])
	assert.Equal(t, "boom", mapped["status_message"])
	assert.Equal(t, map[string]any{
		"service.name":        "checkout",
		"service.instance.id": "checkout-1",
		"k8s.pod.name":        "checkout-pod",
		"host.name":           "checkout-node",
		"host.ip":             []any{"10.0.0.10"},
	}, mapped["resource"])
	assert.Equal(t, map[string]any{"name": "test-scope", "version": "1.0.0"}, mapped["scope"])
	assert.Equal(t, map[string]any{"http.method": "GET"}, mapped["attributes"])

	events := mapped["events"].([]map[string]any)
	require.Len(t, events, 1)
	assert.Equal(t, "db.query", events[0]["name"])

	links := mapped["links"].([]map[string]any)
	require.Len(t, links, 1)
	assert.Equal(t, map[string]any{"link.kind": "follows_from"}, links[0]["attributes"])
}
