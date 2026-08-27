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
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const SampleInspectionVersion = 1

type SampleInspection struct {
	Version int                     `json:"version"`
	Signal  string                  `json:"signal"`
	Records int                     `json:"records"`
	Fields  []SampleFieldInspection `json:"fields"`
}

type SampleFieldInspection struct {
	Group         string   `json:"group"`
	Selector      string   `json:"selector"`
	ObservedTypes []string `json:"observed_types"`
	Populated     int      `json:"populated"`
	Total         int      `json:"total"`
}

type sampleFieldObservation struct {
	group     string
	types     map[string]struct{}
	populated int
}

type sampleSelector struct {
	name string
	get  recordGetter
}

// InspectSample reports the mapping selectors observed in a representative
// OTLP sample. It uses the production mapper so selectors and value types match
// preview and runtime projection, while omitting empty protocol defaults.
func InspectSample(signal string, sample []byte) (SampleInspection, error) {
	payload, err := mapOTLPSample(signal, sample)
	if err != nil {
		return SampleInspection{}, err
	}
	if len(payload.Records) == 0 {
		return SampleInspection{}, fmt.Errorf("%s OTLP sample contains no records", signal)
	}

	selectors := append([]string(nil), commonSourceSelectors...)
	selectors = append(selectors, signalSourceSelectors[signal]...)
	compiled := make([]sampleSelector, 0, len(selectors))
	for _, selector := range selectors {
		getter, err := compileRecordGetter(signal, selector)
		if err != nil {
			return SampleInspection{}, err
		}
		compiled = append(compiled, sampleSelector{name: selector, get: getter})
	}
	observed := make(map[string]*sampleFieldObservation, len(selectors))
	for _, record := range payload.Records {
		for _, selector := range compiled {
			value, present := selector.get(record)
			if !present || !sampleFieldPopulated(selector.name, value) {
				continue
			}
			observeSampleField(observed, selector.name, value)
			expandSampleObject(observed, selector.name, value)
		}
	}

	fields := make([]SampleFieldInspection, 0, len(observed))
	for selector, observation := range observed {
		types := make([]string, 0, len(observation.types))
		for valueType := range observation.types {
			types = append(types, valueType)
		}
		sort.Strings(types)
		fields = append(fields, SampleFieldInspection{
			Group:         observation.group,
			Selector:      selector,
			ObservedTypes: types,
			Populated:     observation.populated,
			Total:         len(payload.Records),
		})
	}
	sort.Slice(fields, func(left int, right int) bool {
		leftRank := sampleFieldGroupRank(fields[left].Group)
		rightRank := sampleFieldGroupRank(fields[right].Group)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return fields[left].Selector < fields[right].Selector
	})

	return SampleInspection{
		Version: SampleInspectionVersion,
		Signal:  signal,
		Records: len(payload.Records),
		Fields:  fields,
	}, nil
}

func observeSampleField(observed map[string]*sampleFieldObservation, selector string, value any) {
	observation := observed[selector]
	if observation == nil {
		observation = &sampleFieldObservation{
			group: sampleFieldGroup(selector),
			types: make(map[string]struct{}),
		}
		observed[selector] = observation
	}
	observation.populated++
	observation.types[sampleFieldType(selector, value)] = struct{}{}
}

func expandSampleObject(observed map[string]*sampleFieldObservation, selector string, value any) {
	values, ok := value.(map[string]any)
	if !ok {
		if record, recordOK := value.(Record); recordOK {
			values = map[string]any(record)
		} else {
			return
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		child := values[key]
		childSelector := selector + "[" + strconv.Quote(key) + "]"
		if !sampleFieldPopulated(childSelector, child) {
			continue
		}
		observeSampleField(observed, childSelector, child)
		expandSampleObject(observed, childSelector, child)
	}
}

func sampleFieldPopulated(selector string, value any) bool {
	if value == nil {
		return false
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return reflected.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return !sampleFieldDefaultZero(selector) || reflected.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return !sampleFieldDefaultZero(selector) || reflected.Uint() != 0
	default:
		return true
	}
}

func sampleFieldDefaultZero(selector string) bool {
	switch selector {
	case "resource.dropped_attributes_count",
		"scope.dropped_attributes_count",
		"log.severity_number",
		"log.flags",
		"log.dropped_attributes_count",
		"span.flags",
		"span.dropped_attributes_count",
		"span.dropped_events_count",
		"span.dropped_links_count",
		"datapoint.flags":
		return true
	default:
		return false
	}
}

func sampleFieldType(selector string, value any) string {
	if selectorTypeFor(selector) == selectorTypeTimestamp {
		return string(selectorTypeTimestamp)
	}
	if value == nil {
		return "null"
	}
	valueType := reflect.TypeOf(value)
	switch valueType.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "uint"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Array, reflect.Slice:
		return "array"
	default:
		return valueType.String()
	}
}

func sampleFieldGroup(selector string) string {
	prefix, _, _ := strings.Cut(selector, ".")
	return prefix
}

func sampleFieldGroupRank(group string) int {
	switch group {
	case "resource":
		return 0
	case "scope":
		return 1
	case "log", "span", "metric":
		return 2
	case "datapoint":
		return 3
	default:
		return 4
	}
}
