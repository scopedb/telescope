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

const (
	mappingPreviewRowLimit   = 3
	mappingPreviewErrorLimit = 20
)

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
	OutputType       string
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
	Errors        int
	ObservedTypes []string
	Selections    []MappingSelectionPreview
}

type MappingSelectionPreview struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
}

type MappingPreviewError struct {
	Record  int
	Column  string
	Source  string
	Message string
}

type MappingPreview struct {
	Signal         string
	Records        int
	ValidRecords   int
	InvalidRecords int
	ErrorCount     int
	Errors         []MappingPreviewError
	Columns        []MappingColumnPreview
	Rows           []map[string]any
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
		Errors:  make([]MappingPreviewError, 0),
		Columns: make([]MappingColumnPreview, 0, len(plan.columns)),
		Rows:    make([]map[string]any, 0, min(mappingPreviewRowLimit, len(payload.Records))),
	}
	observed := make([]map[string]struct{}, len(plan.columns))
	present := make([]int, len(plan.columns))
	columnErrors := make([]int, len(plan.columns))
	resolutionCounts := make([][]int, len(plan.columns))
	for index := range observed {
		observed[index] = make(map[string]struct{})
		resolutionCounts[index] = make([]int, len(plan.columns[index].resolutions))
	}
	for recordIndex, record := range payload.Records {
		row := make(map[string]any, len(plan.columns))
		valid := true
		for columnIndex, column := range plan.columns {
			evaluation, err := column.get(record)
			if evaluation.resolution >= 0 && evaluation.resolution < len(column.resolutions) {
				resolutionCounts[columnIndex][evaluation.resolution]++
			}
			if err != nil {
				valid = false
				preview.ErrorCount++
				columnErrors[columnIndex]++
				if len(preview.Errors) < mappingPreviewErrorLimit {
					preview.Errors = append(preview.Errors, MappingPreviewError{
						Record:  recordIndex + 1,
						Column:  column.name,
						Source:  mappingResolutionSource(column, evaluation.resolution),
						Message: err.Error(),
					})
				}
				continue
			}
			if !evaluation.present {
				continue
			}
			row[column.name] = evaluation.value
			present[columnIndex]++
			observed[columnIndex][observedType(column.outputType, evaluation.value)] = struct{}{}
		}
		if valid {
			preview.ValidRecords++
			if len(preview.Rows) < mappingPreviewRowLimit {
				preview.Rows = append(preview.Rows, row)
			}
		} else {
			preview.InvalidRecords++
		}
	}
	for index, column := range plan.columns {
		types := make([]string, 0, len(observed[index]))
		for valueType := range observed[index] {
			types = append(types, valueType)
		}
		sort.Strings(types)
		selections := make([]MappingSelectionPreview, 0, len(column.resolutions))
		for resolutionIndex, count := range resolutionCounts[index] {
			if count == 0 {
				continue
			}
			selections = append(selections, MappingSelectionPreview{
				Source: column.resolutions[resolutionIndex],
				Count:  count,
			})
		}
		preview.Columns = append(preview.Columns, MappingColumnPreview{
			MappingColumnDescription: describeMappedColumn(column),
			Present:                  present[index],
			Total:                    len(payload.Records),
			Errors:                   columnErrors[index],
			ObservedTypes:            types,
			Selections:               selections,
		})
	}
	return preview, nil
}

func mappingResolutionSource(column mappedColumn, resolution int) string {
	if resolution < 0 || resolution >= len(column.resolutions) {
		return "-"
	}
	return column.resolutions[resolution]
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
		OutputType:       string(column.outputType),
		RuntimeDependent: column.runtimeDependent,
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
		if err := unmarshalOTLPRequest(signal, request, sample, isJSON); err != nil {
			return nil, err
		}
		return mapLogs(request.Logs())
	case signalTraces:
		request := ptraceotlp.NewExportRequest()
		if err := unmarshalOTLPRequest(signal, request, sample, isJSON); err != nil {
			return nil, err
		}
		return mapTraces(request.Traces())
	case signalMetrics:
		request := pmetricotlp.NewExportRequest()
		if err := unmarshalOTLPRequest(signal, request, sample, isJSON); err != nil {
			return nil, err
		}
		return mapMetrics(request.Metrics())
	default:
		return nil, fmt.Errorf("unsupported signal %q", signal)
	}
}

type otlpRequest interface {
	UnmarshalJSON([]byte) error
	UnmarshalProto([]byte) error
}

func unmarshalOTLPRequest(signal string, request otlpRequest, sample []byte, jsonEncoding bool) error {
	encoding := "protobuf"
	var err error
	if jsonEncoding {
		encoding = "JSON"
		err = request.UnmarshalJSON(sample)
	} else {
		err = request.UnmarshalProto(sample)
	}
	if err != nil {
		return fmt.Errorf("decode %s OTLP %s sample: %w", signal, encoding, err)
	}
	return nil
}

func observedType(sourceType selectorType, value any) string {
	if sourceType == selectorTypeTimestamp {
		return string(selectorTypeTimestamp)
	}
	if _, ok := value.([]byte); ok {
		return "binary"
	}
	if valueType := selectorTypeForValue(value); valueType != selectorTypeDynamic {
		return string(valueType)
	}
	return fmt.Sprintf("%T", value)
}
