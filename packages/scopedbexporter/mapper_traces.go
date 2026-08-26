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
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func mapTraces(traces ptrace.Traces) (*IngestPayload, error) {
	payload := newPayload()

	resourceSpans := traces.ResourceSpans()
	for i := 0; i < resourceSpans.Len(); i++ {
		resourceSpan := resourceSpans.At(i)

		scopeSpans := resourceSpan.ScopeSpans()
		for j := 0; j < scopeSpans.Len(); j++ {
			scopeSpan := scopeSpans.At(j)
			otelCtx := newOTelContext(resourceSpan.Resource(), resourceSpan.SchemaUrl(), scopeSpan.Scope(), scopeSpan.SchemaUrl())

			spans := scopeSpan.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				mapped := Record{
					"trace_id":                 traceIDString(span.TraceID()),
					"span_id":                  spanIDString(span.SpanID()),
					"parent_span_id":           spanIDString(span.ParentSpanID()),
					"trace_state":              span.TraceState().AsRaw(),
					"flags":                    span.Flags(),
					"name":                     span.Name(),
					"kind":                     strings.ToLower(span.Kind().String()),
					"start_time_unix_nano":     timestampString(span.StartTimestamp()),
					"end_time_unix_nano":       timestampString(span.EndTimestamp()),
					"duration_ns":              durationNanos(span.StartTimestamp(), span.EndTimestamp()),
					"status_code":              strings.ToLower(span.Status().Code().String()),
					"status_message":           span.Status().Message(),
					"attributes":               attributesToMap(span.Attributes()),
					"dropped_attributes_count": span.DroppedAttributesCount(),
					"events":                   spanEventsToSlice(span.Events()),
					"dropped_events_count":     span.DroppedEventsCount(),
					"links":                    spanLinksToSlice(span.Links()),
					"dropped_links_count":      span.DroppedLinksCount(),
				}
				otelCtx.addTo(mapped)
				payload.Records = append(payload.Records, mapped)
			}
		}
	}

	return payload, nil
}

func spanEventsToSlice(events ptrace.SpanEventSlice) []map[string]any {
	out := make([]map[string]any, 0, events.Len())
	for i := 0; i < events.Len(); i++ {
		event := events.At(i)
		out = append(out, map[string]any{
			"name":                     event.Name(),
			"timestamp_unix_nano":      timestampString(event.Timestamp()),
			"attributes":               attributesToMap(event.Attributes()),
			"dropped_attributes_count": event.DroppedAttributesCount(),
		})
	}
	return out
}

func durationNanos(start pcommon.Timestamp, end pcommon.Timestamp) any {
	if start == 0 || end == 0 || end < start {
		return nil
	}
	return int64(end - start)
}

func spanLinksToSlice(links ptrace.SpanLinkSlice) []map[string]any {
	out := make([]map[string]any, 0, links.Len())
	for i := 0; i < links.Len(); i++ {
		link := links.At(i)
		out = append(out, map[string]any{
			"trace_id":                 traceIDString(link.TraceID()),
			"span_id":                  spanIDString(link.SpanID()),
			"trace_state":              link.TraceState().AsRaw(),
			"attributes":               attributesToMap(link.Attributes()),
			"dropped_attributes_count": link.DroppedAttributesCount(),
			"flags":                    link.Flags(),
		})
	}
	return out
}
