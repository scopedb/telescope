package scopedbexporter

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/collector/exporter"
	"go.uber.org/zap"
)

const userAgent = "telescope/0.1.0"

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
		logger: settings.Logger.Named("scopedbexporter/client"),
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
	if payload.Env == "" {
		payload.Env = c.cfg.Env
	}

	table, err := parseTableRef(c.cfg.tableForSignal(signal))
	if err != nil {
		return fmt.Errorf("resolve table for %s: %w", signal, err)
	}

	rawBody, err := c.marshalScopeDBRequest(signal, payload, table)
	if err != nil {
		return fmt.Errorf("marshal ingest request: %w", err)
	}

	requestBody, contentEncoding, err := compressRequestBody(rawBody, c.cfg.compressionMode())
	if err != nil {
		return fmt.Errorf("compress ingest payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ingestURL(), bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", c.formattedAPIKey())
	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
		req.Header.Set("X-ScopeDB-Uncompressed-Content-Length", strconv.Itoa(len(rawBody)))
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
			zap.String("table", table.String()),
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

func (c *Client) marshalScopeDBRequest(signal string, payload *IngestPayload, table tableRef) ([]byte, error) {
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
		Statement: c.defaultIngestStatement(signal, table),
	}

	return json.Marshal(request)
}

func (c *Client) defaultIngestStatement(signal string, table tableRef) string {
	return ingestStatementForSignal(signal, table)
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

func zstdBytes(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(raw); err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func compressRequestBody(raw []byte, compression string) ([]byte, string, error) {
	switch compression {
	case "none":
		return raw, "", nil
	case "gzip":
		body, err := gzipBytes(raw)
		return body, "gzip", err
	case "zstd":
		body, err := zstdBytes(raw)
		return body, "zstd", err
	default:
		return nil, "", fmt.Errorf("unsupported compression %q", compression)
	}
}
