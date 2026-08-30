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
	plan, err := compileMappingPlan(signalTraces, "analytics.spans", shorthandMapping(map[string]string{
		"ts":       "span.start_time",
		"trace_id": "span.trace_id",
		"service":  `resource.attributes["service.name"]`,
		"order_id": `span.attributes["order.id"]`,
	}))
	require.NoError(t, err)

	row, err := plan.project(Record{
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
	require.NoError(t, err)

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
	plan, err := compileMappingPlan(signalLogs, "logs", shorthandMapping(map[string]string{
		"attributes":          "log.attributes",
		"resource_attributes": "resource.attributes",
	}))
	require.NoError(t, err)

	row, err := plan.project(Record{
		"attributes": map[string]any{"order.id": "42"},
		"resource":   map[string]any{"service.name": "checkout"},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"order.id": "42"}, row["attributes"])
	assert.Equal(t, map[string]any{"service.name": "checkout"}, row["resource_attributes"])
}

func TestMappingPlanOmitsMissingSources(t *testing.T) {
	plan, err := compileMappingPlan(signalMetrics, "metrics", shorthandMapping(map[string]string{
		"value":   "datapoint.number_value",
		"missing": `datapoint.attributes["missing"]`,
	}))
	require.NoError(t, err)

	row, err := plan.project(Record{"number_value": 0.0})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"value": 0.0}, row)
}

func TestMappingPlanKeepsMetricNumberTypesSeparate(t *testing.T) {
	plan, err := compileMappingPlan(signalMetrics, "metrics", shorthandMapping(map[string]string{
		"int_value":    "datapoint.int_value",
		"double_value": "datapoint.double_value",
	}))
	require.NoError(t, err)

	row, err := plan.project(Record{
		"int_value": int64(9_007_199_254_740_993),
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"int_value": int64(9_007_199_254_740_993)}, row)
	row, err = plan.project(Record{
		"double_value": 0.75,
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"double_value": 0.75}, row)
}

func TestMappingPlanPreservesSelectedEmptyString(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", shorthandMapping(map[string]string{
		"tenant": `resource.attributes["tenant"]`,
	}))
	require.NoError(t, err)

	row, err := plan.project(Record{
		"resource": map[string]any{"tenant": ""},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"tenant": ""}, row)
}

func TestMappingPlanProjectsNestedFallbackCastDefaultAndValue(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", MappingConfig{
		"attempt": {
			Source: `log.body["attempt"]`,
			Cast:   "int",
		},
		"environment": {
			Source:  `resource.attributes["deployment.environment.name"]`,
			Default: "unknown",
		},
		"first_tag": {
			Source: `log.body["tags"][0]`,
		},
		"origin": {
			Value: "otel",
		},
		"sampled": {
			Value: false,
		},
		"shard": {
			Value: "42",
			Cast:  "int",
		},
		"request_id": {
			Source: `log.body["request"]["id"]`,
		},
		"service": {
			Sources: []string{
				`resource.attributes["service.name"]`,
				`resource.attributes["service"]`,
			},
		},
	})
	require.NoError(t, err)

	row, err := plan.project(Record{
		"body": map[string]any{
			"attempt": "2",
			"request": map[string]any{"id": "request-42"},
			"tags":    []any{"checkout", "production"},
		},
		"resource": map[string]any{"service": "checkout"},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"attempt":     int64(2),
		"environment": "unknown",
		"first_tag":   "checkout",
		"origin":      "otel",
		"request_id":  "request-42",
		"sampled":     false,
		"service":     "checkout",
		"shard":       int64(42),
	}, row)
}

func TestMappingPlanTreatsNestedPathMismatchAsAbsent(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", MappingConfig{
		"request_id": {
			Sources: []string{
				`log.body["request"]["id"]`,
				`log.attributes["request.id"]`,
			},
		},
	})
	require.NoError(t, err)

	row, err := plan.project(Record{
		"body":       map[string]any{"request": "not-an-object"},
		"attributes": map[string]any{"request.id": "request-42"},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"request_id": "request-42"}, row)
}

