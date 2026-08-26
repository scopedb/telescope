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
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/collector/otelcol"

	"github.com/scopedb/telescope/services/api/internal/appruntime"
	"github.com/scopedb/telescope/services/api/internal/collector"
)

var version = "(unknown)"

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			version = info.Main.Version
		}
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "telescope: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "daemon":
		return runDaemon(args[1:])
	case "mcp":
		return runMCP(args[1:])
	case "collector":
		return runCollector(args[1:])
	case "ingestion":
		return runIngestion(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runDaemon(args []string) error {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bootstrap := addBootstrapFlags(flags)
	collectorConfig := flags.String("collector-config", "", "collector config URI or file path")
	ingestionConfig := flags.String("ingestion-config", "", "Telescope tables and mappings YAML file")
	ingestionProfile := flags.String("ingestion-profile", "", "built-in ingestion profile (starter)")
	httpAddr := flags.String("http-addr", "", "HTTP API listen address; overrides TELESCOPE_HTTP_ADDR")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := applyBootstrapFlags(bootstrap); err != nil {
		return err
	}
	if *httpAddr != "" {
		if err := os.Setenv("TELESCOPE_HTTP_ADDR", *httpAddr); err != nil {
			return fmt.Errorf("set TELESCOPE_HTTP_ADDR: %w", err)
		}
	}
	collectorConfigURI, err := resolveDaemonConfig(
		*collectorConfig,
		flagProvided(flags, "collector-config"),
		*ingestionConfig,
		flagProvided(flags, "ingestion-config"),
		*ingestionProfile,
		flagProvided(flags, "ingestion-profile"),
	)
	if err != nil {
		return err
	}

	config, err := appruntime.LoadConfigFromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := appruntime.New(config, version)
	if err != nil {
		return err
	}
	defer app.Close()

	httpServer, err := app.HTTPServer(version)
	if err != nil {
		return err
	}

	otelCollector, err := collector.New(collectorConfigURI, version)
	if err != nil {
		return fmt.Errorf("build collector: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() {
		if err := otelCollector.Run(ctx); err != nil {
			errCh <- fmt.Errorf("collector: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		if err := httpServer.Start(config.ListenAddr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http: %w", err)
			return
		}
		errCh <- nil
	}()

	fmt.Fprintf(os.Stderr, "telescope daemon started: http=%s otlp_grpc=%s otlp_http=%s health=%s\n",
		config.ListenAddr,
		os.Getenv("TELESCOPE_OTLP_GRPC_ADDR"),
		os.Getenv("TELESCOPE_OTLP_HTTP_ADDR"),
		os.Getenv("TELESCOPE_HEALTH_ADDR"),
	)

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		runErr = err
		stop()
	}

	otelCollector.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown http: %w", err)
	}

	return runErr
}

func runMCP(args []string) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bootstrap := addBootstrapFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := applyBootstrapFlags(bootstrap); err != nil {
		return err
	}

	config, err := appruntime.LoadConfigFromEnv()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := appruntime.New(config, version)
	if err != nil {
		return err
	}
	defer app.Close()

	server, err := app.MCPServer(version)
	if err != nil {
		return err
	}
	return server.Serve(context.Background(), os.Stdin, os.Stdout)
}

func runCollector(args []string) error {
	settings := collector.Settings("", version)
	if collectorArgsIncludeConfig(args) {
		settings.ConfigProviderSettings.ResolverSettings.URIs = nil
	}
	command := otelcol.NewCommand(settings)
	command.SetArgs(args)
	return command.Execute()
}

type bootstrapFlags struct {
	envFile         *string
	scopedbEndpoint *string
	scopedbAPIKey   *string
}

func addBootstrapFlags(flags *flag.FlagSet) bootstrapFlags {
	return bootstrapFlags{
		envFile:         flags.String("env-file", "", "load bootstrap environment variables from a KEY=VALUE file"),
		scopedbEndpoint: flags.String("scopedb-endpoint", "", "ScopeDB physical region endpoint; overrides TELESCOPE_SCOPEDB_ENDPOINT"),
		scopedbAPIKey:   flags.String("scopedb-api-key", "", "ScopeDB API key; overrides TELESCOPE_SCOPEDB_API_KEY"),
	}
}

