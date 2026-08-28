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
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/otelcol"

	"github.com/scopedb/telescope/internal/collector"
	statusapi "github.com/scopedb/telescope/internal/status"
)

func runTelescopeCommand(parent context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: telescope run [options] [telescope.yaml]")
		fmt.Fprintln(stderr, "\nRun the OTLP-to-ScopeDB data plane from the validated Telescope contract.")
		fmt.Fprintln(stderr, "\nOptions:")
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

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	fmt.Fprintf(stderr, "telescope starting: version=%s config=%s digest=%s signals=%s\n",
		version,
		configPath,
		configDigest,
		strings.Join(ingestion.EnabledSignals(), ","),
	)

	errCh := make(chan error, 2)
	var services sync.WaitGroup
	services.Add(2)
	go func() {
		defer services.Done()
		if err := otelCollector.Run(ctx); err != nil {
			errCh <- fmt.Errorf("collector: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		defer services.Done()
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http: %w", err)
			return
		}
		errCh <- nil
	}()

	readinessDone := make(chan struct{})
	go func() {
		defer close(readinessDone)
		reportReadiness(ctx, operationalServer.Ready, stderr, 200*time.Millisecond, listenAddr)
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}
	cancel()

	otelCollector.Shutdown()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown http: %w", err)
	}
	services.Wait()
	<-readinessDone
	if runErr == nil {
		fmt.Fprintln(stderr, "telescope stopped")
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

func runAdvanced(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "collector" {
		return errors.New("usage: telescope advanced collector <otelcol command>")
	}
	return runCollector(ctx, args[1:], stdout, stderr)
}

func runCollector(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	settings := collector.Settings("", version)
	command := otelcol.NewCommand(settings)
	command.SilenceErrors = true
	command.SetContext(ctx)
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs(args)
	return command.Execute()
}
