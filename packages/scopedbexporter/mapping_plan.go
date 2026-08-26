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
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	scopedb "github.com/scopedb/goscopedb"
)

var attributeSourcePattern = regexp.MustCompile(`^(resource|scope|log|span|datapoint)\.attributes\["([^"]+)"\]$`)
var metadataSourcePattern = regexp.MustCompile(`^metric\.metadata\["([^"]+)"\]$`)

var commonSourceSelectors = []string{
	"resource.attributes",
	"resource.schema_url",
	"resource.dropped_attributes_count",
	"scope.name",
	"scope.version",
	"scope.attributes",
	"scope.schema_url",
	"scope.dropped_attributes_count",
}

var signalSourceSelectors = map[string][]string{
	signalLogs: {
		"log.timestamp",
		"log.observed_timestamp",
		"log.timestamp_unix_nano",
		"log.observed_timestamp_unix_nano",
		"log.trace_id",
		"log.span_id",
		"log.event_name",
		"log.severity_text",
		"log.severity_number",
		"log.flags",
		"log.dropped_attributes_count",
		"log.body",
		"log.message",
		"log.attributes",
	},
	signalTraces: {
		"span.trace_id",
		"span.span_id",
		"span.parent_span_id",
		"span.trace_state",
		"span.flags",
		"span.name",
		"span.kind",
		"span.start_time",
		"span.end_time",
		"span.start_time_unix_nano",
		"span.end_time_unix_nano",
		"span.duration_ns",
		"span.status.code",
		"span.status.message",
		"span.attributes",
		"span.dropped_attributes_count",
		"span.events",
		"span.dropped_events_count",
		"span.links",
		"span.dropped_links_count",
	},
	signalMetrics: {
		"metric.name",
		"metric.description",
		"metric.unit",
		"metric.type",
		"metric.metadata",
		"metric.temporality",
		"metric.is_monotonic",
		"datapoint.timestamp",
		"datapoint.start_time",
		"datapoint.timestamp_unix_nano",
		"datapoint.start_time_unix_nano",
		"datapoint.flags",
		"datapoint.attributes",
		"datapoint.value",
		"datapoint.value_type",
		"datapoint.int_value",
		"datapoint.double_value",
		"datapoint.number_value",
		"datapoint.distribution",
		"datapoint.exemplars",
	},
}

type recordGetter func(Record) (any, bool)

type mappedColumn struct {
	name       string
	source     string
	sourceType selectorType
	get        recordGetter
}

type selectorType string

const (
	selectorTypeDynamic   selectorType = "dynamic"
	selectorTypeString    selectorType = "string"
	selectorTypeTimestamp selectorType = "timestamp"
	selectorTypeInt       selectorType = "int"
	selectorTypeUInt      selectorType = "uint"
	selectorTypeFloat     selectorType = "float"
	selectorTypeBoolean   selectorType = "boolean"
	selectorTypeObject    selectorType = "object"
	selectorTypeArray     selectorType = "array"
	selectorTypeNumber    selectorType = "int or float"
)

type mappingPlan struct {
	signal  string
	table   tableRef
	columns []mappedColumn
}

func (t selectorType) runtimeDependent() bool {
	return t == selectorTypeDynamic || t == selectorTypeNumber
}

