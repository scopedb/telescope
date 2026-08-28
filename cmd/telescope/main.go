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

const (
	telescopeScopeDBEndpointEnv = "TELESCOPE_SCOPEDB_ENDPOINT"
	telescopeScopeDBAPIKeyEnv   = "TELESCOPE_SCOPEDB_API_KEY"
	sharedScopeDBEndpointEnv    = "SCOPEDB_ENDPOINT"
	sharedScopeDBAPIKeyEnv      = "SCOPEDB_API_KEY"
)

func init() {
	if version != "(unknown)" {
		return
	}
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
		printUsage(os.Stderr)
		return nil
	}

	switch args[0] {
	case "run":
		return runTelescope(args[1:])
	case "validate":
		return runValidate(args[1:])
	case "preview":
		return runPreview(args[1:])
	case "inspect":
		return runInspect(args[1:])
	case "plan":
		return runPlan(args[1:])
	case "capture":
		return runCapture(args[1:])
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
		printUsage(os.Stderr)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runTelescope(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: telescope run [options] [telescope.yaml]")
		fmt.Fprintln(os.Stderr, "\nRun the OTLP-to-ScopeDB data plane from the validated Telescope contract.")
		fmt.Fprintln(os.Stderr, "\nOptions:")
		flags.PrintDefaults()
	}
	bootstrap := addBootstrapFlags(flags)
	httpAddr := flags.String("http-addr", "", "operational HTTP listen address; overrides TELESCOPE_HTTP_ADDR")
	if err := flags.Parse(args); err != nil {
		return err
	}
	configPath, ingestion, err := loadTelescopeConfig(flags, bootstrap)
	if err != nil {
		return err
	}
	collectorConfigURI, err := collector.ConfigURI(ingestion)
	if err != nil {
		return fmt.Errorf("render Telescope config: %w", err)
	}
	configDigest, err := ingestion.ContractDigest()
	if err != nil {
		return fmt.Errorf("identify Telescope config: %w", err)
	}
	listenAddr := resolveHTTPListenAddr(*httpAddr)

	otelCollector, err := collector.New(collectorConfigURI, version)
	if err != nil {
		return fmt.Errorf("build collector: %w", err)
	}
	operationalServer := statusapi.New(version, configDigest)
	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: operationalServer,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "telescope starting: version=%s config=%s digest=%s signals=%s\n",
		version,
		configPath,
		configDigest,
		strings.Join(ingestion.EnabledSignals(), ","),
	)

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

	readinessDone := make(chan struct{})
	go func() {
		defer close(readinessDone)
		reportReadiness(
			ctx,
			operationalServer.Ready,
			os.Stderr,
			200*time.Millisecond,
			listenAddr,
		)
	}()

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
	<-readinessDone
	if runErr == nil {
		fmt.Fprintln(os.Stderr, "telescope stopped")
	}

	return runErr
}

func reportReadiness(
	ctx context.Context,
	ready func(context.Context) bool,
	w io.Writer,
	interval time.Duration,
	httpAddr string,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if ready(ctx) {
			fmt.Fprintf(
				w,
				"telescope ready: otlp_grpc=%s otlp_http=%s http=%s\n",
				os.Getenv("TELESCOPE_OTLP_GRPC_ADDR"),
				os.Getenv("TELESCOPE_OTLP_HTTP_ADDR"),
				httpAddr,
			)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
		scopedbEndpoint: flags.String("scopedb-endpoint", "", "ScopeDB physical region endpoint; overrides Telescope and shared ScopeDB environment"),
		scopedbAPIKey:   flags.String("scopedb-api-key", "", "ScopeDB API key; overrides Telescope and shared ScopeDB environment"),
	}
}

