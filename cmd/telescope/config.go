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
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/scopedb/telescope/internal/collector"
	"github.com/scopedb/telescope/packages/scopedbexporter"
)

const defaultTelescopeConfigPath = "telescope.yaml"

func runValidate(args []string) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bootstrap := addBootstrapFlags(flags)
	offline := flags.Bool("offline", false, "validate configuration without connecting to ScopeDB")
	timeout := flags.Duration("timeout", 30*time.Second, "destination validation timeout")
	if err := flags.Parse(args); err != nil {
		return err
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

	printConfigPlan(os.Stdout, ingestion)
	fmt.Fprintln(os.Stdout, "configuration: ok")
	if *offline {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := scopedbexporter.CheckIngestionDestinations(
		ctx,
		strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT")),
		strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_API_KEY")),
		ingestion,
	); err != nil {
		return fmt.Errorf("destination validation failed: %w", err)
	}
	fmt.Fprintln(os.Stdout, "destination: ok")
	return nil
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

func printConfigPlan(w io.Writer, config scopedbexporter.IngestionConfig) {
	for _, signal := range config.EnabledSignals() {
		signalConfig, _ := config.Signal(signal)
		fmt.Fprintf(w, "%s -> %s\n", signal, signalConfig.Table)
		columns := make([]string, 0, len(signalConfig.Mapping))
		for column := range signalConfig.Mapping {
			columns = append(columns, column)
		}
		sort.Strings(columns)
		for _, column := range columns {
			fmt.Fprintf(w, "  %s <- %s\n", column, signalConfig.Mapping[column])
		}
	}
}
