package vendordbexporter

import (
	"context"
	"fmt"
	"strings"
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
	if !e.cfg.CreateTableIfNotExists {
		return nil
	}

	key := strings.TrimRight(e.cfg.Endpoint, "/") + "|" + e.cfg.Table
	stateValue, _ := tableInitStates.LoadOrStore(key, &tableInitState{})
	state := stateValue.(*tableInitState)

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.done {
		return nil
	}

	timeout := e.cfg.Timeout.Timeout
	if timeout <= 0 {
		timeout = defaultTableInitTimeout
	}

	ensureCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := e.client.EnsureTable(ensureCtx); err != nil {
		return err
	}

	state.done = true
	return nil
}

func (c *Client) EnsureTable(ctx context.Context) error {
	c.logger.Info("Ensuring ScopeDB table exists", zap.String("table", c.cfg.Table))

	sdkClient := scopedb.NewClient(&scopedb.Config{
		Endpoint: c.cfg.Endpoint,
		APIKey:   string(c.cfg.APIKey),
	})
	defer sdkClient.Close()

	if _, err := sdkClient.Statement(c.createTableStatement()).Execute(ctx); err != nil {
		return fmt.Errorf("ensure table %s: %w", c.cfg.Table, err)
	}

	c.logger.Info("ScopeDB table is ready", zap.String("table", c.cfg.Table))
	return nil
}

func (c *Client) createTableStatement() string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
  ingest_ts timestamp,
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
)`, c.cfg.Table)
}
