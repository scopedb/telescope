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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

type sampleFlags struct {
	paths map[string]string
}

type mappingSample struct {
	path    string
	preview scopedbexporter.MappingPreview
}

func (samples *sampleFlags) String() string {
	if samples == nil || len(samples.paths) == 0 {
		return ""
	}
	values := make([]string, 0, len(samples.paths))
	for signal, path := range samples.paths {
		values = append(values, signal+"="+path)
	}
	sort.Strings(values)
	return strings.Join(values, ",")
}

func (samples *sampleFlags) Set(value string) error {
	signal, path, ok := strings.Cut(value, "=")
	signal = strings.TrimSpace(signal)
	path = strings.TrimSpace(path)
	if !ok || signal == "" || path == "" {
		return fmt.Errorf("sample must be signal=path")
	}
	if signal != "logs" && signal != "traces" && signal != "metrics" {
		return fmt.Errorf("unsupported sample signal %q; choose logs, traces, or metrics", signal)
	}
	if samples.paths == nil {
		samples.paths = make(map[string]string, 3)
	}
	if _, exists := samples.paths[signal]; exists {
		return fmt.Errorf("sample for %s was provided more than once", signal)
	}
	samples.paths[signal] = path
	return nil
}

func loadMappingSamples(paths map[string]string, ingestion scopedbexporter.IngestionConfig) ([]mappingSample, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	enabled := make(map[string]bool, len(ingestion.EnabledSignals()))
	for _, signal := range ingestion.EnabledSignals() {
		enabled[signal] = true
	}
	for signal := range paths {
		if !enabled[signal] {
			return nil, fmt.Errorf("sample provided for disabled signal %q", signal)
		}
	}

	previews := make([]mappingSample, 0, len(paths))
	for _, signal := range ingestion.EnabledSignals() {
		path, ok := paths[signal]
		if !ok {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s sample %s: %w", signal, path, err)
		}
		config, _ := ingestion.Signal(signal)
		preview, err := scopedbexporter.PreviewMapping(signal, config, contents)
		if err != nil {
			return nil, fmt.Errorf("preview %s sample %s: %w", signal, path, err)
		}
		previews = append(previews, mappingSample{path: path, preview: preview})
	}
	return previews, nil
}

func printMappingPreview(
	w io.Writer,
	sample mappingSample,
	destinations []scopedbexporter.SignalDestinationValidation,
) error {
	preview := sample.preview
	fmt.Fprintf(w, "sample %s <- %s (%d records)\n", preview.Signal, sample.path, preview.Records)
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "COLUMN\tSOURCE\tTARGET\tOBSERVED\tCOVERAGE\tRESULT")
	var mismatched []string
	for _, column := range preview.Columns {
		target := findDestinationColumn(destinations, preview.Signal, column.Column)
		targetType := "-"
		result := "observed"
		if column.Present == 0 {
			result = "unobserved"
		} else if column.Present < column.Total {
			result = "partial"
		}
		if target != nil {
			targetType = target.TargetType
			result = previewColumnResult(column, *target)
			if result == "sample-mismatch" {
				mismatched = append(mismatched, column.Column)
			}
		}
		observed := "-"
		if len(column.ObservedTypes) > 0 {
			observed = strings.Join(column.ObservedTypes, "|")
		}
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			column.Column,
			column.Source,
			targetType,
			observed,
			formatCoverage(column.Present, column.Total),
			result,
		)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "projected NDJSON (first %d of %d):\n", len(preview.Rows), preview.Records)
	for _, row := range preview.Rows {
		line, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("marshal preview row: %w", err)
		}
		fmt.Fprintln(w, string(line))
	}
	if len(mismatched) > 0 {
		return fmt.Errorf("%s sample has values incompatible with destination columns: %s", preview.Signal, strings.Join(mismatched, ", "))
	}
	return nil
}

func findDestinationColumn(
	validations []scopedbexporter.SignalDestinationValidation,
	signal string,
	column string,
) *scopedbexporter.DestinationColumnValidation {
	for validationIndex := range validations {
		if validations[validationIndex].Signal != signal {
			continue
		}
		for columnIndex := range validations[validationIndex].Columns {
			if validations[validationIndex].Columns[columnIndex].Column == column {
				return &validations[validationIndex].Columns[columnIndex]
			}
		}
	}
	return nil
}

func previewColumnResult(
	preview scopedbexporter.MappingColumnPreview,
	destination scopedbexporter.DestinationColumnValidation,
) string {
	if destination.Compatibility == scopedbexporter.MappingMissing {
		return "missing-column"
	}
	if destination.Compatibility == scopedbexporter.MappingIncompatible {
		return "static-mismatch"
	}
	if preview.Present == 0 {
		return "unobserved"
	}
	if destination.Compatibility == scopedbexporter.MappingCompatible && !destination.RuntimeDependent {
		if preview.Present < preview.Total {
			return "partial"
		}
		return "static-ok"
	}
	if destination.TargetType == "any" {
		if preview.Present < preview.Total {
			return "partial"
		}
		return "target-any"
	}
	uncertain := false
	for _, observed := range preview.ObservedTypes {
		compatible, conclusive := observedTypeCompatibility(observed, destination.TargetType)
		if conclusive && !compatible {
			return "sample-mismatch"
		}
		if !conclusive {
			uncertain = true
		}
	}
	if uncertain {
		return "review"
	}
	if preview.Present < preview.Total {
		return "partial"
	}
	return "sample-ok"
}

func observedTypeCompatibility(observed string, target string) (bool, bool) {
	if target == "any" || observed == target {
		return true, true
	}
	switch observed {
	case "timestamp":
		return target == "string", true
	case "uint":
		if target == "int" {
			return true, true
		}
		if target == "float" {
			return false, false
		}
	case "array":
		return target == "object", true
	case "string":
		if target == "timestamp" || target == "binary" {
			return false, false
		}
	case "int":
		if target == "float" {
			return false, false
		}
	}
	return false, true
}

func formatCoverage(present int, total int) string {
	if total == 0 {
		return "0/0"
	}
	return fmt.Sprintf("%.0f%% (%d/%d)", 100*float64(present)/float64(total), present, total)
}
