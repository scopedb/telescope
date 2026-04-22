package vendordbexporter

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const (
	signalLogs    = "logs"
	signalTraces  = "traces"
	signalMetrics = "metrics"
)

type IngestPayload struct {
	SchemaVersion string         `json:"schema_version"`
	Signal        string         `json:"signal"`
	Dataset       string         `json:"dataset"`
	Resource      map[string]any `json:"resource,omitempty"`
	Records       []Record       `json:"records"`
}

type Record map[string]any

type scopeDBIngestRequest struct {
	Type      string            `json:"type"`
	Data      scopeDBIngestData `json:"data"`
	Statement string            `json:"statement"`
}

type scopeDBIngestData struct {
	Format string `json:"format"`
	Rows   string `json:"rows"`
}

func newPayload(cfg *Config, signal string) *IngestPayload {
	return &IngestPayload{
		SchemaVersion: cfg.SchemaVersion,
		Signal:        signal,
		Dataset:       cfg.Dataset,
		Records:       make([]Record, 0),
	}
}

func (p *IngestPayload) scopeDBRows() []map[string]any {
	ingestTS := time.Now().UTC().Format(time.RFC3339Nano)
	rows := make([]map[string]any, 0, len(p.Records))
	for _, record := range p.Records {
		row := map[string]any{
			"ingest_ts":      ingestTS,
			"signal":         p.Signal,
			"schema_version": p.SchemaVersion,
			"dataset":        p.Dataset,
			"record":         map[string]any(record),
		}
		if recordTimestamp := recordTimestamp(record); recordTimestamp != "" {
			row["record_timestamp"] = recordTimestamp
		}
		if observedTimestamp := recordObservedTimestamp(record); observedTimestamp != "" {
			row["observed_timestamp"] = observedTimestamp
		}
		if startTimestamp := recordStartTimestamp(record); startTimestamp != "" {
			row["start_timestamp"] = startTimestamp
		}
		if endTimestamp := recordEndTimestamp(record); endTimestamp != "" {
			row["end_timestamp"] = endTimestamp
		}
		if traceID, ok := record["trace_id"].(string); ok && traceID != "" {
			row["trace_id"] = traceID
		}
		if spanID, ok := record["span_id"].(string); ok && spanID != "" {
			row["span_id"] = spanID
		}
		if parentSpanID, ok := record["parent_span_id"].(string); ok && parentSpanID != "" {
			row["parent_span_id"] = parentSpanID
		}
		if metricName, ok := record["metric_name"].(string); ok && metricName != "" {
			row["metric_name"] = metricName
		}
		if severityText, ok := record["severity_text"].(string); ok && severityText != "" {
			row["severity_text"] = severityText
		}
		if serviceName := recordServiceName(record); serviceName != "" {
			row["service_name"] = serviceName
		}
		rows = append(rows, row)
	}
	return rows
}

func timestampString(ts pcommon.Timestamp) string {
	if ts == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(ts), 10)
}

func recordTimestamp(record Record) string {
	return unixNanoStringToRFC3339(recordString(record, "timestamp_unix_nano"))
}

func recordObservedTimestamp(record Record) string {
	return unixNanoStringToRFC3339(recordString(record, "observed_timestamp_unix_nano"))
}

func recordStartTimestamp(record Record) string {
	switch {
	case recordString(record, "start_time_unix_nano") != "":
		return unixNanoStringToRFC3339(recordString(record, "start_time_unix_nano"))
	default:
		return unixNanoStringToRFC3339(recordString(record, "start_timestamp_unix_nano"))
	}
}

func recordEndTimestamp(record Record) string {
	return unixNanoStringToRFC3339(recordString(record, "end_time_unix_nano"))
}

func recordString(record Record, key string) string {
	value, _ := record[key].(string)
	return value
}

func unixNanoStringToRFC3339(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	nanos, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || nanos > math.MaxInt64 {
		return ""
	}

	return time.Unix(0, int64(nanos)).UTC().Format(time.RFC3339Nano)
}

func traceIDString(id pcommon.TraceID) string {
	if id.IsEmpty() {
		return ""
	}
	return id.String()
}

func spanIDString(id pcommon.SpanID) string {
	if id.IsEmpty() {
		return ""
	}
	return id.String()
}

func attributesToMap(attrs pcommon.Map) map[string]any {
	out := make(map[string]any, attrs.Len())
	attrs.Range(func(k string, v pcommon.Value) bool {
		out[k] = valueToAny(v)
		return true
	})
	return out
}

func scopeToMap(scope pcommon.InstrumentationScope) map[string]any {
	out := map[string]any{
		"name":    scope.Name(),
		"version": scope.Version(),
	}
	if scope.Attributes().Len() > 0 {
		out["attributes"] = attributesToMap(scope.Attributes())
	}
	return out
}

func valueToAny(v pcommon.Value) any {
	switch v.Type() {
	case pcommon.ValueTypeEmpty:
		return nil
	case pcommon.ValueTypeStr:
		return v.Str()
	case pcommon.ValueTypeInt:
		return v.Int()
	case pcommon.ValueTypeDouble:
		return v.Double()
	case pcommon.ValueTypeBool:
		return v.Bool()
	case pcommon.ValueTypeBytes:
		return base64.StdEncoding.EncodeToString(v.Bytes().AsRaw())
	case pcommon.ValueTypeSlice:
		s := v.Slice()
		out := make([]any, 0, s.Len())
		for i := 0; i < s.Len(); i++ {
			out = append(out, valueToAny(s.At(i)))
		}
		return out
	case pcommon.ValueTypeMap:
		return attributesToMap(v.Map())
	default:
		return stringifyRaw(v.AsRaw())
	}
}

func uint64SliceToAny(slice pcommon.UInt64Slice) []uint64 {
	out := make([]uint64, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		out = append(out, slice.At(i))
	}
	return out
}

func float64SliceToAny(slice pcommon.Float64Slice) []float64 {
	out := make([]float64, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		out = append(out, slice.At(i))
	}
	return out
}

func metricTemporalityString(v pmetric.AggregationTemporality) string {
	switch v {
	case pmetric.AggregationTemporalityDelta:
		return "delta"
	case pmetric.AggregationTemporalityCumulative:
		return "cumulative"
	default:
		return "unspecified"
	}
}

func stringifyRaw(v any) string {
	return fmt.Sprint(v)
}

func recordServiceName(record Record) string {
	resource, ok := record["resource"].(map[string]any)
	if !ok {
		return ""
	}

	serviceName, _ := resource["service.name"].(string)
	return serviceName
}
