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
	"strings"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
	"github.com/scopedb/telescope/scopedbexporter"
)

func runCaptureCommand(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: telescope capture [options] <signal>")
		fmt.Fprintln(stderr, "\nCapture a bounded live OTLP sample from logs, traces, or metrics.")
		fmt.Fprintln(stderr, "Running-instance capture observes new exporter input after Collector batching; past telemetry is not replayed.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintln(stderr, "  telescope capture --listen-http 127.0.0.1:4318 traces > traces.otlp.json")
		fmt.Fprintln(stderr, "  telescope capture traces | telescope preview --offline --sample traces=- telescope.yaml")
	}
	endpoint := flags.String("endpoint", defaultOperationalEndpoint, "Telescope operational HTTP base URL")
	listenHTTP := flags.String("listen-http", "", "standalone OTLP/HTTP listen address; does not require a running Telescope")
	limit := flags.Int("limit", statusapi.DefaultCaptureLimit, "maximum records to capture")
	timeout := flags.Duration("timeout", statusapi.DefaultCaptureTimeout, "time to wait for telemetry")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("capture requires exactly one signal: logs, traces, or metrics")
	}
	signal := strings.TrimSpace(flags.Arg(0))
	if !supportedSignal(signal) {
		return fmt.Errorf("unsupported capture signal %q; choose logs, traces, or metrics", signal)
	}
	if *limit <= 0 {
		return errors.New("--limit must be greater than zero")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	endpointSet := false
	flags.Visit(func(current *flag.Flag) {
		endpointSet = endpointSet || current.Name == "endpoint"
	})
	if strings.TrimSpace(*listenHTTP) != "" && endpointSet {
		return errors.New("--listen-http and --endpoint cannot be used together")
	}

	captureCtx, cancel := context.WithTimeout(ctx, *timeout+5*time.Second)
	defer cancel()
	var payload []byte
	var records int
	var err error
	if address := strings.TrimSpace(*listenHTTP); address != "" {
		var sample scopedbexporter.CapturedSample
		sample, err = captureStandaloneHTTP(captureCtx, address, signal, *limit, *timeout, func(endpoint string) {
			fmt.Fprintf(
				stderr,
				"listening for %s OTLP/HTTP at %s/v1/%s (limit=%d, timeout=%s)\n",
				signal,
				endpoint,
				signal,
				*limit,
				*timeout,
			)
		})
		payload = sample.Payload
		records = sample.Records
	} else {
		fmt.Fprintf(
			stderr,
			"waiting for new %s after Collector batching; generate traffic now (limit=%d, timeout=%s)\n",
			signal,
			*limit,
			*timeout,
		)
		payload, records, err = requestCapture(captureCtx, &http.Client{}, *endpoint, signal, *limit, *timeout)
	}
	if err != nil {
		return err
	}
	if _, err := stdout.Write(payload); err != nil {
		return fmt.Errorf("write captured sample: %w", err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		if _, err := fmt.Fprintln(stdout); err != nil {
			return fmt.Errorf("finish captured sample: %w", err)
		}
	}
	if records > 0 {
		fmt.Fprintf(stderr, "captured %s: %d records\n", signal, records)
	} else {
		fmt.Fprintf(stderr, "captured %s\n", signal)
	}
	return nil
}
