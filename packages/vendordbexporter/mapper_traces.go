package vendordbexporter

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func mapTraces(cfg *Config, traces ptrace.Traces) (*IngestPayload, error) {
	payload := newPayload(cfg, signalTraces)

	resourceSpans := traces.ResourceSpans()
	for i := 0; i < resourceSpans.Len(); i++ {
		resourceSpan := resourceSpans.At(i)
		resource := attributesToMap(resourceSpan.Resource().Attributes())

		scopeSpans := resourceSpan.ScopeSpans()
		for j := 0; j < scopeSpans.Len(); j++ {
			scopeSpan := scopeSpans.At(j)
			scope := scopeToMap(scopeSpan.Scope())

			spans := scopeSpan.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				payload.Records = append(payload.Records, Record{
					"trace_id":             traceIDString(span.TraceID()),
					"span_id":              spanIDString(span.SpanID()),
					"parent_span_id":       spanIDString(span.ParentSpanID()),
					"name":                 span.Name(),
					"kind":                 strings.ToLower(span.Kind().String()),
					"start_time_unix_nano": timestampString(span.StartTimestamp()),
					"end_time_unix_nano":   timestampString(span.EndTimestamp()),
					"duration_ns":          durationNanos(span.StartTimestamp(), span.EndTimestamp()),
					"status_code":          strings.ToLower(span.Status().Code().String()),
					"status_message":       span.Status().Message(),
					"resource":             resource,
					"scope":                scope,
					"attributes":           attributesToMap(span.Attributes()),
					"events":               spanEventsToSlice(span.Events()),
					"links":                spanLinksToSlice(span.Links()),
				})
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
