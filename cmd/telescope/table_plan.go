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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/scopedb/telescope/internal/collector"
	"github.com/scopedb/telescope/packages/scopedbexporter"
	"go.yaml.in/yaml/v3"
)

func runPlan(args []string) error {
	return runPlanCommand(args, os.Stdin, os.Stdout, os.Stderr)
}

func runPlanCommand(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	bootstrap := addBootstrapFlags(flags)
	format := flags.String("format", "human", "output format: human, json, or scopeql")
	outputPath := flags.String("out", "", "write additive ScopeQL to a file while printing the human plan")
	timeout := flags.Duration("timeout", 30*time.Second, "ScopeDB catalog timeout")
	var samples sampleFlags
	flags.Var(&samples, "sample", "representative OTLP JSON or protobuf as signal=path; use - for stdin; repeat per signal")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: telescope plan [options] [telescope.yaml]")
		fmt.Fprintln(stderr, "\nCompare the mapping contract with live ScopeDB tables. The command never applies DDL.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nExample:")
		fmt.Fprintln(stderr, "  telescope plan --sample traces=traces.otlp.json --out tables.scopeql telescope.yaml")
		fmt.Fprintln(stderr, "  scopeql run -f tables.scopeql")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	outputFormat := strings.ToLower(strings.TrimSpace(*format))
	if outputFormat != "human" && outputFormat != "json" && outputFormat != "scopeql" {
		return fmt.Errorf("unsupported plan format %q; choose human, json, or scopeql", *format)
	}
	if *outputPath != "" && outputFormat != "human" {
		return fmt.Errorf("--out requires --format human")
	}
	if *outputPath == "-" {
		return fmt.Errorf("--out requires a file path; use --format scopeql for stdout")
	}
	if err := applyBootstrapFlags(bootstrap); err != nil {
		return err
	}
	configPath, err := telescopeConfigPath(flags)
	if err != nil {
		return err
	}
	ingestion, err := collector.LoadConfig(configPath)
	if err != nil {
		return err
	}
	previews, err := loadMappingSamples(samples.paths, ingestion, stdin)
	if err != nil {
		return err
	}
	previewValues := make([]scopedbexporter.MappingPreview, 0, len(previews))
	for _, sample := range previews {
		previewValues = append(previewValues, sample.preview)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	plan, err := scopedbexporter.PlanIngestionTables(
		ctx,
		strings.TrimSpace(os.Getenv(telescopeScopeDBEndpointEnv)),
		strings.TrimSpace(os.Getenv(telescopeScopeDBAPIKeyEnv)),
		ingestion,
		previewValues,
	)
	if err != nil {
		return err
	}

	switch outputFormat {
	case "human":
		writtenPath := ""
		if *outputPath != "" && !plan.Blocked() {
			script, err := scopedbexporter.RenderTablePlanScopeQL(plan)
			if err != nil {
				return err
			}
			if err := writeFileAtomically(*outputPath, []byte(script)); err != nil {
				return fmt.Errorf("write ScopeQL output: %w", err)
			}
			writtenPath = *outputPath
		}
		if err := printTablePlan(stdout, plan, ingestion, writtenPath); err != nil {
			return err
		}
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(plan); err != nil {
			return fmt.Errorf("encode table plan: %w", err)
		}
	case "scopeql":
		script, err := scopedbexporter.RenderTablePlanScopeQL(plan)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(stdout, script); err != nil {
			return err
		}
	}
	if plan.Blocked() {
		return fmt.Errorf("table plan is blocked; resolve mapping types, sample errors, or catalog conflicts and rerun")
	}
	return nil
}

func printTablePlan(
	w io.Writer,
	plan scopedbexporter.IngestionTablePlan,
	ingestion scopedbexporter.IngestionConfig,
	outputPath string,
) error {
	counts := map[scopedbexporter.TablePlanAction]int{}
	for _, tablePlan := range plan.Tables {
		counts[tablePlan.Action]++
		fmt.Fprintf(w, "table %s [%s]: %s\n", tablePlan.Table, strings.Join(tablePlan.Signals, ","), tablePlan.Action)
		if tablePlan.CreateDatabase {
			fmt.Fprintln(w, "namespace: create database "+tablePlan.Database)
		}
		if tablePlan.CreateSchema {
			fmt.Fprintf(w, "namespace: create schema %s.%s\n", tablePlan.Database, tablePlan.Schema)
		}
		if !tablePlan.Exists {
			fmt.Fprintln(w, "physical policy: unspecified; review retention, clustering, distinct keys, and indexes")
		}
		table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(table, "COLUMN\tREQUIRED\tACTUAL\tOBSERVED\tSTATUS\tREASON")
		for _, column := range tablePlan.Columns {
			fmt.Fprintf(
				table,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				column.Name,
				planValue(column.RequiredType),
				planValue(column.ActualType),
				planValue(strings.Join(column.ObservedTypes, "|")),
				column.Status,
				planValue(column.Reason),
			)
		}
		if err := table.Flush(); err != nil {
			return err
		}
		for _, column := range tablePlan.Columns {
			for _, requirement := range column.Requirements {
				fmt.Fprintf(w, "  %s.%s <- %s [%s]\n", requirement.Signal, column.Name, requirement.Mapping, requirementSummary(requirement))
				if requirement.SuggestedCast == "" {
					continue
				}
				suggestion, err := mappingCastSuggestion(ingestion, requirement.Signal, column.Name, requirement.SuggestedCast)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "    suggested edit for signals.%s.mapping.%s:\n%s", requirement.Signal, column.Name, indentLines(suggestion, "      "))
			}
		}
	}
	fmt.Fprintf(
		w,
		"table plan: create=%d, alter=%d, no-op=%d, blocked=%d\n",
		counts[scopedbexporter.TableActionCreate],
		counts[scopedbexporter.TableActionAlter],
		counts[scopedbexporter.TableActionNoop],
		counts[scopedbexporter.TableActionBlocked],
	)
	switch {
	case plan.Blocked():
		fmt.Fprintln(w, "next: resolve blocked mappings or table conflicts, then rerun telescope plan")
	case outputPath != "":
		fmt.Fprintln(w, "scopeql output: "+outputPath)
		if counts[scopedbexporter.TableActionCreate]+counts[scopedbexporter.TableActionAlter] > 0 {
			fmt.Fprintln(w, "next: review it, then run scopeql run -f "+outputPath)
		} else {
			fmt.Fprintln(w, "next: run telescope validate, then start Telescope")
		}
	case counts[scopedbexporter.TableActionCreate]+counts[scopedbexporter.TableActionAlter] > 0:
		fmt.Fprintln(w, "next: rerun with --out <file>, review the script, then apply it with scopeql run -f <file>")
	default:
		fmt.Fprintln(w, "next: run telescope validate, then start Telescope")
	}
	return nil
}

