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

import "testing"

var benchmarkMappingRow map[string]any

func BenchmarkMappingPlanProject(b *testing.B) {
	record := Record{
		"trace_id": "0102030405060708090a0b0c0d0e0f10",
		"message":  "order accepted",
		"status":   "INFO",
		"body": map[string]any{
			"attempt": "2",
			"message": "order accepted",
			"request": map[string]any{"id": "request-42"},
		},
		"attributes": map[string]any{
			"message": "order accepted",
			"tenant":  "store-7",
		},
		"resource": map[string]any{
			"service.name": "checkout",
		},
	}

	benchmarks := []struct {
		name    string
		mapping MappingConfig
	}{
		{
			name: "shorthand_6_columns",
			mapping: shorthandMapping(map[string]string{
				"body":     "log.body",
				"message":  "log.message",
				"service":  `resource.attributes["service.name"]`,
				"severity": "log.severity_text",
				"tenant":   `log.attributes["tenant"]`,
				"trace_id": "log.trace_id",
			}),
		},
		{
			name: "fallback_cast_6_columns",
			mapping: MappingConfig{
				"attempt": {
					Source: `log.body["attempt"]`,
					Cast:   "int",
				},
				"environment": {
					Source:  `resource.attributes["deployment.environment.name"]`,
					Default: "unknown",
				},
				"message": {
					Sources: []string{`log.attributes["missing"]`, `log.body["message"]`},
					Cast:    "string",
				},
				"origin": {
					Value: "otel",
				},
				"request_id": {
					Source: `log.body["request"]["id"]`,
				},
				"service": {
					Sources: []string{`resource.attributes["service.name"]`, `resource.attributes["service"]`},
				},
			},
		},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			plan, err := compileMappingPlan(signalLogs, "logs", benchmark.mapping)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				row, err := plan.project(record)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkMappingRow = row
			}
		})
	}
}
