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

import "go.opentelemetry.io/collector/pdata/plog"

func mapLogs(logs plog.Logs) (*IngestPayload, error) {
	payload := newPayload()

	resourceLogs := logs.ResourceLogs()
	for i := 0; i < resourceLogs.Len(); i++ {
		resourceLog := resourceLogs.At(i)

		scopeLogs := resourceLog.ScopeLogs()
		for j := 0; j < scopeLogs.Len(); j++ {
			scopeLog := scopeLogs.At(j)
			otelCtx := newOTelContext(resourceLog.Resource(), resourceLog.SchemaUrl(), scopeLog.Scope(), scopeLog.SchemaUrl())

			logRecords := scopeLog.LogRecords()
			for k := 0; k < logRecords.Len(); k++ {
				record := logRecords.At(k)
				body := valueToAny(record.Body())
				mapped := Record{
					"timestamp_unix_nano":          timestampString(record.Timestamp()),
					"observed_timestamp_unix_nano": timestampString(record.ObservedTimestamp()),
					"trace_id":                     traceIDString(record.TraceID()),
					"span_id":                      spanIDString(record.SpanID()),
					"event_name":                   record.EventName(),
					"status":                       record.SeverityText(),
					"severity_number":              int(record.SeverityNumber()),
					"flags":                        uint32(record.Flags()),
					"dropped_attributes_count":     record.DroppedAttributesCount(),
					"body":                         body,
					"message":                      messageString(body),
					"attributes":                   attributesToMap(record.Attributes()),
				}
				otelCtx.addTo(mapped)
				payload.Records = append(payload.Records, mapped)
			}
		}
	}

	return payload, nil
}
