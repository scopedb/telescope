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
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/scopedb/telescope/scopedbexporter"
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
	slices.Sort(values)
	return strings.Join(values, ",")
}

func (samples *sampleFlags) Set(value string) error {
	signal, path, ok := strings.Cut(value, "=")
	signal = strings.TrimSpace(signal)
	path = strings.TrimSpace(path)
	if !ok || signal == "" || path == "" {
		return fmt.Errorf("sample must be signal=path")
	}
	if !supportedSignal(signal) {
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

func loadMappingSamples(paths map[string]string, ingestion scopedbexporter.IngestionConfig, stdin io.Reader) ([]mappingSample, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	enabled := ingestion.EnabledSignals()
	for signal := range paths {
		if !slices.Contains(enabled, signal) {
			return nil, fmt.Errorf("sample provided for disabled signal %q", signal)
		}
	}
	stdinSamples := 0
	for _, path := range paths {
		if path == "-" {
			stdinSamples++
		}
	}
	if stdinSamples > 1 {
		return nil, fmt.Errorf("stdin can provide only one signal sample")
	}

	previews := make([]mappingSample, 0, len(paths))
	for _, signal := range ingestion.EnabledSignals() {
		path, ok := paths[signal]
		if !ok {
			continue
		}
		contents, err := readSample(path, stdin)
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

func readSample(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path)
}

func printMappingPreview(
	w io.Writer,
	sample mappingSample,
	destinations []scopedbexporter.SignalDestinationValidation,
	strict bool,
) error {
	preview := sample.preview
	fmt.Fprintf(w, "sample %s <- %s (%d records)\n", preview.Signal, sample.path, preview.Records)
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	var targets map[string]scopedbexporter.DestinationColumnValidation
	for _, destination := range destinations {
		if destination.Signal == preview.Signal {
			targets = make(map[string]scopedbexporter.DestinationColumnValidation, len(destination.Columns))
			for _, column := range destination.Columns {
				targets[column.Column] = column
			}
			break
		}
	}
	hasDestination := targets != nil
	if hasDestination {
		fmt.Fprintln(table, "COLUMN\tTARGET\tOBSERVED\tCOVERAGE\tSELECTED\tRESULT")
	} else {
		fmt.Fprintln(table, "COLUMN\tOBSERVED\tCOVERAGE\tSELECTED\tRESULT")
	}
	var failures []string
	var warnings []string
	errorCount := preview.ErrorCount
	for _, column := range preview.Columns {
		// An unconstrained target checks only the sample's own mapping and coverage.
		target := scopedbexporter.DestinationColumnValidation{Compatibility: scopedbexporter.MappingCompatible}
		if hasDestination {
			var ok bool
			target, ok = targets[column.Column]
			if !ok {
				target.Compatibility = scopedbexporter.MappingMissing
			}
		}
		result := previewColumnResult(column, target)
		if strings.HasPrefix(result, "ERROR") {
			failures = append(failures, fmt.Sprintf("%s (%s)", column.Column, strings.TrimPrefix(result, "ERROR ")))
			if !strings.HasPrefix(result, "ERROR mapping") {
				errorCount++
			}
		}
		if strings.HasPrefix(result, "WARN") {
			warnings = append(warnings, fmt.Sprintf("%s (%s)", column.Column, strings.TrimPrefix(result, "WARN ")))
		}
		fmt.Fprint(table, column.Column+"\t")
		if hasDestination {
			fmt.Fprint(table, planValue(target.TargetType)+"\t")
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			planValue(strings.Join(column.ObservedTypes, "|")),
			formatCoverage(column.Present, column.Total),
			formatSelections(column.Selections), result,
		)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if preview.ErrorCount > 0 {
		fmt.Fprintf(w, "mapping errors (%d):\n", preview.ErrorCount)
		for _, issue := range preview.Errors {
			if issue.Source == "-" {
				fmt.Fprintf(w, "  record %d, column %s: %s\n", issue.Record, issue.Column, issue.Message)
			} else {
				fmt.Fprintf(w, "  record %d, column %s <- %s: %s\n", issue.Record, issue.Column, issue.Source, issue.Message)
			}
		}
		if omitted := preview.ErrorCount - len(preview.Errors); omitted > 0 {
			fmt.Fprintf(w, "  ... %d more\n", omitted)
		}
	}
	if preview.ValidRecords == 0 {
		fmt.Fprintln(w, "projected NDJSON: no valid records")
	} else if preview.InvalidRecords > 0 {
		fmt.Fprintf(w, "projected NDJSON (first %d of %d valid; %d invalid):\n", len(preview.Rows), preview.ValidRecords, preview.InvalidRecords)
	} else {
		fmt.Fprintf(w, "projected NDJSON (first %d of %d):\n", len(preview.Rows), preview.ValidRecords)
	}
	for _, row := range preview.Rows {
		line, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("marshal preview row: %w", err)
		}
		fmt.Fprintln(w, string(line))
	}
	fmt.Fprintf(w, "sample result: errors=%d, warnings=%d\n", errorCount, len(warnings))
	var resultErrors []error
	if len(failures) > 0 {
		resultErrors = append(resultErrors, fmt.Errorf("%s sample failed: %s", preview.Signal, strings.Join(failures, ", ")))
	}
	if strict && len(warnings) > 0 {
		resultErrors = append(resultErrors, fmt.Errorf("%s sample has warnings under --strict: %s", preview.Signal, strings.Join(warnings, ", ")))
	}
	return errors.Join(resultErrors...)
}

func previewColumnResult(
	preview scopedbexporter.MappingColumnPreview,
	destination scopedbexporter.DestinationColumnValidation,
) string {
	if destination.Compatibility == scopedbexporter.MappingMissing {
		return "ERROR missing column"
	}
	if destination.Compatibility == scopedbexporter.MappingIncompatible {
		return "ERROR type mismatch"
	}
	if preview.Errors > 0 {
		return fmt.Sprintf("ERROR mapping (%d)", preview.Errors)
	}
	if preview.Present == 0 {
		return "WARN unobserved"
	}
	if destination.Compatibility == scopedbexporter.MappingCompatible && !destination.RuntimeDependent {
		return previewCoverageResult(preview)
	}
	if destination.TargetType != "any" {
		uncertain := false
		for _, observed := range preview.ObservedTypes {
			compatible, conclusive := observedTypeCompatibility(observed, destination.TargetType)
			if conclusive && !compatible {
				return "ERROR sample type"
			}
			if !conclusive {
				uncertain = true
			}
		}
		if uncertain {
			return "WARN review type"
		}
	}
	return previewCoverageResult(preview)
}

func previewCoverageResult(preview scopedbexporter.MappingColumnPreview) string {
	if selectionCount(preview.Selections, "default") == preview.Total && preview.Total > 0 {
		return "WARN default only"
	}
	if preview.Present < preview.Total {
		return "WARN partial"
	}
	return "OK"
}

func selectionCount(selections []scopedbexporter.MappingSelectionPreview, source string) int {
	for _, selection := range selections {
		if selection.Source == source {
			return selection.Count
		}
	}
	return 0
}

func formatSelections(selections []scopedbexporter.MappingSelectionPreview) string {
	if len(selections) == 0 {
		return "-"
	}
	formatted := make([]string, 0, len(selections))
	for _, selection := range selections {
		formatted = append(formatted, fmt.Sprintf("%s (%d)", selection.Source, selection.Count))
	}
	return strings.Join(formatted, ", ")
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