func applyBootstrapFlags(flags bootstrapFlags) error {
	if flags.envFile != nil && strings.TrimSpace(*flags.envFile) != "" {
		if err := loadEnvFile(*flags.envFile); err != nil {
			return err
		}
	}
	if err := setEnvIfValue("TELESCOPE_SCOPEDB_ENDPOINT", valueOf(flags.scopedbEndpoint)); err != nil {
		return err
	}
	if err := setEnvIfValue("TELESCOPE_SCOPEDB_API_KEY", valueOf(flags.scopedbAPIKey)); err != nil {
		return err
	}
	return nil
}

func flagProvided(flags *flag.FlagSet, name string) bool {
	provided := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == name {
			provided = true
		}
	})
	return provided
}

func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("load env file %s: %w", path, err)
	}
	for index, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("load env file %s: line %d is not KEY=VALUE", path, index+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("load env file %s: line %d has an empty key", path, index+1)
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if strings.TrimSpace(os.Getenv(key)) == "" {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("load env file %s: line %d: set %q: %w", path, index+1, key, err)
			}
		}
	}
	return nil
}

func setEnvIfValue(key string, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if err := os.Setenv(key, value); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}
	return nil
}

func valueOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func collectorArgsIncludeConfig(args []string) bool {
	for _, arg := range args {
		if arg == "--config" || arg == "-c" || strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "-c=") {
			return true
		}
	}
	return false
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Telescope

Usage:
  telescope daemon [--env-file file] [--scopedb-endpoint url] [--scopedb-api-key key]
  telescope mcp [--env-file file] [--scopedb-endpoint url] [--scopedb-api-key key]
  telescope ingestion check (--config file | --profile starter)
  telescope ingestion test --signal (logs | traces | metrics)
  telescope collector <otelcol command>
  telescope version

Bootstrap:
  --env-file                 Load KEY=VALUE bootstrap config file
  --scopedb-endpoint         ScopeDB physical region endpoint
  --scopedb-api-key          ScopeDB API key

Daemon options:
  --ingestion-config         Tables and mappings YAML for the embedded Collector
  --ingestion-profile        Explicit built-in profile; currently starter
  --collector-config         Full Collector config URI or file path
  --http-addr                HTTP API listen address, overrides TELESCOPE_HTTP_ADDR

Environment:
  TELESCOPE_SCOPEDB_ENDPOINT   ScopeDB physical region endpoint
  TELESCOPE_SCOPEDB_API_KEY    ScopeDB API key
  TELESCOPE_HTTP_ADDR          HTTP API listen address, default :8080
  TELESCOPE_OTLP_GRPC_ADDR     OTLP gRPC listen address, default 0.0.0.0:4317
  TELESCOPE_OTLP_HTTP_ADDR     OTLP HTTP listen address, default 0.0.0.0:4318
  TELESCOPE_HEALTH_ADDR        Collector health listen address, default 0.0.0.0:13133
  TELESCOPE_QUEUE_DIR          Persistent queue directory, default $HOME/.telescope/queue
  TELESCOPE_QUEUE_MAX_BYTES    Logical queued telemetry byte capacity, default 536870912 (512 MiB)
  TELESCOPE_OTEL_BATCH_TIMEOUT Embedded Collector batch timeout, default 30s
  TELESCOPE_OTEL_BATCH_SIZE    Embedded Collector send batch size, default 2000
  TELESCOPE_OTEL_BATCH_MAX_SIZE Embedded Collector send batch max size, default 2000
  TELESCOPE_INTERNAL_METRICS_URL Collector metrics URL for ingestion status, default http://127.0.0.1:8888/metrics
  TELESCOPE_QUERY_TIMEOUT      ScopeDB query timeout, default 15s
  TELESCOPE_INGESTION_CONFIG   Tables and mappings YAML file
  TELESCOPE_INGESTION_PROFILE  Built-in ingestion profile; currently starter
  TELESCOPE_COLLECTOR_CONFIG   Full Collector config URI or file path
`)
}