func TestMappingPlanAllowsWhitespaceInNestedSelectors(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", MappingConfig{
		"request_id": {Source: `log.body[ "request" ][ "id" ]`},
	})
	require.NoError(t, err)

	row, err := plan.project(Record{
		"body": map[string]any{
			"request": map[string]any{"id": "request-42"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"request_id": "request-42"}, row)
}

func TestMappingPlanCoalesceUsesFirstPresentValue(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", MappingConfig{
		"tenant": {
			Sources: []string{
				`resource.attributes["tenant.primary"]`,
				`resource.attributes["tenant.fallback"]`,
			},
			Default: "default",
		},
	})
	require.NoError(t, err)

	row, err := plan.project(Record{"resource": map[string]any{
		"tenant.primary":  "",
		"tenant.fallback": "fallback",
	}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"tenant": ""}, row)
}

func TestMappingPlanReportsCastFailure(t *testing.T) {
	plan, err := compileMappingPlan(signalLogs, "logs", MappingConfig{
		"attempt": {Source: `log.attributes["attempt"]`, Cast: "int"},
	})
	require.NoError(t, err)

	_, err = plan.project(Record{"attributes": map[string]any{"attempt": "second"}})
	require.Error(t, err)
	assert.ErrorContains(t, err, `column "attempt"`)
	reason, ok := mappingErrorReason(err)
	assert.True(t, ok)
	assert.Equal(t, mappingReasonCastFailed, reason)
}

func TestMappingPlanRejectsInvalidNestedSelectors(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{source: `log.message["key"]`, want: "produces string and cannot be indexed"},
		{source: `log.attributes[0]`, want: "produces an object and requires a string key"},
		{source: `log.body[-1]`, want: "non-negative index"},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			_, err := compileMappingPlan(signalLogs, "logs", MappingConfig{
				"value": {Source: tt.source},
			})
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestSelectorTypeCompatibility(t *testing.T) {
	assert.Equal(t, MappingCompatible, selectorTypeFor("log.timestamp").compatibilityWith(scopedb.TimestampDataType))
	assert.Equal(t, MappingCompatible, selectorTypeFor("log.timestamp").compatibilityWith(scopedb.StringDataType))
	assert.Equal(t, MappingIncompatible, selectorTypeFor("log.severity_number").compatibilityWith(scopedb.StringDataType))
	assert.Equal(t, MappingRuntimeDependent, selectorTypeFor(`resource.attributes["tenant.id"]`).compatibilityWith(scopedb.StringDataType))
	assert.Equal(t, MappingCompatible, selectorTypeFor("span.events").compatibilityWith(scopedb.ObjectDataType))
	assert.Equal(t, MappingRuntimeDependent, selectorTypeFor(`resource.attributes["tenant.id"]`).compatibilityWith(scopedb.StringDataType))
	assert.Equal(t, MappingRuntimeDependent, selectorTypeFor("datapoint.value").compatibilityWith(scopedb.IntDataType))
	assert.Equal(t, MappingIncompatible, selectorTypeFor("datapoint.value").compatibilityWith(scopedb.StringDataType))
	assert.Equal(t, MappingCompatible, selectorTypeFor("log.body").compatibilityWith(scopedb.AnyDataType))
}

func TestMappingPlanReportsAllSelectorErrorsWithSuggestion(t *testing.T) {
	_, err := compileMappingPlan(signalTraces, "analytics.spans", shorthandMapping(map[string]string{
		"bad-name": "span.start_tim",
		"service":  `resource.attribute["service.name"]`,
		"status":   "span.status.mesage",
	}))
	require.Error(t, err)

	assert.ErrorContains(t, err, `destination column "bad-name" must be an unquoted ScopeDB identifier`)
	assert.ErrorContains(t, err, `column "bad-name": unsupported traces source "span.start_tim"; did you mean "span.start_time"?`)
	assert.ErrorContains(t, err, `column "service": unsupported traces source "resource.attribute[\"service.name\"]"; did you mean "resource.attributes[\"service.name\"]"?`)
	assert.ErrorContains(t, err, `column "status": unsupported traces source "span.status.mesage"; did you mean "span.status.message"?`)
}
