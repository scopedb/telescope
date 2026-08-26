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
	"bytes"
	"errors"
	"fmt"
	"sort"

	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

const mappingPreviewRowLimit = 3

type MappingCompatibility string

const (
	MappingCompatible       MappingCompatibility = "compatible"
	MappingRuntimeDependent MappingCompatibility = "runtime-dependent"
	MappingIncompatible     MappingCompatibility = "incompatible"
	MappingMissing          MappingCompatibility = "missing"
)

type MappingColumnDescription struct {
	Column           string
	Source           string
	SelectorType     string
	RuntimeDependent bool
}

type SignalMappingDescription struct {
	Signal  string
	Table   string
	Columns []MappingColumnDescription
}

type DestinationColumnValidation struct {
	MappingColumnDescription
	TargetType    string
	Compatibility MappingCompatibility
}

type SignalDestinationValidation struct {
	Signal  string
	Columns []DestinationColumnValidation
}

type MappingColumnPreview struct {
	MappingColumnDescription
	Present       int
	Total         int
	ObservedTypes []string
}

type MappingPreview struct {
	Signal  string
	Records int
	Columns []MappingColumnPreview
	Rows    []map[string]any
}

func DescribeIngestionMappings(ingestion IngestionConfig) ([]SignalMappingDescription, error) {
	if err := ingestion.Validate(); err != nil {
		return nil, err
	}
	descriptions := make([]SignalMappingDescription, 0, len(ingestion.EnabledSignals()))
	for _, signal := range ingestion.EnabledSignals() {
		config, _ := ingestion.Signal(signal)
		plan, err := compileMappingPlan(signal, config.Table, config.Mapping)
		if err != nil {
			return nil, fmt.Errorf("signals.%s: %w", signal, err)
		}
		descriptions = append(descriptions, describeMappingPlan(plan))
	}
	return descriptions, nil
}

func PreviewMapping(signal string, config SignalIngestionConfig, sample []byte) (MappingPreview, error) {
	plan, err := compileMappingPlan(signal, config.Table, config.Mapping)
	if err != nil {
		return MappingPreview{}, err
	}
	payload, err := mapOTLPSample(signal, sample)
	if err != nil {
		return MappingPreview{}, err
	}
	if len(payload.Records) == 0 {
		return MappingPreview{}, fmt.Errorf("%s OTLP sample contains no records", signal)
	}

	preview := MappingPreview{
		Signal:  signal,
		Records: len(payload.Records),
		Columns: make([]MappingColumnPreview, 0, len(plan.columns)),
		Rows:    make([]map[string]any, 0, min(mappingPreviewRowLimit, len(payload.Records))),
	}
	observed := make([]map[string]struct{}, len(plan.columns))
	present := make([]int, len(plan.columns))
	for index := range observed {
		observed[index] = make(map[string]struct{})
	}
	for recordIndex, record := range payload.Records {
		if recordIndex < mappingPreviewRowLimit {
			preview.Rows = append(preview.Rows, plan.project(record))
		}
		for columnIndex, column := range plan.columns {
			value, ok := column.get(record)
			if !ok {
				continue
			}
			present[columnIndex]++
			observed[columnIndex][observedType(column.sourceType, value)] = struct{}{}
		}
	}
	for index, column := range plan.columns {
		types := make([]string, 0, len(observed[index]))
		for valueType := range observed[index] {
			types = append(types, valueType)
		}
		sort.Strings(types)
		preview.Columns = append(preview.Columns, MappingColumnPreview{
			MappingColumnDescription: describeMappedColumn(column),
			Present:                  present[index],
			Total:                    len(payload.Records),
			ObservedTypes:            types,
		})
	}
	return preview, nil
}

func describeMappingPlan(plan *mappingPlan) SignalMappingDescription {
	description := SignalMappingDescription{
		Signal:  plan.signal,
		Table:   plan.table.String(),
		Columns: make([]MappingColumnDescription, 0, len(plan.columns)),
	}
	for _, column := range plan.columns {
		description.Columns = append(description.Columns, describeMappedColumn(column))
	}
	return description
}

func describeMappedColumn(column mappedColumn) MappingColumnDescription {
	return MappingColumnDescription{
		Column:           column.name,
		Source:           column.source,
		SelectorType:     string(column.sourceType),
		RuntimeDependent: column.sourceType.runtimeDependent(),
	}
}

func mapOTLPSample(signal string, sample []byte) (*IngestPayload, error) {
	trimmed := bytes.TrimSpace(sample)
	if len(trimmed) == 0 {
		return nil, errors.New("OTLP sample is empty")
	}
	isJSON := trimmed[0] == '{' || trimmed[0] == '['

	switch signal {
	case signalLogs:
		request := plogotlp.NewExportRequest()
		if err := unmarshalLogRequest(request, sample, isJSON); err != nil {
			return nil, err
		}
		return mapLogs(request.Logs())
	case signalTraces:
		request := ptraceotlp.NewExportRequest()
		if err := unmarshalTraceRequest(request, sample, isJSON); err != nil {
			return nil, err
		}
		return mapTraces(request.Traces())
	case signalMetrics:
		request := pmetricotlp.NewExportRequest()
		if err := unmarshalMetricRequest(request, sample, isJSON); err != nil {
			return nil, err
		}
		return mapMetrics(request.Metrics())
	default:
		return nil, fmt.Errorf("unsupported signal %q", signal)
	}
}

func unmarshalLogRequest(request plogotlp.ExportRequest, sample []byte, jsonEncoding bool) error {
	if jsonEncoding {
		if err := request.UnmarshalJSON(sample); err != nil {
			return fmt.Errorf("decode logs OTLP JSON sample: %w", err)
		}
		return nil
	}
	if err := request.UnmarshalProto(sample); err != nil {
		return fmt.Errorf("decode logs OTLP protobuf sample: %w", err)
	}
	return nil
}

func unmarshalTraceRequest(request ptraceotlp.ExportRequest, sample []byte, jsonEncoding bool) error {
	if jsonEncoding {
		if err := request.UnmarshalJSON(sample); err != nil {
			return fmt.Errorf("decode traces OTLP JSON sample: %w", err)
		}
		return nil
	}
	if err := request.UnmarshalProto(sample); err != nil {
		return fmt.Errorf("decode traces OTLP protobuf sample: %w", err)
	}
	return nil
}

func unmarshalMetricRequest(request pmetricotlp.ExportRequest, sample []byte, jsonEncoding bool) error {
	if jsonEncoding {
		if err := request.UnmarshalJSON(sample); err != nil {
			return fmt.Errorf("decode metrics OTLP JSON sample: %w", err)
		}
		return nil
	}
	if err := request.UnmarshalProto(sample); err != nil {
		return fmt.Errorf("decode metrics OTLP protobuf sample: %w", err)
	}
	return nil
}

func observedType(sourceType selectorType, value any) string {
	if sourceType == selectorTypeTimestamp {
		return string(selectorTypeTimestamp)
	}
	switch value.(type) {
	case string:
		return "string"
	case []byte:
		return "binary"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64:
		return "int"
	case uint, uint8, uint16, uint32, uint64:
		return "uint"
	case float32, float64:
		return "float"
	case map[string]any, Record:
		return "object"
	case []any, []map[string]any:
		return "array"
	default:
		return fmt.Sprintf("%T", value)
	}
}
