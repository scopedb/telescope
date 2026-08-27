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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
	"github.com/scopedb/telescope/packages/scopedbexporter"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

const (
	defaultOTLPEndpoint   = "http://127.0.0.1:4318"
	defaultStatusEndpoint = "http://127.0.0.1:8080/v1/ingestion/status"
)

func runVerify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	otlpEndpoint := flags.String("otlp-endpoint", defaultOTLPEndpoint, "OTLP HTTP base endpoint")
	statusEndpoint := flags.String("status-endpoint", defaultStatusEndpoint, "Telescope base URL or ingestion status endpoint")
	timeout := flags.Duration("timeout", 45*time.Second, "time to wait for each confirmed ScopeDB append")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := &http.Client{}
	baseline, err := readIngestionStatus(ctx, client, *statusEndpoint)
	if err != nil {
		return fmt.Errorf("read ingestion status before probe: %w", err)
	}
	signals, err := verifySignals(flags.Args(), baseline)
	if err != nil {
		return err
	}

	var errs []error
	for _, signal := range signals {
		baselineSignal, _ := findSignalStatus(baseline, signal)
		probeCtx, probeCancel := context.WithTimeout(context.Background(), *timeout)
		err := verifySignal(probeCtx, client, signal, baselineSignal, *otlpEndpoint, *statusEndpoint)
		probeCancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", signal, err))
		}
	}
	return errors.Join(errs...)
}

func verifySignals(requested []string, status statusapi.IngestionStatusResponse) ([]string, error) {
	if len(requested) == 0 {
		if len(status.Signals) == 0 {
			return nil, errors.New("the running Telescope has no enabled signals")
		}
		signals := make([]string, 0, len(status.Signals))
		for _, signal := range status.Signals {
			signals = append(signals, signal.Signal)
		}
		return signals, nil
	}

	seen := make(map[string]bool, len(requested))
	signals := make([]string, 0, len(requested))
	for _, raw := range requested {
		signal := strings.TrimSpace(raw)
		if !supportedSignal(signal) {
			return nil, fmt.Errorf("unsupported signal %q; choose logs, traces, or metrics", raw)
		}
		if !seen[signal] {
			seen[signal] = true
			signals = append(signals, signal)
		}
	}
	return signals, nil
}

func verifySignal(
	ctx context.Context,
	client *http.Client,
	signal string,
	baseline statusapi.IngestionSignalStatus,
	otlpEndpoint string,
	statusEndpoint string,
) error {
	if baseline.Signal == "" {
		return fmt.Errorf("signal is not enabled in the running Telescope")
	}
	if !baseline.Ready {
		return fmt.Errorf("signal is not ready: %s", baseline.LastError)
	}

	probeID, err := newProbeID()
	if err != nil {
		return err
	}
	payload, err := marshalIngestionProbe(signal, probeID, time.Now().UTC())
	if err != nil {
		return err
	}
	probeURL, err := appendEndpointPath(otlpEndpoint, "/v1/"+signal)
	if err != nil {
		return fmt.Errorf("invalid OTLP endpoint: %w", err)
	}
	if err := sendIngestionProbe(ctx, client, probeURL, signal, payload); err != nil {
		return fmt.Errorf("OTLP rejected probe %s: %w", probeID, err)
	}
	fmt.Fprintf(os.Stdout, "%s: OTLP accepted synthetic probe (%s)\n", signal, probeID)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	lastStatus := baseline
	var lastReadErr error
	for {
		select {
		case <-ctx.Done():
			detail := fmt.Sprintf("queue=%d/%d", lastStatus.Queue.Size, lastStatus.Queue.Capacity)
			if lastStatus.LastError != "" {
				detail += " last_error=" + lastStatus.LastError
			}
			if lastReadErr != nil {
				detail += " status_error=" + lastReadErr.Error()
			}
			return fmt.Errorf("probe %s was accepted by OTLP but its ScopeDB append was not confirmed: %s", probeID, detail)
		case <-ticker.C:
			status, err := readIngestionStatus(ctx, client, statusEndpoint)
			if err != nil {
				lastReadErr = err
				continue
			}
			lastReadErr = nil
			current, ok := findSignalStatus(status, signal)
			if !ok {
				return fmt.Errorf("signal was disabled while waiting for probe %s", probeID)
			}
			lastStatus = current
			if containsString(current.LastProbeIDs, probeID) {
				fmt.Fprintf(os.Stdout, "%s: ScopeDB append committed synthetic probe (%s)\n", signal, probeID)
				return nil
			}
		}
	}
}

func newProbeID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate probe id: %w", err)
	}
	return "probe-" + hex.EncodeToString(value[:]), nil
}

