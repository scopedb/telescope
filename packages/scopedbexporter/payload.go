package scopedbexporter

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
	Env           string         `json:"env"`
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
		Env:           cfg.Env,
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
			"env":            p.Env,
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
		if status, ok := record["status"].(string); ok && status != "" {
			row["status"] = status
		}
		if severityNumber, ok := int64Value(record["severity_number"]); ok {
			row["severity_number"] = severityNumber
		}
		if service := recordService(record); service != "" {
			row["service"] = service
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
		if source := recordSource(record); source != "" {
			row["source"] = source
		}
		projectAttributeString(row, record, "exception_type", "exception.type")
		projectAttributeString(row, record, "exception_message", "exception.message")
	case signalTraces:
		copyRecordField(row, record, "parent_span_id")
		copyRecordFieldAs(row, record, "name", "span_name")
		copyRecordFieldAs(row, record, "kind", "span_kind")
		copyRecordField(row, record, "status_code")
		copyRecordField(row, record, "duration_ns")
		projectAttributeString(row, record, "http_method", "http.request.method", "http.method")
		projectAttributeInt(row, record, "http_status_code", "http.response.status_code", "http.status_code")
		projectAttributeString(row, record, "url_path", "url.path", "http.target")
		projectAttributeString(row, record, "http_route", "http.route")
		projectAttributeString(row, record, "peer_service", "peer.service", "server.address", "net.peer.name")
		projectAttributeString(row, record, "db_system", "db.system.name", "db.system")
		projectAttributeString(row, record, "db_operation", "db.operation.name", "db.operation")
		projectAttributeString(row, record, "rpc_method", "rpc.method")
		projectAttributeString(row, record, "error_type", "error.type")
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

func projectAttributeString(row map[string]any, record Record, targetKey string, sourceKeys ...string) {
	if value := recordAttributeString(record, sourceKeys...); value != "" {
		row[targetKey] = value
	}
}

func projectAttributeInt(row map[string]any, record Record, targetKey string, sourceKeys ...string) {
	if value, ok := recordAttributeInt(record, sourceKeys...); ok {
		row[targetKey] = value
	}
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

func recordService(record Record) string {
	return recordResourceString(record, "service.name")
}

func projectResourceColumns(row map[string]any, record Record) {
	if version := recordResourceString(record, "service.version"); version != "" {
		row["version"] = version
	}
	if instanceID := recordResourceString(record, "service.instance.id"); instanceID != "" {
		row["instance_id"] = instanceID
	}
	if podName := recordResourceString(record, "k8s.pod.name"); podName != "" {
		row["k8s_pod"] = podName
	}
	if namespace := recordResourceString(record, "k8s.namespace.name"); namespace != "" {
		row["k8s_namespace"] = namespace
	}
	if cluster := recordResourceString(record, "k8s.cluster.name"); cluster != "" {
		row["k8s_cluster"] = cluster
	}
	if container := recordResourceString(record, "container.name"); container != "" {
		row["container_name"] = container
	}
	if hostIP := recordResourceFirstString(record, "host.ip"); hostIP != "" {
		row["host_ip"] = hostIP
	}
	if hostName := recordResourceString(record, "host.name"); hostName != "" {
		row["host"] = hostName
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

func recordSource(record Record) string {
	if source := recordAttributeString(record, "source", "event.source", "log.source"); source != "" {
		return source
	}
	if source := recordResourceString(record, "telemetry.sdk.language"); source != "" {
		return source
	}
	if source := recordResourceString(record, "telemetry.sdk.name"); source != "" {
		return source
	}
	return recordScopeString(record, "name")
}

func recordScopeString(record Record, key string) string {
	scope := recordScope(record)
	if scope == nil {
		return ""
	}
	return firstString(scope[key])
}

func recordAttributeString(record Record, keys ...string) string {
	attributes := recordAttributes(record)
	if attributes == nil {
		return ""
	}
	for _, key := range keys {
		if value := firstString(attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

func recordAttributeInt(record Record, keys ...string) (int64, bool) {
	attributes := recordAttributes(record)
	if attributes == nil {
		return 0, false
	}
	for _, key := range keys {
		if value, ok := int64Value(attributes[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func recordAttributes(record Record) map[string]any {
	attributes, ok := record["attributes"].(map[string]any)
	if !ok {
		return nil
	}
	return attributes
}

func recordScope(record Record) map[string]any {
	scope, ok := record["scope"].(map[string]any)
	if !ok {
		return nil
	}
	return scope
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

func int64Value(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v <= math.MaxInt64 {
			return int64(v), true
		}
	case float64:
		if math.Trunc(v) == v && v >= math.MinInt64 && v <= math.MaxInt64 {
			return int64(v), true
		}
	}
	return 0, false
}