func applyBootstrapFlags(flags bootstrapFlags) error {
	if flags.envFile != nil && strings.TrimSpace(*flags.envFile) != "" {
		if err := loadEnvFile(*flags.envFile); err != nil {
			return err
		}
	}
	if err := setEnvFallback(telescopeScopeDBEndpointEnv, sharedScopeDBEndpointEnv); err != nil {
		return err
	}
	if err := setEnvFallback(telescopeScopeDBAPIKeyEnv, sharedScopeDBAPIKeyEnv); err != nil {
		return err
	}
	if err := setEnvIfValue(telescopeScopeDBEndpointEnv, valueOf(flags.scopedbEndpoint)); err != nil {
		return err
	}
	if err := setEnvIfValue(telescopeScopeDBAPIKeyEnv, valueOf(flags.scopedbAPIKey)); err != nil {
		return err
	}
	return nil
}

func setEnvFallback(target string, fallback string) error {
	if strings.TrimSpace(os.Getenv(target)) != "" {
		return nil
	}
	return setEnvIfValue(target, os.Getenv(fallback))
}

func scopeDBCredentials() (string, string) {
	endpoint := strings.TrimSpace(os.Getenv(telescopeScopeDBEndpointEnv))
	apiKey := strings.TrimSpace(os.Getenv(telescopeScopeDBAPIKeyEnv))
	return endpoint, apiKey
}

func supportedSignal(signal string) bool {
	return signal == "logs" || signal == "traces" || signal == "metrics"
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

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Telescope

Usage:
  Setup:
    telescope inspect [options] <signal>           Discover mapping selectors in an OTLP sample
    telescope preview [options] [telescope.yaml]   Preview sample projection without appending
    telescope plan [options] [telescope.yaml]      Plan additive ScopeDB table DDL
    telescope validate [options] [telescope.yaml]  Validate config and destination tables
    telescope run [options] [telescope.yaml]       Run the OTLP-to-ScopeDB data plane

  Operations:
    telescope status [options]                     Report local delivery state

  Diagnostics:
    telescope capture [options] <signal>           Capture OTLP samples for mapping preview
    telescope verify [options] [signals...]        Verify synthetic OTLP-to-append delivery

  Other:
    telescope version                              Print the build version

Connection options:
  --env-file                 Load KEY=VALUE bootstrap config file
  --scopedb-endpoint         ScopeDB physical region endpoint
  --scopedb-api-key          ScopeDB API key

Validate options:
  --offline                  Skip ScopeDB destination checks

Preview options:
  --offline                  Skip ScopeDB destination checks
  --sample signal=path       Preview OTLP JSON or protobuf; repeat per signal
  --strict                   Fail on unobserved, partial, or default-only columns

Inspect options:
  --sample path              Read one OTLP JSON or protobuf sample; use - for stdin
  --format                   Output human or json, default human

Plan options:
  --sample signal=path       Add representative mapping evidence; repeat per signal
  --format                   Output human, json, or scopeql, default human
  --out                      Write ScopeQL while retaining the human plan on stdout

Capture options:
  --endpoint                 Telescope operational HTTP base URL
  --listen-http              Standalone OTLP/HTTP address; no config or ScopeDB required
  --limit                    Maximum records to capture, default 100
  --timeout                  Time to wait for telemetry, default 45s

Run options:
  --http-addr                Operational HTTP listen address, overrides TELESCOPE_HTTP_ADDR

Environment:
  TELESCOPE_SCOPEDB_ENDPOINT   ScopeDB physical region endpoint
  TELESCOPE_SCOPEDB_API_KEY    ScopeDB API key
  SCOPEDB_ENDPOINT             Shared fallback for TELESCOPE_SCOPEDB_ENDPOINT
  SCOPEDB_API_KEY              Shared fallback for TELESCOPE_SCOPEDB_API_KEY
  TELESCOPE_HTTP_ADDR          Operational HTTP listen address, default :8080
  TELESCOPE_OTLP_GRPC_ADDR     OTLP gRPC listen address, default 0.0.0.0:4317
  TELESCOPE_OTLP_HTTP_ADDR     OTLP HTTP listen address, default 0.0.0.0:4318
  TELESCOPE_QUEUE_DIR          Persistent queue directory, default $HOME/.telescope/queue
  TELESCOPE_QUEUE_MAX_BYTES    Logical queued telemetry byte capacity, default 536870912 (512 MiB)

Advanced:
  telescope advanced collector <otelcol command>
`)
}
