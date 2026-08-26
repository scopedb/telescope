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
	"encoding/base64"
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
	Records []Record
}

type Record map[string]any

type otelContext struct {
	resource                       map[string]any
	resourceSchemaURL              string
	resourceDroppedAttributesCount uint32
	scope                          map[string]any
	scopeSchemaURL                 string
}

func newPayload() *IngestPayload {
	return &IngestPayload{
		Records: make([]Record, 0),
	}
}

func newOTelContext(resource pcommon.Resource, resourceSchemaURL string, scope pcommon.InstrumentationScope, scopeSchemaURL string) otelContext {
	return otelContext{
		resource:                       attributesToMap(resource.Attributes()),
		resourceSchemaURL:              resourceSchemaURL,
		resourceDroppedAttributesCount: resource.DroppedAttributesCount(),
		scope:                          scopeToMap(scope),
		scopeSchemaURL:                 scopeSchemaURL,
	}
}

func (c otelContext) addTo(record Record) {
	record["resource"] = c.resource
	record["resource_schema_url"] = c.resourceSchemaURL
	record["resource_dropped_attributes_count"] = c.resourceDroppedAttributesCount
	record["scope"] = c.scope
	record["scope_schema_url"] = c.scopeSchemaURL
}

func timestampString(ts pcommon.Timestamp) string {
	if ts == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(ts), 10)
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
		"name":                     scope.Name(),
		"version":                  scope.Version(),
		"dropped_attributes_count": scope.DroppedAttributesCount(),
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
