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
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	scopedb "github.com/scopedb/goscopedb"
)

// Each selector defines its type and record access in one place for mapping,
// sample inspection, and typo suggestions. record.id is bound to its signal
// when the mapping is compiled.
var sourceSelectors = map[string]struct {
	valueType selectorType
	get       recordGetter
}{
	"record":                            {selectorTypeObject, func(record telemetryRecord) (any, bool) { return record, true }},
	"record.id":                         {selectorTypeString, nil},
	"resource.attributes":               {selectorTypeObject, pathGetter("resource")},
	"resource.schema_url":               {selectorTypeString, pathGetter("resource_schema_url")},
	"resource.dropped_attributes_count": {selectorTypeUInt, pathGetter("resource_dropped_attributes_count")},
	"scope.name":                        {selectorTypeString, pathGetter("scope", "name")},
	"scope.version":                     {selectorTypeString, pathGetter("scope", "version")},
	"scope.attributes":                  {selectorTypeObject, pathGetter("scope", "attributes")},
	"scope.schema_url":                  {selectorTypeString, pathGetter("scope_schema_url")},
	"scope.dropped_attributes_count":    {selectorTypeUInt, pathGetter("scope", "dropped_attributes_count")},
	"log.timestamp":                     {selectorTypeTimestamp, timestampGetter("timestamp_unix_nano")},
	"log.observed_timestamp":            {selectorTypeTimestamp, timestampGetter("observed_timestamp_unix_nano")},
	"log.timestamp_unix_nano":           {selectorTypeString, pathGetter("timestamp_unix_nano")},
	"log.observed_timestamp_unix_nano":  {selectorTypeString, pathGetter("observed_timestamp_unix_nano")},
	"log.trace_id":                      {selectorTypeString, pathGetter("trace_id")},
	"log.span_id":                       {selectorTypeString, pathGetter("span_id")},
	"log.event_name":                    {selectorTypeString, pathGetter("event_name")},
	"log.severity_text":                 {selectorTypeString, pathGetter("status")},
	"log.severity_number":               {selectorTypeInt, pathGetter("severity_number")},
	"log.flags":                         {selectorTypeUInt, pathGetter("flags")},
	"log.dropped_attributes_count":      {selectorTypeUInt, pathGetter("dropped_attributes_count")},
	"log.body":                          {selectorTypeDynamic, pathGetter("body")},
	"log.message":                       {selectorTypeString, pathGetter("message")},
	"log.attributes":                    {selectorTypeObject, pathGetter("attributes")},
	"span.trace_id":                     {selectorTypeString, pathGetter("trace_id")},
	"span.span_id":                      {selectorTypeString, pathGetter("span_id")},
	"span.parent_span_id":               {selectorTypeString, pathGetter("parent_span_id")},
	"span.trace_state":                  {selectorTypeString, pathGetter("trace_state")},
	"span.flags":                        {selectorTypeUInt, pathGetter("flags")},
	"span.name":                         {selectorTypeString, pathGetter("name")},
	"span.kind":                         {selectorTypeString, pathGetter("kind")},
	"span.start_time":                   {selectorTypeTimestamp, timestampGetter("start_time_unix_nano")},
	"span.end_time":                     {selectorTypeTimestamp, timestampGetter("end_time_unix_nano")},
	"span.start_time_unix_nano":         {selectorTypeString, pathGetter("start_time_unix_nano")},
	"span.end_time_unix_nano":           {selectorTypeString, pathGetter("end_time_unix_nano")},
	"span.duration_ns":                  {selectorTypeInt, pathGetter("duration_ns")},
	"span.status.code":                  {selectorTypeString, pathGetter("status_code")},
	"span.status.message":               {selectorTypeString, pathGetter("status_message")},
	"span.attributes":                   {selectorTypeObject, pathGetter("attributes")},
	"span.dropped_attributes_count":     {selectorTypeUInt, pathGetter("dropped_attributes_count")},
	"span.events":                       {selectorTypeArray, pathGetter("events")},
	"span.dropped_events_count":         {selectorTypeUInt, pathGetter("dropped_events_count")},
	"span.links":                        {selectorTypeArray, pathGetter("links")},
	"span.dropped_links_count":          {selectorTypeUInt, pathGetter("dropped_links_count")},
	"metric.name":                       {selectorTypeString, pathGetter("metric_name")},
	"metric.description":                {selectorTypeString, pathGetter("description")},
	"metric.unit":                       {selectorTypeString, pathGetter("unit")},
	"metric.type":                       {selectorTypeString, pathGetter("type")},
	"metric.metadata":                   {selectorTypeObject, pathGetter("metadata")},
	"metric.temporality":                {selectorTypeString, pathGetter("temporality")},
	"metric.is_monotonic":               {selectorTypeBoolean, pathGetter("is_monotonic")},
	"datapoint.timestamp":               {selectorTypeTimestamp, timestampGetter("timestamp_unix_nano")},
	"datapoint.start_time":              {selectorTypeTimestamp, timestampGetter("start_timestamp_unix_nano")},
	"datapoint.timestamp_unix_nano":     {selectorTypeString, pathGetter("timestamp_unix_nano")},
	"datapoint.start_time_unix_nano":    {selectorTypeString, pathGetter("start_timestamp_unix_nano")},
	"datapoint.flags":                   {selectorTypeUInt, pathGetter("flags")},
	"datapoint.attributes":              {selectorTypeObject, pathGetter("attributes")},
	"datapoint.value":                   {selectorTypeNumber, pathGetter("value")},
	"datapoint.value_type":              {selectorTypeString, pathGetter("value_type")},
	"datapoint.int_value":               {selectorTypeInt, pathGetter("int_value")},
	"datapoint.double_value":            {selectorTypeFloat, pathGetter("double_value")},
	"datapoint.number_value":            {selectorTypeFloat, pathGetter("number_value")},
	"datapoint.distribution":            {selectorTypeObject, pathGetter("distribution")},
	"datapoint.exemplars":               {selectorTypeArray, pathGetter("exemplars")},
}

