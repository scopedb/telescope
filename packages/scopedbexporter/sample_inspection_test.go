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
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
)

func TestInspectSampleReportsNestedTypesAndCoverage(t *testing.T) {
	request := plogotlp.NewExportRequest()
	resourceLogs := request.Logs().ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr("service.name", "checkout")
	scopeLogs := resourceLogs.ScopeLogs().AppendEmpty()
	scopeLogs.Scope().SetName("checkout-logger")

	first := scopeLogs.LogRecords().AppendEmpty()
	first.Attributes().PutInt("attempt", 1)
	firstBody := first.Body().SetEmptyMap()
	firstRequest := firstBody.PutEmptyMap("request")
	firstRequest.PutInt("id", 42)
	firstBody.PutEmptySlice("items").AppendEmpty().SetStr("first")

	second := scopeLogs.LogRecords().AppendEmpty()
	secondRequest := second.Body().SetEmptyMap().PutEmptyMap("request")
	secondRequest.PutStr("id", "43")

	sample, err := request.MarshalJSON()
	require.NoError(t, err)
	inspection, err := InspectSample(signalLogs, sample)
	require.NoError(t, err)

	assert.Equal(t, SampleInspectionVersion, inspection.Version)
	assert.Equal(t, signalLogs, inspection.Signal)
	assert.Equal(t, 2, inspection.Records)
	assert.Equal(t, SampleFieldInspection{
		Group:         "resource",
		Selector:      `resource.attributes["service.name"]`,
		ObservedTypes: []string{"string"},
		Populated:     2,
		Total:         2,
	}, findSampleInspectionField(t, inspection, `resource.attributes["service.name"]`))
	assert.Equal(t, SampleFieldInspection{
		Group:         "log",
		Selector:      `log.attributes["attempt"]`,
		ObservedTypes: []string{"int"},
		Populated:     1,
		Total:         2,
	}, findSampleInspectionField(t, inspection, `log.attributes["attempt"]`))
	assert.Equal(t, []string{"int", "string"}, findSampleInspectionField(
		t,
		inspection,
		`log.body["request"]["id"]`,
	).ObservedTypes)
	assert.Equal(t, SampleFieldInspection{
		Group:         "log",
		Selector:      `log.body["items"]`,
		ObservedTypes: []string{"array"},
		Populated:     1,
		Total:         2,
	}, findSampleInspectionField(t, inspection, `log.body["items"]`))

	selectors := make([]string, 0, len(inspection.Fields))
	for _, field := range inspection.Fields {
		selectors = append(selectors, field.Selector)
		_, err = compileRecordGetter(signalLogs, field.Selector)
		require.NoError(t, err, field.Selector)
	}
	assert.NotContains(t, selectors, `log.body["items"][0]`)
	assert.NotContains(t, selectors, "log.trace_id")
	assert.NotContains(t, selectors, "resource.schema_url")
	assert.NotContains(t, selectors, "resource.dropped_attributes_count")
}

func TestInspectSampleKeepsMeaningfulZeroValues(t *testing.T) {
	request := pmetricotlp.NewExportRequest()
	metric := request.Metrics().ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName("queue.depth")
	point := metric.SetEmptyGauge().DataPoints().AppendEmpty()
	point.SetIntValue(0)

	sample, err := request.MarshalJSON()
	require.NoError(t, err)
	inspection, err := InspectSample(signalMetrics, sample)
	require.NoError(t, err)

	assert.Equal(t, []string{"int"}, findSampleInspectionField(t, inspection, "datapoint.value").ObservedTypes)
	assert.Equal(t, 1, findSampleInspectionField(t, inspection, "datapoint.value").Populated)
	assert.Equal(t, []string{"float"}, findSampleInspectionField(t, inspection, "datapoint.number_value").ObservedTypes)
}

func TestInspectSampleUsesProductionMappersForEverySignalAndEncoding(t *testing.T) {
	tests := []struct {
		signal   string
		selector string
	}{
		{signal: signalLogs, selector: "log.message"},
		{signal: signalTraces, selector: "span.start_time"},
		{signal: signalMetrics, selector: "datapoint.value"},
	}

	for _, tt := range tests {
		for _, encoding := range []string{"json", "protobuf"} {
			t.Run(tt.signal+"_"+encoding, func(t *testing.T) {
				sample := readGoldenFile(t, tt.signal+".otlp.json")
				if encoding == "protobuf" {
					sample = goldenOTLPProtobuf(t, tt.signal, sample)
				}
				inspection, err := InspectSample(tt.signal, sample)
				require.NoError(t, err)
				assert.Positive(t, inspection.Records)
				assert.NotEmpty(t, findSampleInspectionField(t, inspection, tt.selector).ObservedTypes)
			})
		}
	}
}

func findSampleInspectionField(
	t *testing.T,
	inspection SampleInspection,
	selector string,
) SampleFieldInspection {
	t.Helper()
	for _, field := range inspection.Fields {
		if field.Selector == selector {
			return field
		}
	}
	t.Fatalf("sample inspection field %q not found", selector)
	return SampleFieldInspection{}
}
