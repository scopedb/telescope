package scopedbexporter

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

	for _, signal := range []string{signalLogs, signalTraces, signalMetrics} {
		table := e.cfg.tableForSignal(signal)
		ref, err := parseTableRef(table)
		if err != nil {
			return fmt.Errorf("resolve table route for %s (%q): %w", signal, table, err)
		}

		key := e.cfg.Endpoint + "|" + signal + "|" + ref.String()
		stateValue, _ := tableInitStates.LoadOrStore(key, &tableInitState{})
		state := stateValue.(*tableInitState)

		state.mu.Lock()
		if state.done {
			state.mu.Unlock()
			continue
		}

		ensureCtx, cancel := context.WithTimeout(ctx, timeout)
		err = e.client.EnsureTable(ensureCtx, signal, ref)
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

func (c *Client) EnsureTable(ctx context.Context, signal string, table tableRef) error {
	c.logger.Info(
		"Ensuring ScopeDB table exists",
		zap.String("signal", signal),
		zap.String("table", table.String()),
	)

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

	if _, err := sdkClient.Statement(c.createTableStatement(signal, table)).Execute(ctx); err != nil {
		return fmt.Errorf("ensure %s table %s: %w", signal, table.String(), err)
	}

	for _, statement := range c.createIndexStatements(signal, table) {
		if _, err := sdkClient.Statement(statement).Execute(ctx); err != nil {
			return fmt.Errorf("ensure %s table index for %s: %w", signal, table.String(), err)
		}
	}

	c.logger.Info(
		"ScopeDB table is ready",
		zap.String("signal", signal),
		zap.String("table", table.String()),
	)
	return nil
}

func (c *Client) createDatabaseStatement(table tableRef) string {
	return fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s`, quoteTablePart(table.Database))
}

func (c *Client) createSchemaStatement(table tableRef) string {
	return fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, table.SchemaIdentifier())
}

func (c *Client) createTableStatement(signal string, table tableRef) string {
	return createTableStatementForSignal(signal, table)
}

func (c *Client) createIndexStatements(signal string, table tableRef) []string {
	return createIndexStatementsForSignal(signal, table)
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