func marshalIngestionProbe(signal string, probeID string, now time.Time) ([]byte, error) {
	timestamp := pcommon.NewTimestampFromTime(now)
	switch signal {
	case "logs":
		logs := plog.NewLogs()
		resourceLogs := logs.ResourceLogs().AppendEmpty()
		resourceLogs.Resource().Attributes().PutStr(scopedbexporter.ProbeAttribute, probeID)
		resourceLogs.Resource().Attributes().PutStr("service.name", "telescope-ingestion-probe")
		record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		record.SetTimestamp(timestamp)
		record.SetObservedTimestamp(timestamp)
		record.Body().SetStr("Telescope ingestion probe")
		return plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	case "traces":
		traces := ptrace.NewTraces()
		resourceSpans := traces.ResourceSpans().AppendEmpty()
		resourceSpans.Resource().Attributes().PutStr(scopedbexporter.ProbeAttribute, probeID)
		resourceSpans.Resource().Attributes().PutStr("service.name", "telescope-ingestion-probe")
		span := resourceSpans.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		identity := sha256.Sum256([]byte(probeID))
		span.SetTraceID(pcommon.TraceID(identity[:16]))
		span.SetSpanID(pcommon.SpanID(identity[16:24]))
		span.SetName("telescope.ingestion.probe")
		span.SetStartTimestamp(timestamp)
		span.SetEndTimestamp(timestamp + pcommon.Timestamp(time.Millisecond))
		return ptraceotlp.NewExportRequestFromTraces(traces).MarshalProto()
	case "metrics":
		metrics := pmetric.NewMetrics()
		resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
		resourceMetrics.Resource().Attributes().PutStr(scopedbexporter.ProbeAttribute, probeID)
		resourceMetrics.Resource().Attributes().PutStr("service.name", "telescope-ingestion-probe")
		metric := resourceMetrics.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		metric.SetName("telescope.ingestion.probe")
		point := metric.SetEmptyGauge().DataPoints().AppendEmpty()
		point.SetTimestamp(timestamp)
		point.SetIntValue(1)
		return pmetricotlp.NewExportRequestFromMetrics(metrics).MarshalProto()
	default:
		return nil, fmt.Errorf("unsupported probe signal %q", signal)
	}
}

func sendIngestionProbe(ctx context.Context, client *http.Client, endpoint string, signal string, payload []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return validateProbeResponse(signal, body)
}

func validateProbeResponse(signal string, body []byte) error {
	switch signal {
	case "logs":
		response := plogotlp.NewExportResponse()
		if err := response.UnmarshalProto(body); err != nil {
			return fmt.Errorf("decode OTLP logs response: %w", err)
		}
		if rejected := response.PartialSuccess().RejectedLogRecords(); rejected > 0 {
			return fmt.Errorf("collector rejected %d log records: %s", rejected, response.PartialSuccess().ErrorMessage())
		}
	case "traces":
		response := ptraceotlp.NewExportResponse()
		if err := response.UnmarshalProto(body); err != nil {
			return fmt.Errorf("decode OTLP traces response: %w", err)
		}
		if rejected := response.PartialSuccess().RejectedSpans(); rejected > 0 {
			return fmt.Errorf("collector rejected %d spans: %s", rejected, response.PartialSuccess().ErrorMessage())
		}
	case "metrics":
		response := pmetricotlp.NewExportResponse()
		if err := response.UnmarshalProto(body); err != nil {
			return fmt.Errorf("decode OTLP metrics response: %w", err)
		}
		if rejected := response.PartialSuccess().RejectedDataPoints(); rejected > 0 {
			return fmt.Errorf("collector rejected %d metric points: %s", rejected, response.PartialSuccess().ErrorMessage())
		}
	default:
		return fmt.Errorf("unsupported probe signal %q", signal)
	}
	return nil
}

func readIngestionStatus(ctx context.Context, client *http.Client, endpoint string) (statusapi.IngestionStatusResponse, error) {
	var status statusapi.IngestionStatusResponse
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return status, fmt.Errorf("invalid status endpoint %q", endpoint)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1/ingestion/status"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return status, err
	}
	response, err := client.Do(request)
	if err != nil {
		return status, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return status, fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return status, fmt.Errorf("decode response: %w", err)
	}
	return status, nil
}

func findSignalStatus(status statusapi.IngestionStatusResponse, signal string) (statusapi.IngestionSignalStatus, bool) {
	for _, current := range status.Signals {
		if current.Signal == signal {
			return current, true
		}
	}
	return statusapi.IngestionSignalStatus{}, false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func appendEndpointPath(base string, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("endpoint must include scheme and host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("endpoint must not include a query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String(), nil
}
