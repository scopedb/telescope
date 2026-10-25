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

	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestMapMetricsPreservesLargeIntValue(t *testing.T) {
	metrics := pmetric.NewMetrics()
	metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName("large.counter")
	point := metric.SetEmptySum().DataPoints().AppendEmpty()
	const value int64 = 9_007_199_254_740_993
	point.SetIntValue(value)

	payload, err := mapMetrics(metrics)
	require.NoError(t, err)
	require.Len(t, payload.Records, 1)
	assert.Equal(t, value, payload.Records[0]["int_value"])
	assert.NotEqual(t, value, int64(payload.Records[0]["number_value"].(float64)))
}

func TestMapMetricsRejectsEmptyMetricType(t *testing.T) {
	metrics := pmetric.NewMetrics()
	metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName("empty.metric")

	payload, err := mapMetrics(metrics)

	require.Nil(t, payload)
	require.EqualError(t, err, "map metric at resource 0 scope 0 metric 0: metric \"empty.metric\" has unsupported type Empty")
	reason, ok := mappingErrorReason(err)
	require.True(t, ok)
	require.Equal(t, mappingReasonUnsupportedMetricType, reason)
}
