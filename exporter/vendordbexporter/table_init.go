package vendordbexporter

import (
	"context"
	"fmt"
	"sync"
	"time"

	scopedb "github.com/scopedb/scopedb-sdk/go"
	"go.uber.org/zap"
)

const defaultTableInitTimeout = 30 * time.Second

type tableInitState struct {
	mu   sync.Mutex
	done bool
}

var tableInitStates sync.Map

func (e *dbExporter) ensureTable(ctx context.Context) error {
	if !e.cfg.CreateTablesIfNotExist {
		return nil
	}

	timeout := e.cfg.Timeout.Timeout
	if timeout <= 0 {
		timeout = defaultTableInitTimeout
	}

	for _, table := range e.cfg.configuredTables() {
		ref, err := parseTableRef(table)
		if err != nil {
			return fmt.Errorf("resolve table route %q: %w", table, err)
		}

		key := e.cfg.Endpoint + "|" + ref.String()
		stateValue, _ := tableInitStates.LoadOrStore(key, &tableInitState{})
		state := stateValue.(*tableInitState)

		state.mu.Lock()
		if state.done {
			state.mu.Unlock()
			continue
		}

		ensureCtx, cancel := context.WithTimeout(ctx, timeout)
		err = e.client.EnsureTable(ensureCtx, ref)
		cancel()
		if err != nil {
			state.mu.Unlock()
			return err
		}

		state.done = true
		state.mu.Unlock()
	}

	return nil
}

func (c *Client) EnsureTable(ctx context.Context, table tableRef) error {
	c.logger.Info("Ensuring ScopeDB table exists", zap.String("table", table.String()))

	sdkClient := scopedb.NewClient(&scopedb.Config{
		Endpoint:    c.cfg.Endpoint,
		APIKey:      string(c.cfg.APIKey),
		Compression: sdkCompression(c.cfg.compressionMode()),
	})
	defer sdkClient.Close()

	if table.Database != "" {
		if _, err := sdkClient.Statement(c.createDatabaseStatement(table)).Execute(ctx); err != nil {
			return fmt.Errorf("ensure database for %s: %w", table.String(), err)
		}
	}

	if table.Schema != "" {
		if _, err := sdkClient.Statement(c.createSchemaStatement(table)).Execute(ctx); err != nil {
			return fmt.Errorf("ensure schema for %s: %w", table.String(), err)
		}
	}

	if _, err := sdkClient.Statement(c.createTableStatement(table)).Execute(ctx); err != nil {
		return fmt.Errorf("ensure table %s: %w", table.String(), err)
	}

	c.logger.Info("ScopeDB table is ready", zap.String("table", table.String()))
	return nil
}

func (c *Client) createDatabaseStatement(table tableRef) string {
	return fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s`, quoteTablePart(table.Database))
}

func (c *Client) createSchemaStatement(table tableRef) string {
	return fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, table.SchemaIdentifier())
}

func (c *Client) createTableStatement(table tableRef) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
  ingest_ts timestamp,
  record_timestamp timestamp,
  observed_timestamp timestamp,
  start_timestamp timestamp,
  end_timestamp timestamp,
  signal string,
  schema_version string,
  dataset string,
  trace_id string,
  span_id string,
  parent_span_id string,
  service_name string,
  metric_name string,
  severity_text string,
  record object
)`, table.Identifier())
}

func sdkCompression(mode string) scopedb.Compression {
	switch mode {
	case "gzip":
		return scopedb.CompressionGzip
	case "zstd":
		return scopedb.CompressionZstd
	default:
		return ""
	}
}