type recordGetter func(telemetryRecord) (any, bool)

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

	names := slices.Sorted(maps.Keys(columns))

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

func (p *mappingPlan) project(record telemetryRecord) (map[string]any, error) {
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
	if selector, ok := sourceSelectors[strings.TrimSpace(source)]; ok {
		return selector.valueType
	}
	return selectorTypeDynamic
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

	compatible := string(t) == string(actual)
	switch t {
	case selectorTypeTimestamp:
		compatible = compatible || actual == scopedb.StringDataType
	case selectorTypeUInt:
		compatible = compatible || actual == scopedb.IntDataType
	case selectorTypeArray:
		compatible = compatible || actual == scopedb.ObjectDataType
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

	if signal != signalLogs && signal != signalTraces && signal != signalMetrics {
		return nil, fmt.Errorf("unsupported signal %q", signal)
	}
	if expected := selectorSignal(base); expected != "" && expected != signal {
		return nil, fmt.Errorf("source %q is only valid for %s", source, expected)
	}
	selector, ok := sourceSelectors[base]
	if !ok {
		if suggestion := suggestSource(signal, source); suggestion != "" {
			return nil, fmt.Errorf("unsupported %s source %q; did you mean %q?", signal, source, suggestion)
		}
		return nil, fmt.Errorf("unsupported %s source %q", signal, source)
	}
	getter := selector.get
	if base == "record.id" {
		getter = func(record telemetryRecord) (any, bool) { return recordID(signal, record) }
	}
	if len(steps) == 0 {
		return getter, nil
	}

	baseType := selector.valueType
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
	return func(record telemetryRecord) (any, bool) {
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
			values, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			current = values[step.key]
		}
		return current, current != nil
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
	best := ""
	bestDistance := len(comparedSource) + 1
	for _, candidate := range slices.Sorted(maps.Keys(sourceSelectors)) {
		if expected := selectorSignal(candidate); expected != "" && expected != signal {
			continue
		}
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

func timestampGetter(key string) recordGetter {
	return func(record telemetryRecord) (any, bool) {
		raw, ok := record[key]
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

func pathGetter(key string, nested ...string) recordGetter {
	get := func(record telemetryRecord) (any, bool) {
		value := record[key]
		return value, value != nil
	}
	if len(nested) == 0 {
		return get
	}
	steps := make([]selectorPathStep, len(nested))
	for i, key := range nested {
		steps[i].key = key
	}
	return selectorPathGetter(get, steps)
}
