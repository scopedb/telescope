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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/scopedb/telescope/internal/collector"
	"github.com/scopedb/telescope/packages/scopedbexporter"
)

const defaultTelescopeConfigPath = "telescope.yaml"

func runValidate(args []string) error {
	return runConfigCommand("validate", args, os.Stdin, os.Stdout, os.Stderr)
}

func runPreview(args []string) error {
	return runConfigCommand("preview", args, os.Stdin, os.Stdout, os.Stderr)
}

func runConfigCommand(command string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	preview := command == "preview"
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	bootstrap := addBootstrapFlags(flags)
	offline := flags.Bool("offline", false, "validate configuration without connecting to ScopeDB")
	timeout := flags.Duration("timeout", 30*time.Second, "destination validation timeout")
	var samples sampleFlags
	strict := false
	if preview {
		flags.Var(&samples, "sample", "OTLP JSON or protobuf as signal=path; use - for stdin; repeat per signal")
		flags.BoolVar(&strict, "strict", false, "fail when the sample leaves a column unobserved, partial, or default-only")
	}
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: telescope %s [options] [telescope.yaml]\n\nOptions:\n", command)
		flags.PrintDefaults()
		if preview {
			fmt.Fprintln(stderr, "\nExample:")
			fmt.Fprintln(stderr, "  telescope capture logs | telescope preview --offline --sample logs=- telescope.yaml")
		}
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if preview && len(samples.paths) == 0 {
		return errors.New("preview requires at least one --sample signal=path; capture live input with: telescope capture logs | telescope preview --offline --sample logs=- telescope.yaml")
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
	if _, err := collector.ConfigURI(ingestion); err != nil {
		return fmt.Errorf("render Telescope config: %w", err)
	}
	descriptions, err := scopedbexporter.DescribeIngestionMappings(ingestion)
	if err != nil {
		return err
	}
	previews, err := loadMappingSamples(samples.paths, ingestion, stdin)
	if err != nil {
		return err
	}

	printConfigPlan(stdout, descriptions)
	printConfigurationSummary(stdout, descriptions)

	var destinations []scopedbexporter.SignalDestinationValidation
	var validationErrors []error
	if *offline {
		fmt.Fprintln(stdout, "destination: skipped (--offline)")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		destinations, err = scopedbexporter.InspectIngestionDestinations(
			ctx,
			strings.TrimSpace(os.Getenv(telescopeScopeDBEndpointEnv)),
			strings.TrimSpace(os.Getenv(telescopeScopeDBAPIKeyEnv)),
			ingestion,
		)
		if err != nil {
			fmt.Fprintln(stdout, "destination: check failed")
			validationErrors = append(validationErrors, fmt.Errorf("destination validation failed: %w", err))
		} else {
			printDestinationSummary(stdout, destinations)
		}
	}

	for _, sample := range previews {
		if err := printMappingPreview(stdout, sample, destinations, strict); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}
	return errors.Join(validationErrors...)
}

func telescopeConfigPath(flags *flag.FlagSet) (string, error) {
	switch flags.NArg() {
	case 0:
		return defaultTelescopeConfigPath, nil
	case 1:
		return strings.TrimSpace(flags.Arg(0)), nil
	default:
		return "", fmt.Errorf("expected one Telescope config path, got %d", flags.NArg())
	}
}

func printConfigPlan(w io.Writer, descriptions []scopedbexporter.SignalMappingDescription) {
	for _, description := range descriptions {
		fmt.Fprintf(w, "%s -> %s\n", description.Signal, description.Table)
		for _, column := range description.Columns {
			outputType := column.OutputType
			if column.RuntimeDependent {
				outputType += ", sample-check"
			}
			fmt.Fprintf(w, "  %s <- %s [%s]\n", column.Column, column.Source, outputType)
		}
	}
}

func printConfigurationSummary(w io.Writer, descriptions []scopedbexporter.SignalMappingDescription) {
	total, runtimeDependent := mappingCounts(descriptions)
	fmt.Fprintf(w, "configuration: ok (columns=%d, statically-typed=%d, sample-check=%d)\n", total, total-runtimeDependent, runtimeDependent)
}

func printDestinationSummary(w io.Writer, validations []scopedbexporter.SignalDestinationValidation) {
	total := 0
	runtimeDependent := 0
	for _, validation := range validations {
		for _, column := range validation.Columns {
			total++
			if column.Compatibility == scopedbexporter.MappingRuntimeDependent {
				runtimeDependent++
			}
		}
	}
	fmt.Fprintf(w, "destination: ok (columns=%d, catalog-checked=%d, sample-check=%d)\n", total, total-runtimeDependent, runtimeDependent)
}

func mappingCounts(descriptions []scopedbexporter.SignalMappingDescription) (int, int) {
	total := 0
	runtimeDependent := 0
	for _, description := range descriptions {
		for _, column := range description.Columns {
			total++
			if column.RuntimeDependent {
				runtimeDependent++
			}
		}
	}
	return total, runtimeDependent
}
