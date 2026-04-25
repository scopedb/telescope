/*
 * Copyright 2026 ScopeDB contributors
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
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/collector/otelcol"

	"github.com/scopedb/telescope/services/api/internal/appruntime"
	"github.com/scopedb/telescope/services/api/internal/collector"
)

var version = "dev"

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
	collectorConfig := flags.String("collector-config", strings.TrimSpace(os.Getenv("TELESCOPE_COLLECTOR_CONFIG")), "collector config URI or file path")
	httpAddr := flags.String("http-addr", "", "HTTP API listen address; overrides TELESCOPE_HTTP_ADDR")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *httpAddr != "" {
		_ = os.Setenv("TELESCOPE_HTTP_ADDR", *httpAddr)
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

	otelCollector, err := collector.New(*collectorConfig, version)
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
	if err := flags.Parse(args); err != nil {
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
  telescope daemon [--collector-config file] [--http-addr addr]
  telescope mcp
  telescope collector <otelcol command>
  telescope version

Environment:
  TELESCOPE_SCOPEDB_ENDPOINT   ScopeDB physical region endpoint
  TELESCOPE_SCOPEDB_API_KEY    ScopeDB API key
  TELESCOPE_ENV                Telemetry environment label, default "default"
  TELESCOPE_HTTP_ADDR          HTTP API listen address, default :8080
  TELESCOPE_OTLP_GRPC_ADDR     OTLP gRPC listen address, default 0.0.0.0:4317
  TELESCOPE_OTLP_HTTP_ADDR     OTLP HTTP listen address, default 0.0.0.0:4318
  TELESCOPE_HEALTH_ADDR        Collector health listen address, default 0.0.0.0:13133
  TELESCOPE_QUEUE_DIR          Persistent queue directory, default $HOME/.telescope/queue
  TELESCOPE_QUERY_TIMEOUT      ScopeDB query timeout, default 15s
  TELESCOPE_COLLECTOR_CONFIG   Collector config URI or file path, default embedded config
`)
}