func requirementSummary(requirement scopedbexporter.TableColumnRequirement) string {
	parts := []string{"output=" + requirement.OutputType}
	if requirement.Sampled {
		parts = append(parts, fmt.Sprintf("coverage=%d/%d", requirement.Present, requirement.Total))
		parts = append(parts, "observed="+planValue(strings.Join(requirement.ObservedTypes, "|")))
		if requirement.Errors > 0 {
			parts = append(parts, fmt.Sprintf("errors=%d", requirement.Errors))
		}
	}
	if len(requirement.Selections) > 0 {
		selections := make([]string, 0, len(requirement.Selections))
		for _, selection := range requirement.Selections {
			selections = append(selections, fmt.Sprintf("%s:%d", selection.Source, selection.Count))
		}
		parts = append(parts, "selected="+strings.Join(selections, ","))
	}
	return strings.Join(parts, ", ")
}

func mappingCastSuggestion(ingestion scopedbexporter.IngestionConfig, signal string, column string, cast string) (string, error) {
	signalConfig, ok := ingestion.Signal(signal)
	if !ok {
		return "", fmt.Errorf("cannot suggest mapping edit for disabled signal %q", signal)
	}
	rule, ok := signalConfig.Mapping[column]
	if !ok {
		return "", fmt.Errorf("cannot suggest mapping edit for missing column %q", column)
	}
	rule.Cast = cast
	contents, err := yaml.Marshal(scopedbexporter.MappingConfig{column: rule})
	if err != nil {
		return "", fmt.Errorf("marshal mapping suggestion: %w", err)
	}
	return string(contents), nil
}

func indentLines(value string, indent string) string {
	value = strings.TrimSuffix(value, "\n")
	return indent + strings.ReplaceAll(value, "\n", "\n"+indent) + "\n"
}

func writeFileAtomically(path string, contents []byte) (err error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".telescope-plan-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err = temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func planValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
