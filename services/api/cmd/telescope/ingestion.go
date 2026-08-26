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
	"sort"
	"strings"
	"time"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/scopedb/telescope/services/api/internal/collector"
)

func runIngestion(args []string) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("usage: telescope ingestion check (--config file | --profile starter)")
	}

	flags := flag.NewFlagSet("ingestion check", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bootstrap := addBootstrapFlags(flags)
	configPath := flags.String("config", "", "Telescope tables and mappings YAML file")
	profile := flags.String("profile", "", "built-in ingestion profile (starter)")
	timeout := flags.Duration("timeout", 30*time.Second, "destination check timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if err := applyBootstrapFlags(bootstrap); err != nil {
		return err
	}

	ingestion, err := resolveIngestionConfig(
		*configPath,
		flagProvided(flags, "config"),
		*profile,
		flagProvided(flags, "profile"),
	)
	if err != nil {
		return err
	}
	printIngestionPlan(os.Stdout, ingestion)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := scopedbexporter.CheckIngestionDestinations(
		ctx,
		strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_ENDPOINT")),
		strings.TrimSpace(os.Getenv("TELESCOPE_SCOPEDB_API_KEY")),
		ingestion,
	); err != nil {
		return fmt.Errorf("ingestion check failed: %w", err)
	}
	fmt.Fprintln(os.Stdout, "destination check: ok")
	return nil
}

func resolveDaemonConfig(
	collectorValue string,
	collectorIsSet bool,
	ingestionValue string,
	ingestionIsSet bool,
	profileValue string,
	profileIsSet bool,
) (string, error) {
	var collectorConfig, ingestionPath, profile string
	if collectorIsSet || ingestionIsSet || profileIsSet {
		collectorConfig = strings.TrimSpace(collectorValue)
		ingestionPath = strings.TrimSpace(ingestionValue)
		profile = strings.TrimSpace(profileValue)
	} else {
		collectorConfig = strings.TrimSpace(os.Getenv("TELESCOPE_COLLECTOR_CONFIG"))
		ingestionPath = strings.TrimSpace(os.Getenv("TELESCOPE_INGESTION_CONFIG"))
		profile = strings.TrimSpace(os.Getenv("TELESCOPE_INGESTION_PROFILE"))
	}

	if collectorConfig != "" {
		if ingestionPath != "" || profile != "" {
			return "", errors.New("collector config cannot be combined with an ingestion config or profile")
		}
		return collectorConfig, nil
	}

	ingestion, err := resolveIngestionConfig(ingestionPath, true, profile, true)
	if err != nil {
		return "", err
	}
	return collector.ConfigURIForIngestion(ingestion)
}

func resolveIngestionConfig(
	configValue string,
	configIsSet bool,
	profileValue string,
	profileIsSet bool,
) (scopedbexporter.IngestionConfig, error) {
	var configPath, profile string
	if configIsSet || profileIsSet {
		configPath = strings.TrimSpace(configValue)
		profile = strings.TrimSpace(profileValue)
	} else {
		configPath = strings.TrimSpace(os.Getenv("TELESCOPE_INGESTION_CONFIG"))
		profile = strings.TrimSpace(os.Getenv("TELESCOPE_INGESTION_PROFILE"))
	}
	if configPath != "" && profile != "" {
		return scopedbexporter.IngestionConfig{}, errors.New("choose one ingestion config or profile, not both")
	}
	if configPath != "" {
		return collector.LoadIngestionConfig(configPath)
	}
	if profile == "" {
		return scopedbexporter.IngestionConfig{}, errors.New("choose --ingestion-config or --ingestion-profile starter")
	}
	if profile != "starter" {
		return scopedbexporter.IngestionConfig{}, fmt.Errorf("unknown ingestion profile %q; supported profile: starter", profile)
	}
	return scopedbexporter.StarterIngestionConfig(), nil
}

func printIngestionPlan(w io.Writer, config scopedbexporter.IngestionConfig) {
	for _, signal := range []struct {
		name    string
		table   string
		mapping map[string]string
	}{
		{name: "logs", table: config.Tables.Logs, mapping: config.Mappings.Logs},
		{name: "traces", table: config.Tables.Traces, mapping: config.Mappings.Traces},
		{name: "metrics", table: config.Tables.Metrics, mapping: config.Mappings.Metrics},
	} {
		fmt.Fprintf(w, "%s -> %s\n", signal.name, signal.table)
		columns := make([]string, 0, len(signal.mapping))
		for column := range signal.mapping {
			columns = append(columns, column)
		}
		sort.Strings(columns)
		for _, column := range columns {
			fmt.Fprintf(w, "  %s <- %s\n", column, signal.mapping[column])
		}
	}
}
