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
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

func runInspect(args []string) error {
	return runInspectCommand(args, os.Stdin, os.Stdout, os.Stderr)
}

func runInspectCommand(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	samplePath := flags.String("sample", "", "OTLP JSON or protobuf path; use - for stdin")
	format := flags.String("format", "human", "output format: human or json")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: telescope inspect [options] <signal>")
		fmt.Fprintln(stderr, "\nDiscover mapping selectors and observed types in an OTLP sample.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintln(stderr, "  telescope inspect traces --sample traces.otlp.json")
		fmt.Fprintln(stderr, "  telescope capture --listen-http :4318 traces | telescope inspect traces --sample -")
	}

	positionalSignal := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positionalSignal = strings.TrimSpace(args[0])
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if positionalSignal == "" {
		if flags.NArg() != 1 {
			return errors.New("inspect requires exactly one signal: logs, traces, or metrics")
		}
		positionalSignal = strings.TrimSpace(flags.Arg(0))
	} else if flags.NArg() != 0 {
		return errors.New("inspect requires exactly one signal: logs, traces, or metrics")
	}
	if !supportedSignal(positionalSignal) {
		return fmt.Errorf("unsupported inspect signal %q; choose logs, traces, or metrics", positionalSignal)
	}

	path := strings.TrimSpace(*samplePath)
	if path == "" {
		return errors.New("inspect requires --sample <path>; use --sample - for stdin")
	}
	outputFormat := strings.ToLower(strings.TrimSpace(*format))
	if outputFormat != "human" && outputFormat != "json" {
		return fmt.Errorf("unsupported inspect format %q; choose human or json", *format)
	}

	sample, err := readSample(path, stdin)
	if err != nil {
		return fmt.Errorf("read %s sample %s: %w", positionalSignal, path, err)
	}
	inspection, err := scopedbexporter.InspectSample(positionalSignal, sample)
	if err != nil {
		return fmt.Errorf("inspect %s sample %s: %w", positionalSignal, path, err)
	}

	if outputFormat == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(inspection); err != nil {
			return fmt.Errorf("encode sample inspection: %w", err)
		}
		return nil
	}
	return printSampleInspection(stdout, path, inspection)
}

func printSampleInspection(w io.Writer, path string, inspection scopedbexporter.SampleInspection) error {
	fmt.Fprintf(
		w,
		"sample %s <- %s (%d records, %d selectors)\n",
		inspection.Signal,
		path,
		inspection.Records,
		len(inspection.Fields),
	)
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "GROUP\tSELECTOR\tOBSERVED\tPOPULATED\tSTATUS")
	partial := 0
	mixed := 0
	for _, field := range inspection.Fields {
		status := "OK"
		isPartial := field.Populated < field.Total
		isMixed := len(field.ObservedTypes) > 1
		switch {
		case isPartial && isMixed:
			status = "MIXED, PARTIAL"
		case isPartial:
			status = "PARTIAL"
		case isMixed:
			status = "MIXED"
		}
		if isPartial {
			partial++
		}
		if isMixed {
			mixed++
		}
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\n",
			field.Group,
			field.Selector,
			strings.Join(field.ObservedTypes, "|"),
			formatCoverage(field.Populated, field.Total),
			status,
		)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "inspection result: selectors=%d, partial=%d, mixed=%d\n", len(inspection.Fields), partial, mixed)
	fmt.Fprintf(
		w,
		"next: copy selected sources into signals.%s.mapping, then run telescope preview with this sample\n",
		inspection.Signal,
	)
	return nil
}
