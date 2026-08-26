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

	scopedb "github.com/scopedb/goscopedb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMappingPlanProjectsOnlySelectedColumns(t *testing.T) {
	plan, err := compileMappingPlan(signalTraces, "analytics.spans", map[string]string{
		"ts":       "span.start_time",
		"trace_id": "span.trace_id",
		"service":  `resource.attributes["service.name"]`,
		"order_id": `span.attributes["order.id"]`,
	})
	require.NoError(t, err)

	row := plan.project(Record{
		"trace_id":             "010203",
		"span_id":              "not-selected",
		"start_time_unix_nano": "1713835425123456789",
		"resource": map[string]any{
			"service.name":    "checkout",
			"service.version": "not-selected",
		},
		"attributes": map[string]any{
			"order.id": "order-42",
			"user.id":  "not-selected",
		},
	})

	assert.Equal(t, map[string]any{
		"ts":       "2024-04-23T01:23:45.123456789Z",
		"trace_id": "010203",
		"service":  "checkout",
		"order_id": "order-42",
	}, row)
	assert.NotContains(t, row, "span_id")
	assert.NotContains(t, row, "attributes")
}

func TestMappingPlanCanExplicitlyKeepAttributeObjects(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", map[string]string{
		"attributes":          "log.attributes",
		"resource_attributes": "resource.attributes",
	})
	require.NoError(t, err)

	row := plan.project(Record{
		"attributes": map[string]any{"order.id": "42"},
		"resource":   map[string]any{"service.name": "checkout"},
	})

	assert.Equal(t, map[string]any{"order.id": "42"}, row["attributes"])
	assert.Equal(t, map[string]any{"service.name": "checkout"}, row["resource_attributes"])
}

func TestMappingPlanOmitsMissingSources(t *testing.T) {
	plan, err := compileMappingPlan(signalMetrics, "metrics", map[string]string{
		"value":   "datapoint.number_value",
		"missing": `datapoint.attributes["missing"]`,
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"value": 0.0}, plan.project(Record{"number_value": 0.0}))
}

func TestMappingPlanKeepsMetricNumberTypesSeparate(t *testing.T) {
	plan, err := compileMappingPlan(signalMetrics, "metrics", map[string]string{
		"int_value":    "datapoint.int_value",
		"double_value": "datapoint.double_value",
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"int_value": int64(9_007_199_254_740_993)}, plan.project(Record{
		"int_value": int64(9_007_199_254_740_993),
	}))
	assert.Equal(t, map[string]any{"double_value": 0.75}, plan.project(Record{
		"double_value": 0.75,
	}))
}

func TestMappingPlanPreservesSelectedEmptyString(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", map[string]string{
		"tenant": `resource.attributes["tenant"]`,
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"tenant": ""}, plan.project(Record{
		"resource": map[string]any{"tenant": ""},
	}))
}

func TestSelectorTypeCompatibility(t *testing.T) {
	assert.True(t, selectorTypeFor("log.timestamp").compatibleWith(scopedb.TimestampDataType))
	assert.True(t, selectorTypeFor("log.timestamp").compatibleWith(scopedb.StringDataType))
	assert.False(t, selectorTypeFor("log.severity_number").compatibleWith(scopedb.StringDataType))
	assert.True(t, selectorTypeFor(`resource.attributes["tenant.id"]`).compatibleWith(scopedb.StringDataType))
	assert.True(t, selectorTypeFor("span.events").compatibleWith(scopedb.ObjectDataType))
}
