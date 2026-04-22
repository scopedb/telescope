package vendordbexporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.opentelemetry.io/collector/exporter"
	"go.uber.org/zap"
)

const userAgent = "vendor-otel-gateway/0.1.0"

type Client struct {
	cfg        *Config
	httpClient *http.Client
	logger     *zap.Logger
}

func NewClient(cfg *Config, settings exporter.Settings) (*Client, error) {
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		transport = &http.Transport{}
	} else {
		transport = transport.Clone()
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Transport: transport,
		},
		logger: settings.Logger.Named("vendordbexporter/client"),
	}, nil
}

func (c *Client) Close() {
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

func (c *Client) Send(ctx context.Context, signal string, payload *IngestPayload) error {
	if payload == nil {
		return fmt.Errorf("nil ingest payload")
	}

	payload.Signal = signal
	if payload.SchemaVersion == "" {
		payload.SchemaVersion = c.cfg.SchemaVersion
	}
	if payload.Dataset == "" {
		payload.Dataset = c.cfg.Dataset
	}

	rawBody, err := c.marshalScopeDBRequest(payload)
	if err != nil {
		return fmt.Errorf("marshal ingest request: %w", err)
	}

	requestBody := rawBody
	if c.cfg.compressionEnabled() {
		requestBody, err = gzipBytes(rawBody)
		if err != nil {
			return fmt.Errorf("gzip ingest payload: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL(), bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", c.formattedAPIKey())
	if c.cfg.Dataset != "" {
		req.Header.Set("X-Vendor-Dataset", c.cfg.Dataset)
	}
	if c.cfg.compressionEnabled() {
		req.Header.Set("Content-Encoding", "gzip")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return classifyRequestError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.logger.Debug(
			"Sent ingest request",
			zap.String("signal", signal),
			zap.Int("records", len(payload.Records)),
			zap.Int("status_code", resp.StatusCode),
		)
		return nil
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if readErr != nil {
		body = []byte(fmt.Sprintf("failed to read response body: %v", readErr))
	}

	return classifyHTTPStatus(resp.StatusCode, &httpStatusError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       strings.TrimSpace(string(body)),
	})
}

func (c *Client) ingestURL() string {
	return strings.TrimRight(c.cfg.Endpoint, "/") + c.cfg.Path
}

func (c *Client) formattedAPIKey() string {
	raw := string(c.cfg.APIKey)
	if !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return "Bearer " + raw
	}
	return raw
}

func (c *Client) marshalScopeDBRequest(payload *IngestPayload) ([]byte, error) {
	rowsBody, err := marshalJSONLines(payload.scopeDBRows())
	if err != nil {
		return nil, err
	}

	request := scopeDBIngestRequest{
		Type: "committed",
		Data: scopeDBIngestData{
			Format: "json",
			Rows:   rowsBody,
		},
		Statement: c.defaultIngestStatement(),
	}

	return json.Marshal(request)
}

func (c *Client) defaultIngestStatement() string {
	return fmt.Sprintf(`SELECT
  $0["ingest_ts"]::timestamp AS ingest_ts,
  $0["signal"]::string AS signal,
  $0["schema_version"]::string AS schema_version,
  $0["dataset"]::string AS dataset,
  $0["trace_id"]::string AS trace_id,
  $0["span_id"]::string AS span_id,
  $0["parent_span_id"]::string AS parent_span_id,
  $0["service_name"]::string AS service_name,
  $0["metric_name"]::string AS metric_name,
  $0["severity_text"]::string AS severity_text,
  $0["record"]::object AS record
INSERT INTO %s (
  ingest_ts,
  signal,
  schema_version,
  dataset,
  trace_id,
  span_id,
  parent_span_id,
  service_name,
  metric_name,
  severity_text,
  record
)`, c.cfg.Table)
}

func marshalJSONLines(rows []map[string]any) (string, error) {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return "", err
		}
		lines = append(lines, string(raw))
	}
	return strings.Join(lines, "\n"), nil
}

func gzipBytes(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
