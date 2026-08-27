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
	"net/url"
	"os"
	osSignal "os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
	"github.com/scopedb/telescope/packages/scopedbexporter"
)

const defaultCaptureEndpoint = "http://127.0.0.1:8080"

func runCapture(args []string) error {
	ctx, stop := osSignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCaptureWithContextAndWriters(ctx, args, os.Stdout, os.Stderr)
}

func runCaptureWithWriters(args []string, stdout io.Writer, stderr io.Writer) error {
	return runCaptureWithContextAndWriters(context.Background(), args, stdout, stderr)
}

func runCaptureWithContextAndWriters(
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
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintln(stderr, "  telescope capture --listen-http 127.0.0.1:4318 traces > traces.otlp.json")
		fmt.Fprintln(stderr, "  telescope capture traces | telescope preview --offline --sample traces=- telescope.yaml")
	}
	endpoint := flags.String("endpoint", defaultCaptureEndpoint, "Telescope operational HTTP base URL")
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

	ctx, cancel := context.WithTimeout(ctx, *timeout+5*time.Second)
	defer cancel()
	var payload []byte
	var records int
	var err error
	if address := strings.TrimSpace(*listenHTTP); address != "" {
		var sample scopedbexporter.CapturedSample
		sample, err = captureStandaloneHTTP(ctx, address, signal, *limit, *timeout, func(endpoint string) {
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
		payload, records, err = requestCapture(ctx, &http.Client{}, *endpoint, signal, *limit, *timeout)
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

func requestCapture(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	signal string,
	limit int,
	timeout time.Duration,
) ([]byte, int, error) {
	captureURL, err := appendEndpointPath(endpoint, "/v1/ingestion/capture")
	if err != nil {
		return nil, 0, fmt.Errorf("invalid capture endpoint %q: %w", endpoint, err)
	}
	parsed, err := url.Parse(captureURL)
	if err != nil {
		return nil, 0, fmt.Errorf("parse capture endpoint: %w", err)
	}
	query := parsed.Query()
	query.Set("signal", signal)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("timeout", timeout.String())
	parsed.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build capture request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("capture %s: %w", signal, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, 0, fmt.Errorf("capture %s: %s: %s", signal, response.Status, strings.TrimSpace(string(body)))
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read captured %s sample: %w", signal, err)
	}
	if len(payload) == 0 {
		return nil, 0, fmt.Errorf("capture %s returned an empty sample", signal)
	}
	records, _ := strconv.Atoi(response.Header.Get("X-Telescope-Records"))
	return payload, records, nil
}
