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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/scopedb/telescope/internal/collector"
	"github.com/scopedb/telescope/packages/scopedbexporter"
)

func runPlan(args []string) error {
	return runPlanCommand(args, os.Stdin, os.Stdout, os.Stderr)
}

func runPlanCommand(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	bootstrap := addBootstrapFlags(flags)
	format := flags.String("format", "human", "output format: human, json, or scopeql")
	timeout := flags.Duration("timeout", 30*time.Second, "ScopeDB catalog timeout")
	var samples sampleFlags
	flags.Var(&samples, "sample", "representative OTLP JSON or protobuf as signal=path; use - for stdin; repeat per signal")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: telescope plan [options] [telescope.yaml]")
		fmt.Fprintln(stderr, "\nCompare the mapping contract with live ScopeDB tables. The command never applies DDL.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nExample:")
		fmt.Fprintln(stderr, "  telescope plan --sample traces=traces.otlp.json --format scopeql telescope.yaml > tables.scopeql")
		fmt.Fprintln(stderr, "  scopeql run -f tables.scopeql")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	outputFormat := strings.ToLower(strings.TrimSpace(*format))
	if outputFormat != "human" && outputFormat != "json" && outputFormat != "scopeql" {
		return fmt.Errorf("unsupported plan format %q; choose human, json, or scopeql", *format)
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
		strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT")),
		strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_API_KEY")),
		ingestion,
		previewValues,
	)
	if err != nil {
		return err
	}

	switch outputFormat {
	case "human":
		if err := printTablePlan(stdout, plan); err != nil {
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

func printTablePlan(w io.Writer, plan scopedbexporter.IngestionTablePlan) error {
	counts := map[scopedbexporter.TablePlanAction]int{}
	for _, tablePlan := range plan.Tables {
		counts[tablePlan.Action]++
		fmt.Fprintf(w, "table %s [%s]: %s\n", tablePlan.Table, strings.Join(tablePlan.Signals, ","), tablePlan.Action)
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
	case counts[scopedbexporter.TableActionCreate]+counts[scopedbexporter.TableActionAlter] > 0:
		fmt.Fprintln(w, "next: rerun with --format scopeql, review the script, then apply it with scopeql run -f <file>")
	default:
		fmt.Fprintln(w, "next: run telescope validate, then start Telescope")
	}
	return nil
}

func planValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
