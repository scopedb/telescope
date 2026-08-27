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
	"reflect"
	"sort"
	"strconv"
	"strings"

	scopedb "github.com/scopedb/goscopedb"
)

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
	name             string
	source           string
	outputType       selectorType
	runtimeDependent bool
	resolutions      []string
	get              mappingEvaluator
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
	selectorTypeAny       selectorType = "any"
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

func compileMappingPlan(signal string, table string, columns MappingConfig) (*mappingPlan, error) {
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
		compiled, err := compileMappingRule(signal, columns[name])
		if err != nil {
			errs = append(errs, fmt.Errorf("column %q: %w", name, err))
			valid = false
		}
		if !valid {
			continue
		}
		plan.columns = append(plan.columns, mappedColumn{
			name:             name,
			source:           compiled.description,
			outputType:       compiled.valueType,
			runtimeDependent: compiled.runtimeDependent,
			resolutions:      compiled.resolutions,
			get:              compiled.evaluate,
		})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return plan, nil
}

func (p *mappingPlan) project(record Record) (map[string]any, error) {
	row := make(map[string]any, len(p.columns))
	for _, column := range p.columns {
		evaluation, err := column.get(record)
		if err != nil {
			return nil, fmt.Errorf("column %q (%s): %w", column.name, column.source, err)
		}
		if evaluation.present {
			row[column.name] = evaluation.value
		}
	}
	return row, nil
}

func selectorTypeFor(source string) selectorType {
	base, steps, err := parseSelectorPath(source)
	if err != nil || len(steps) > 0 {
		return selectorTypeDynamic
	}

	switch base {
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
	case selectorTypeAny:
		compatible = actual == scopedb.AnyDataType
	}
	if compatible {
		return MappingCompatible
	}
	return MappingIncompatible
}

func compileRecordGetter(signal string, source string) (recordGetter, error) {
	base, steps, err := parseSelectorPath(source)
	if err != nil {
		return nil, err
	}
	if base == "" {
		return nil, fmt.Errorf("source is required")
	}

	getter := commonRecordGetter(base)
	if getter == nil {
		switch signal {
		case signalLogs:
			getter = logRecordGetter(base)
		case signalTraces:
			getter = traceRecordGetter(base)
		case signalMetrics:
			getter = metricRecordGetter(base)
		default:
			return nil, fmt.Errorf("unsupported signal %q", signal)
		}
	}
	if getter == nil {
		if expected := selectorSignal(base); expected != "" && expected != signal {
			return nil, fmt.Errorf("source %q is only valid for %s", source, expected)
		}
		if suggestion := suggestSource(signal, source); suggestion != "" {
			return nil, fmt.Errorf("unsupported %s source %q; did you mean %q?", signal, source, suggestion)
		}
		return nil, fmt.Errorf("unsupported %s source %q", signal, source)
	}
	if len(steps) == 0 {
		return getter, nil
	}

	baseType := selectorTypeFor(base)
	first := steps[0]
	switch baseType {
	case selectorTypeObject:
		if first.isIndex {
			return nil, fmt.Errorf("source %q produces an object and requires a string key", base)
		}
	case selectorTypeArray:
		if !first.isIndex {
			return nil, fmt.Errorf("source %q produces an array and requires a numeric index", base)
		}
	case selectorTypeDynamic:
	default:
		return nil, fmt.Errorf("source %q produces %s and cannot be indexed", base, baseType)
	}
	return selectorPathGetter(getter, steps), nil
}

func selectorSignal(source string) string {
	switch {
	case strings.HasPrefix(source, "log."):
		return signalLogs
	case strings.HasPrefix(source, "span."):
		return signalTraces
	case strings.HasPrefix(source, "metric."), strings.HasPrefix(source, "datapoint."):
		return signalMetrics
	default:
		return ""
	}
}

type selectorPathStep struct {
	key     string
	index   int
	isIndex bool
}

func parseSelectorPath(source string) (string, []selectorPathStep, error) {
	value := strings.TrimSpace(source)
	if value == "" {
		return "", nil, nil
	}
	start := strings.IndexByte(value, '[')
	if start < 0 {
		return value, nil, nil
	}
	base := strings.TrimSpace(value[:start])
	if base == "" {
		return "", nil, fmt.Errorf("invalid source %q: selector base is required", source)
	}

	steps := make([]selectorPathStep, 0, 2)
	position := start
	for position < len(value) {
		if value[position] != '[' {
			return "", nil, fmt.Errorf("invalid source %q: expected '[' at offset %d", source, position)
		}
		position++
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		if position == len(value) {
			return "", nil, fmt.Errorf("invalid source %q: unclosed '['", source)
		}

		step := selectorPathStep{}
		if value[position] == '"' {
			quotedStart := position
			position++
			escaped := false
			for position < len(value) {
				character := value[position]
				position++
				if escaped {
					escaped = false
					continue
				}
				if character == '\\' {
					escaped = true
					continue
				}
				if character == '"' {
					break
				}
			}
			if position > len(value) || value[position-1] != '"' {
				return "", nil, fmt.Errorf("invalid source %q: unclosed string key", source)
			}
			key, err := strconv.Unquote(value[quotedStart:position])
			if err != nil {
				return "", nil, fmt.Errorf("invalid source %q: invalid string key: %w", source, err)
			}
			step.key = key
		} else {
			indexStart := position
			for position < len(value) && value[position] >= '0' && value[position] <= '9' {
				position++
			}
			if indexStart == position {
				return "", nil, fmt.Errorf("invalid source %q: path segment must be a quoted key or non-negative index", source)
			}
			index, err := strconv.Atoi(value[indexStart:position])
			if err != nil {
				return "", nil, fmt.Errorf("invalid source %q: invalid array index: %w", source, err)
			}
			step.index = index
			step.isIndex = true
		}

		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		if position == len(value) || value[position] != ']' {
			return "", nil, fmt.Errorf("invalid source %q: expected ']'", source)
		}
		position++
		for position < len(value) && (value[position] == ' ' || value[position] == '\t') {
			position++
		}
		steps = append(steps, step)
	}
	return base, steps, nil
}

func selectorPathGetter(base recordGetter, steps []selectorPathStep) recordGetter {
	return func(record Record) (any, bool) {
		current, ok := base(record)
		if !ok {
			return nil, false
		}
		for _, step := range steps {
			if step.isIndex {
				value := reflect.ValueOf(current)
				if !value.IsValid() || (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) || step.index >= value.Len() {
					return nil, false
				}
				current = value.Index(step.index).Interface()
				continue
			}
			switch values := current.(type) {
			case map[string]any:
				current, ok = values[step.key]
			case Record:
				current, ok = values[step.key]
			default:
				return nil, false
			}
			if !ok {
				return nil, false
			}
		}
		return presentValue(current, true)
	}
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

func presentValue(value any, ok bool) (any, bool) {
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}
