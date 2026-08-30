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

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestMapLogs(t *testing.T) {
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr("service.name", "checkout")
	resourceLogs.Resource().Attributes().PutStr("service.instance.id", "checkout-1")
	resourceLogs.Resource().Attributes().PutStr("k8s.pod.name", "checkout-pod")
	resourceLogs.Resource().Attributes().PutStr("host.name", "checkout-node")
	resourceLogs.Resource().Attributes().PutEmptySlice("host.ip").AppendEmpty().SetStr("10.0.0.10")

	scopeLogs := resourceLogs.ScopeLogs().AppendEmpty()
	scopeLogs.Scope().SetName("test-scope")
	scopeLogs.Scope().SetVersion("1.2.3")

	record := scopeLogs.LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.Timestamp(123))
	record.SetObservedTimestamp(pcommon.Timestamp(456))
	record.SetTraceID(pcommon.TraceID([16]byte{1, 2, 3}))
	record.SetSpanID(pcommon.SpanID([8]byte{4, 5, 6}))
	record.SetSeverityText("INFO")
	record.SetSeverityNumber(plog.SeverityNumberInfo)
	record.Body().SetStr("hello world")
	record.Attributes().PutStr("env", "dev")

	payload, err := mapLogs(logs)
	require.NoError(t, err)
	require.Len(t, payload.Records, 1)

	mapped := payload.Records[0]
	assert.Equal(t, "123", mapped["timestamp_unix_nano"])
	assert.Equal(t, "456", mapped["observed_timestamp_unix_nano"])
	assert.Equal(t, "01020300000000000000000000000000", mapped["trace_id"])
	assert.Equal(t, "0405060000000000", mapped["span_id"])
	assert.Equal(t, "INFO", mapped["status"])
	assert.Equal(t, int(plog.SeverityNumberInfo), mapped["severity_number"])
	assert.Equal(t, "hello world", mapped["body"])
	assert.Equal(t, "hello world", mapped["message"])
	assert.Equal(t, map[string]any{
		"service.name":        "checkout",
		"service.instance.id": "checkout-1",
		"k8s.pod.name":        "checkout-pod",
		"host.name":           "checkout-node",
		"host.ip":             []any{"10.0.0.10"},
	}, mapped["resource"])
	assert.Equal(t, map[string]any{
		"name":                     "test-scope",
		"version":                  "1.2.3",
		"dropped_attributes_count": uint32(0),
	}, mapped["scope"])
	assert.Equal(t, map[string]any{"env": "dev"}, mapped["attributes"])
}
