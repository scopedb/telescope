package vendordbexporter

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
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
	ingestID := newIngestID()
	rows := make([]map[string]any, 0, len(p.Records))
	for i, record := range p.Records {
		row := map[string]any{
			"ingest_ts":      ingestTS,
			"signal":         p.Signal,
			"schema_version": p.SchemaVersion,
			"dataset":        p.Dataset,
			"record":         map[string]any(record),
			"row_id":         deriveRowID(ingestID, uint32(i)),
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
		projectResourceColumns(row, record)
		projectSignalColumns(row, p.Signal, record)
		rows = append(rows, row)
	}
	return rows
}

func projectSignalColumns(row map[string]any, signal string, record Record) {
	switch signal {
	case signalLogs:
		copyRecordField(row, record, "message")
	case signalTraces:
		copyRecordField(row, record, "parent_span_id")
		copyRecordFieldAs(row, record, "name", "span_name")
		copyRecordFieldAs(row, record, "kind", "span_kind")
		copyRecordField(row, record, "status_code")
		copyRecordField(row, record, "duration_ns")
	case signalMetrics:
		copyRecordField(row, record, "unit")
		copyRecordField(row, record, "temporality")
		copyRecordFieldAs(row, record, "type", "metric_type")
		copyRecordField(row, record, "number_value")
		copyRecordField(row, record, "distribution")
	}
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

func copyRecordField(row map[string]any, record Record, key string) {
	copyRecordFieldAs(row, record, key, key)
}

func copyRecordFieldAs(row map[string]any, record Record, sourceKey string, targetKey string) {
	value, ok := record[sourceKey]
	if !ok || value == nil {
		return
	}
	if text, ok := value.(string); ok && text == "" {
		return
	}
	row[targetKey] = value
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

func stringifyRaw(v any) string {
	return fmt.Sprint(v)
}

func messageString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := json.Marshal(v)
		if err == nil {
			return string(raw)
		}
		return stringifyRaw(v)
	}
}

func recordServiceName(record Record) string {
	resource := recordResource(record)
	if resource == nil {
		return ""
	}

	serviceName, _ := resource["service.name"].(string)
	return serviceName
}

func projectResourceColumns(row map[string]any, record Record) {
	if instanceID := recordResourceString(record, "service.instance.id"); instanceID != "" {
		row["instance_id"] = instanceID
	}
	if podName := recordResourceString(record, "k8s.pod.name"); podName != "" {
		row["pod_name"] = podName
	}
	if hostIP := recordResourceFirstString(record, "host.ip"); hostIP != "" {
		row["host_ip"] = hostIP
	}
	if hostName := recordResourceString(record, "host.name"); hostName != "" {
		row["host_name"] = hostName
	}
}

func recordResource(record Record) map[string]any {
	resource, ok := record["resource"].(map[string]any)
	if !ok {
		return nil
	}
	return resource
}

func recordResourceString(record Record, key string) string {
	resource := recordResource(record)
	if resource == nil {
		return ""
	}
	return firstString(resource[key])
}

func recordResourceFirstString(record Record, key string) string {
	resource := recordResource(record)
	if resource == nil {
		return ""
	}
	return firstString(resource[key])
}

func newIngestID() uint32 {
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(bytes[:])
}

func deriveRowID(ingestID uint32, rowOrdinal uint32) string {
	var bytes [8]byte
	binary.BigEndian.PutUint32(bytes[:4], ingestID)
	binary.BigEndian.PutUint32(bytes[4:], rowOrdinal)
	return hex.EncodeToString(bytes[:])
}

func firstString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []string:
		for _, item := range v {
			if item != "" {
				return item
			}
		}
	case []any:
		for _, item := range v {
			if text, ok := item.(string); ok && text != "" {
				return text
			}
		}
	}
	return ""
}
