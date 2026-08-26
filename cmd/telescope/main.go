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

	"github.com/scopedb/telescope/internal/collector"
	statusapi "github.com/scopedb/telescope/internal/status"
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
		if errors.Is(err, flag.ErrHelp) {
			return
		}
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
	case "run":
		return runTelescope(args[1:])
	case "validate":
		return runValidate(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "status":
		return runStatus(args[1:])
	case "advanced":
		return runAdvanced(args[1:])
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

func runTelescope(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bootstrap := addBootstrapFlags(flags)
	httpAddr := flags.String("http-addr", "", "operational HTTP listen address; overrides TELESCOPE_HTTP_ADDR")
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
	collectorConfigURI, err := collector.ConfigURI(ingestion)
	if err != nil {
		return fmt.Errorf("render Telescope config: %w", err)
	}
	listenAddr := resolveHTTPListenAddr(*httpAddr)

	otelCollector, err := collector.New(collectorConfigURI, version)
	if err != nil {
		return fmt.Errorf("build collector: %w", err)
	}
	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: statusapi.New(version),
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
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http: %w", err)
			return
		}
		errCh <- nil
	}()

	fmt.Fprintf(os.Stderr, "telescope starting: config=%s http=%s otlp_grpc=%s otlp_http=%s\n",
		configPath,
		listenAddr,
		os.Getenv("TELESCOPE_OTLP_GRPC_ADDR"),
		os.Getenv("TELESCOPE_OTLP_HTTP_ADDR"),
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

func runAdvanced(args []string) error {
	if len(args) == 0 || args[0] != "collector" {
		return errors.New("usage: telescope advanced collector <otelcol command>")
	}
	return runCollector(args[1:])
}

func runCollector(args []string) error {
	settings := collector.Settings("", version)
	command := otelcol.NewCommand(settings)
	command.SilenceErrors = true
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

func resolveHTTPListenAddr(flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("TELESCOPE_HTTP_ADDR")); value != "" {
		return value
	}
	return ":8080"
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Telescope

Usage:
  telescope validate [options] [telescope.yaml]
  telescope run [options] [telescope.yaml]
  telescope verify [options] [logs | traces | metrics ...]
  telescope status [options]
  telescope version

Connection options:
  --env-file                 Load KEY=VALUE bootstrap config file
  --scopedb-endpoint         ScopeDB physical region endpoint
  --scopedb-api-key          ScopeDB API key

Run options:
  --http-addr                Operational HTTP listen address, overrides TELESCOPE_HTTP_ADDR

Environment:
  TELESCOPE_SCOPEDB_ENDPOINT   ScopeDB physical region endpoint
  TELESCOPE_SCOPEDB_API_KEY    ScopeDB API key
  TELESCOPE_HTTP_ADDR          Operational HTTP listen address, default :8080
  TELESCOPE_OTLP_GRPC_ADDR     OTLP gRPC listen address, default 0.0.0.0:4317
  TELESCOPE_OTLP_HTTP_ADDR     OTLP HTTP listen address, default 0.0.0.0:4318
  TELESCOPE_QUEUE_DIR          Persistent queue directory, default $HOME/.telescope/queue
  TELESCOPE_QUEUE_MAX_BYTES    Logical queued telemetry byte capacity, default 536870912 (512 MiB)

Advanced:
  telescope advanced collector <otelcol command>
`)
}