func compileMappingPlan(signal string, table string, columns map[string]string) (*mappingPlan, error) {
	var errs []error
	ref, err := parseTableRef(table)
	if err != nil {
		errs = append(errs, err)
	}
	if len(columns) == 0 {
		errs = append(errs, fmt.Errorf("at least one destination column is required"))
	}

	names := make([]string, 0, len(columns))
	for name := range columns {
		names = append(names, name)
	}
	sort.Strings(names)

	plan := &mappingPlan{signal: signal, table: ref, columns: make([]mappedColumn, 0, len(names))}
	for _, name := range names {
		valid := true
		if !tablePartPattern.MatchString(name) {
			errs = append(errs, fmt.Errorf("destination column %q must be an unquoted ScopeDB identifier", name))
			valid = false
		}
		source := strings.TrimSpace(columns[name])
		getter, err := compileRecordGetter(signal, source)
		if err != nil {
			errs = append(errs, fmt.Errorf("column %q: %w", name, err))
			valid = false
		}
		if !valid {
			continue
		}
		plan.columns = append(plan.columns, mappedColumn{
			name:       name,
			source:     source,
			sourceType: selectorTypeFor(source),
			get:        getter,
		})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return plan, nil
}

func (p *mappingPlan) project(record Record) map[string]any {
	row := make(map[string]any, len(p.columns))
	for _, column := range p.columns {
		if value, ok := column.get(record); ok {
			row[column.name] = value
		}
	}
	return row
}

func selectorTypeFor(source string) selectorType {
	if attributeSourcePattern.MatchString(source) || metadataSourcePattern.MatchString(source) {
		return selectorTypeDynamic
	}

	switch source {
	case "resource.attributes", "scope.attributes", "log.attributes", "span.attributes",
		"metric.metadata", "datapoint.attributes", "datapoint.distribution":
		return selectorTypeObject
	case "span.events", "span.links", "datapoint.exemplars":
		return selectorTypeArray
	case "log.timestamp", "log.observed_timestamp", "span.start_time", "span.end_time",
		"datapoint.timestamp", "datapoint.start_time":
		return selectorTypeTimestamp
	case "log.severity_number", "span.duration_ns", "datapoint.int_value":
		return selectorTypeInt
	case "resource.dropped_attributes_count", "scope.dropped_attributes_count",
		"log.flags", "log.dropped_attributes_count", "span.flags", "span.dropped_attributes_count",
		"span.dropped_events_count", "span.dropped_links_count", "datapoint.flags":
		return selectorTypeUInt
	case "datapoint.double_value", "datapoint.number_value":
		return selectorTypeFloat
	case "metric.is_monotonic":
		return selectorTypeBoolean
	case "datapoint.value":
		return selectorTypeNumber
	case "log.body":
		return selectorTypeDynamic
	case "resource.schema_url", "scope.name", "scope.version", "scope.schema_url",
		"log.timestamp_unix_nano", "log.observed_timestamp_unix_nano", "log.trace_id", "log.span_id",
		"log.event_name", "log.severity_text", "log.message", "span.trace_id", "span.span_id",
		"span.parent_span_id", "span.trace_state", "span.name", "span.kind", "span.start_time_unix_nano",
		"span.end_time_unix_nano", "span.status.code", "span.status.message", "metric.name",
		"metric.description", "metric.unit", "metric.type", "metric.temporality", "datapoint.timestamp_unix_nano",
		"datapoint.start_time_unix_nano", "datapoint.value_type":
		return selectorTypeString
	default:
		return selectorTypeDynamic
	}
}

func (t selectorType) compatibleWith(actual scopedb.DataType) bool {
	return t.compatibilityWith(actual) != MappingIncompatible
}

func (t selectorType) compatibilityWith(actual scopedb.DataType) MappingCompatibility {
	if actual == scopedb.AnyDataType {
		return MappingCompatible
	}
	if t == selectorTypeDynamic {
		return MappingRuntimeDependent
	}
	if t == selectorTypeNumber {
		if actual == scopedb.IntDataType || actual == scopedb.FloatDataType {
			return MappingRuntimeDependent
		}
		return MappingIncompatible
	}

	compatible := false
	switch t {
	case selectorTypeString:
		compatible = actual == scopedb.StringDataType
	case selectorTypeTimestamp:
		compatible = actual == scopedb.TimestampDataType || actual == scopedb.StringDataType
	case selectorTypeInt:
		compatible = actual == scopedb.IntDataType
	case selectorTypeUInt:
		compatible = actual == scopedb.UIntDataType || actual == scopedb.IntDataType
	case selectorTypeFloat:
		compatible = actual == scopedb.FloatDataType
	case selectorTypeBoolean:
		compatible = actual == scopedb.BooleanDataType
	case selectorTypeObject:
		compatible = actual == scopedb.ObjectDataType
	case selectorTypeArray:
		compatible = actual == scopedb.ArrayDataType || actual == scopedb.ObjectDataType
	}
	if compatible {
		return MappingCompatible
	}
	return MappingIncompatible
}

func compileRecordGetter(signal string, source string) (recordGetter, error) {
	if source == "" {
		return nil, fmt.Errorf("source is required")
	}
	if getter := commonRecordGetter(source); getter != nil {
		return getter, nil
	}
	if matches := attributeSourcePattern.FindStringSubmatch(source); matches != nil {
		return attributeGetter(signal, matches[1], matches[2])
	}
	if matches := metadataSourcePattern.FindStringSubmatch(source); matches != nil {
		if signal != signalMetrics {
			return nil, fmt.Errorf("source %q is only valid for metrics", source)
		}
		return nestedMapKeyGetter([]string{"metadata"}, matches[1]), nil
	}

	var getter recordGetter
	switch signal {
	case signalLogs:
		getter = logRecordGetter(source)
	case signalTraces:
		getter = traceRecordGetter(source)
	case signalMetrics:
		getter = metricRecordGetter(source)
	default:
		return nil, fmt.Errorf("unsupported signal %q", signal)
	}
	if getter == nil {
		if suggestion := suggestSource(signal, source); suggestion != "" {
			return nil, fmt.Errorf("unsupported %s source %q; did you mean %q?", signal, source, suggestion)
		}
		return nil, fmt.Errorf("unsupported %s source %q", signal, source)
	}
	return getter, nil
}

func suggestSource(signal string, source string) string {
	if source == "" {
		return ""
	}
	comparedSource := source
	suffix := ""
	if bracket := strings.IndexByte(source, '['); bracket > 0 {
		comparedSource = source[:bracket]
		suffix = source[bracket:]
	}
	candidates := make([]string, 0, len(commonSourceSelectors)+len(signalSourceSelectors[signal]))
	candidates = append(candidates, commonSourceSelectors...)
	candidates = append(candidates, signalSourceSelectors[signal]...)
	best := ""
	bestDistance := len(comparedSource) + 1
	for _, candidate := range candidates {
		distance := editDistance(comparedSource, candidate)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	maxDistance := max(2, len(comparedSource)/4)
	if bestDistance > maxDistance {
		return ""
	}
	suggestion := best + suffix
	if suggestion == source {
		return ""
	}
	return suggestion
}

func editDistance(left string, right string) int {
	previous := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex := 1; leftIndex <= len(left); leftIndex++ {
		current := make([]int, len(right)+1)
		current[0] = leftIndex
		for rightIndex := 1; rightIndex <= len(right); rightIndex++ {
			cost := 1
			if left[leftIndex-1] == right[rightIndex-1] {
				cost = 0
			}
			current[rightIndex] = min(
				current[rightIndex-1]+1,
				previous[rightIndex]+1,
				previous[rightIndex-1]+cost,
			)
		}
		previous = current
	}
	return previous[len(right)]
}

func commonRecordGetter(source string) recordGetter {
	switch source {
	case "resource.attributes":
		return pathGetter("resource")
	case "resource.schema_url":
		return pathGetter("resource_schema_url")
	case "resource.dropped_attributes_count":
		return pathGetter("resource_dropped_attributes_count")
	case "scope.name":
		return nestedPathGetter("scope", "name")
	case "scope.version":
		return nestedPathGetter("scope", "version")
	case "scope.attributes":
		return nestedPathGetter("scope", "attributes")
	case "scope.schema_url":
		return pathGetter("scope_schema_url")
	case "scope.dropped_attributes_count":
		return nestedPathGetter("scope", "dropped_attributes_count")
	default:
		return nil
	}
}

func attributeGetter(signal string, context string, key string) (recordGetter, error) {
	switch context {
	case "resource":
		return nestedMapKeyGetter([]string{"resource"}, key), nil
	case "scope":
		return nestedMapKeyGetter([]string{"scope", "attributes"}, key), nil
	case "log":
		if signal != signalLogs {
			return nil, fmt.Errorf("log attributes are only valid for logs")
		}
	case "span":
		if signal != signalTraces {
			return nil, fmt.Errorf("span attributes are only valid for traces")
		}
	case "datapoint":
		if signal != signalMetrics {
			return nil, fmt.Errorf("datapoint attributes are only valid for metrics")
		}
	}
	return nestedMapKeyGetter([]string{"attributes"}, key), nil
}

func logRecordGetter(source string) recordGetter {
	switch source {
	case "log.timestamp":
		return timestampGetter("timestamp_unix_nano")
	case "log.observed_timestamp":
		return timestampGetter("observed_timestamp_unix_nano")
	case "log.timestamp_unix_nano":
		return pathGetter("timestamp_unix_nano")
	case "log.observed_timestamp_unix_nano":
		return pathGetter("observed_timestamp_unix_nano")
	case "log.trace_id":
		return pathGetter("trace_id")
	case "log.span_id":
		return pathGetter("span_id")
	case "log.event_name":
		return pathGetter("event_name")
	case "log.severity_text":
		return pathGetter("status")
	case "log.severity_number":
		return pathGetter("severity_number")
	case "log.flags":
		return pathGetter("flags")
	case "log.dropped_attributes_count":
		return pathGetter("dropped_attributes_count")
	case "log.body":
		return pathGetter("body")
	case "log.message":
		return pathGetter("message")
	case "log.attributes":
		return pathGetter("attributes")
	default:
		return nil
	}
}

func traceRecordGetter(source string) recordGetter {
	switch source {
	case "span.trace_id":
		return pathGetter("trace_id")
	case "span.span_id":
		return pathGetter("span_id")
	case "span.parent_span_id":
		return pathGetter("parent_span_id")
	case "span.trace_state":
		return pathGetter("trace_state")
	case "span.flags":
		return pathGetter("flags")
	case "span.name":
		return pathGetter("name")
	case "span.kind":
		return pathGetter("kind")
	case "span.start_time":
		return timestampGetter("start_time_unix_nano")
	case "span.end_time":
		return timestampGetter("end_time_unix_nano")
	case "span.start_time_unix_nano":
		return pathGetter("start_time_unix_nano")
	case "span.end_time_unix_nano":
		return pathGetter("end_time_unix_nano")
	case "span.duration_ns":
		return pathGetter("duration_ns")
	case "span.status.code":
		return pathGetter("status_code")
	case "span.status.message":
		return pathGetter("status_message")
	case "span.attributes":
		return pathGetter("attributes")
	case "span.dropped_attributes_count":
		return pathGetter("dropped_attributes_count")
	case "span.events":
		return pathGetter("events")
	case "span.dropped_events_count":
		return pathGetter("dropped_events_count")
	case "span.links":
		return pathGetter("links")
	case "span.dropped_links_count":
		return pathGetter("dropped_links_count")
	default:
		return nil
	}
}

func metricRecordGetter(source string) recordGetter {
	switch source {
	case "metric.name":
		return pathGetter("metric_name")
	case "metric.description":
		return pathGetter("description")
	case "metric.unit":
		return pathGetter("unit")
	case "metric.type":
		return pathGetter("type")
	case "metric.metadata":
		return pathGetter("metadata")
	case "metric.temporality":
		return pathGetter("temporality")
	case "metric.is_monotonic":
		return pathGetter("is_monotonic")
	case "datapoint.timestamp":
		return timestampGetter("timestamp_unix_nano")
	case "datapoint.start_time":
		return timestampGetter("start_timestamp_unix_nano")
	case "datapoint.timestamp_unix_nano":
		return pathGetter("timestamp_unix_nano")
	case "datapoint.start_time_unix_nano":
		return pathGetter("start_timestamp_unix_nano")
	case "datapoint.flags":
		return pathGetter("flags")
	case "datapoint.attributes":
		return pathGetter("attributes")
	case "datapoint.value":
		return pathGetter("value")
	case "datapoint.value_type":
		return pathGetter("value_type")
	case "datapoint.int_value":
		return pathGetter("int_value")
	case "datapoint.double_value":
		return pathGetter("double_value")
	case "datapoint.number_value":
		return pathGetter("number_value")
	case "datapoint.distribution":
		return pathGetter("distribution")
	case "datapoint.exemplars":
		return pathGetter("exemplars")
	default:
		return nil
	}
}

func timestampGetter(key string) recordGetter {
	return func(record Record) (any, bool) {
		raw, ok := pathGetter(key)(record)
		if !ok {
			return nil, false
		}
		text, ok := raw.(string)
		if !ok {
			return nil, false
		}
		value := unixNanoStringToRFC3339(text)
		return value, value != ""
	}
}

func pathGetter(key string) recordGetter {
	return func(record Record) (any, bool) {
		value, ok := record[key]
		return presentValue(value, ok)
	}
}

func nestedPathGetter(path ...string) recordGetter {
	return func(record Record) (any, bool) {
		var current any = map[string]any(record)
		for _, key := range path {
			values, ok := current.(map[string]any)
			if !ok {
				if typed, recordOK := current.(Record); recordOK {
					values = map[string]any(typed)
				} else {
					return nil, false
				}
			}
			current, ok = values[key]
			if !ok {
				return nil, false
			}
		}
		return presentValue(current, true)
	}
}

func nestedMapKeyGetter(path []string, key string) recordGetter {
	base := nestedPathGetter(path...)
	return func(record Record) (any, bool) {
		value, ok := base(record)
		if !ok {
			return nil, false
		}
		values, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		item, ok := values[key]
		return presentValue(item, ok)
	}
}

func presentValue(value any, ok bool) (any, bool) {
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}
