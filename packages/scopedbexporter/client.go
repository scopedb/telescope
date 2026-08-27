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

package scopedbexporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	scopedb "github.com/scopedb/goscopedb"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.uber.org/zap"
)

const (
	maxAppendRequestBytes = 8 * 1024 * 1024
	maxAppendRequestRows  = 200_000
)

type Client struct {
	sdk      *scopedb.Client
	logger   *zap.Logger
	plans    map[string]*mappingPlan
	appendFn func(context.Context, *scopedb.Table, []byte) (scopedb.AppendRowsResult, error)
}

type deliveryError struct {
	err       error
	retryFrom int
}

func (e *deliveryError) Error() string {
	return e.err.Error()
}

func (e *deliveryError) Unwrap() error {
	return e.err
}

func NewClient(cfg *Config, settings exporter.Settings) (*Client, error) {
	return newClient(cfg, settings.Logger)
}

func newClient(cfg *Config, logger *zap.Logger) (*Client, error) {
	sdkClient, err := scopedb.NewClient(scopedb.Config{
		Endpoint:    cfg.Endpoint,
		APIKey:      string(cfg.APIKey),
		Compression: sdkCompression(cfg.compressionMode()),
	})
	if err != nil {
		return nil, err
	}

	plans := make(map[string]*mappingPlan, 3)
	for _, signal := range cfg.enabledSignals() {
		plan, err := compileMappingPlan(signal, cfg.tableForSignal(signal), cfg.mappingForSignal(signal))
		if err != nil {
			sdkClient.Close()
			return nil, fmt.Errorf("compile %s mapping: %w", signal, err)
		}
		plans[signal] = plan
	}

	return &Client{
		sdk:    sdkClient,
		logger: logger.Named("scopedbexporter/client"),
		plans:  plans,
		appendFn: func(ctx context.Context, table *scopedb.Table, ndjson []byte) (scopedb.AppendRowsResult, error) {
			return table.AppendNDJSON(ctx, ndjson)
		},
	}, nil
}

func (c *Client) Close() {
	if c.sdk != nil {
		c.sdk.Close()
	}
}

func (c *Client) ValidateDestination(ctx context.Context, signal string) error {
	_, err := c.inspectDestination(ctx, signal)
	return err
}

func (c *Client) inspectDestination(ctx context.Context, signal string) (SignalDestinationValidation, error) {
	plan, ok := c.plans[signal]
	if !ok {
		return SignalDestinationValidation{}, fmt.Errorf("no mapping plan for signal %q", signal)
	}

	description, err := c.table(plan.table).Describe(ctx)
	if err != nil {
		return SignalDestinationValidation{}, fmt.Errorf("describe target table %s: %w", plan.table.String(), err)
	}
	available := make(map[string]scopedb.DataType, len(description.Columns))
	for _, column := range description.Columns {
		available[column.Name] = column.DataType
	}
	var missing []string
	var incompatible []string
	validation := SignalDestinationValidation{
		Signal:  signal,
		Columns: make([]DestinationColumnValidation, 0, len(plan.columns)),
	}
	for _, column := range plan.columns {
		dataType, ok := available[column.name]
		if !ok {
			missing = append(missing, column.name)
			validation.Columns = append(validation.Columns, DestinationColumnValidation{
				MappingColumnDescription: describeMappedColumn(column),
				Compatibility:            MappingMissing,
			})
			continue
		}
		compatibility := column.outputType.compatibilityWith(dataType)
		validation.Columns = append(validation.Columns, DestinationColumnValidation{
			MappingColumnDescription: describeMappedColumn(column),
			TargetType:               string(dataType),
			Compatibility:            compatibility,
		})
		if compatibility == MappingIncompatible {
			incompatible = append(incompatible, fmt.Sprintf(
				"%s (%s produces %s, table has %s)",
				column.name, column.source, column.outputType, dataType,
			))
		}
	}
	var validationErrors []error
	if len(missing) > 0 {
		validationErrors = append(validationErrors, fmt.Errorf(
			"target table %s is missing mapped columns: %s", plan.table.String(), strings.Join(missing, ", "),
		))
	}
	if len(incompatible) > 0 {
		validationErrors = append(validationErrors, fmt.Errorf(
			"target table %s has incompatible mapped columns: %s", plan.table.String(), strings.Join(incompatible, "; "),
		))
	}
	return validation, errors.Join(validationErrors...)
}

func (c *Client) Send(ctx context.Context, signal string, payload *IngestPayload) error {
	if payload == nil {
		return consumererror.NewPermanent(errors.New("nil append payload"))
	}
	plan, ok := c.plans[signal]
	if !ok {
		return consumererror.NewPermanent(fmt.Errorf("no mapping plan for signal %q", signal))
	}
	if len(payload.Records) == 0 {
		return nil
	}

	table := c.table(plan.table)
	body := make([]byte, 0, min(maxAppendRequestBytes, len(payload.Records)*256))
	chunkRows := 0
	chunkStart := 0
	flush := func() error {
		if chunkRows == 0 {
			return nil
		}
		result, err := c.appendFn(ctx, table, body)
		if err != nil {
			return &deliveryError{err: classifyAppendError(err), retryFrom: chunkStart}
		}
		if result.AppendState != scopedb.AppendStateCommitted || result.NumRowsInserted != int64(chunkRows) {
			err := fmt.Errorf(
				"append to %s did not confirm all rows committed: state=%s inserted=%d expected=%d",
				plan.table.String(), result.AppendState, result.NumRowsInserted, chunkRows,
			)
			return &deliveryError{err: consumererror.NewRetryableError(err), retryFrom: chunkStart}
		}
		c.logger.Debug(
			"Appended rows to ScopeDB",
			zap.String("signal", signal),
			zap.String("table", plan.table.String()),
			zap.Int("records", chunkRows),
			zap.Int("uncompressed_bytes", len(body)),
		)
		body = body[:0]
		chunkRows = 0
		return nil
	}
	permanentAt := func(index int, err error) error {
		if chunkRows > 0 {
			if flushErr := flush(); flushErr != nil {
				return flushErr
			}
		}
		return &deliveryError{err: consumererror.NewPermanent(err), retryFrom: index}
	}

	for index, record := range payload.Records {
		row, err := plan.project(record)
		if err != nil {
			return permanentAt(index, fmt.Errorf("project mapped row %d: %w", index, err))
		}
		line, err := json.Marshal(row)
		if err != nil {
			return permanentAt(index, fmt.Errorf("marshal mapped row %d: %w", index, err))
		}
		lineBytes := len(line) + 1
		if lineBytes > maxAppendRequestBytes {
			return permanentAt(index, fmt.Errorf(
				"mapped row %d is %d bytes; maximum is %d", index, lineBytes, maxAppendRequestBytes,
			))
		}
		if chunkRows > 0 && (len(body)+lineBytes > maxAppendRequestBytes || chunkRows == maxAppendRequestRows) {
			if err := flush(); err != nil {
				return err
			}
			chunkStart = index
		}
		body = append(body, line...)
		body = append(body, '\n')
		chunkRows++
	}
	return flush()
}

func (c *Client) table(ref tableRef) *scopedb.Table {
	table := c.sdk.Table(ref.Table)
	table.Database = ref.Database
	table.Schema = ref.Schema
	return table
}

func classifyAppendError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}

	var scopeErr *scopedb.Error
	if !errors.As(err, &scopeErr) {
		return consumererror.NewRetryableError(err)
	}
	formatted := formatAppendError(scopeErr)
	if scopeErr.AppendDetails == nil && !scopeErr.Retryable {
		return consumererror.NewPermanent(formatted)
	}
	if scopeErr.AppendDetails != nil && scopeErr.AppendDetails.AppendState == scopedb.AppendStateRejected && !scopeErr.Retryable {
		return consumererror.NewPermanent(formatted)
	}

	retryable := consumererror.NewRetryableError(formatted)
	if scopeErr.RetryAfter > 0 {
		return exporterhelper.NewThrottleRetry(retryable, scopeErr.RetryAfter)
	}
	return retryable
}

func formatAppendError(err *scopedb.Error) error {
	parts := make([]string, 0, 3)
	if err.AppendDetails != nil {
		parts = append(parts, "state="+string(err.AppendDetails.AppendState))
		if len(err.AppendDetails.RowErrors) > 0 {
			row := err.AppendDetails.RowErrors[0]
			parts = append(parts, fmt.Sprintf("row=%d column=%s reason=%s", row.RowIndex, row.Column, row.Message))
		}
	}
	if err.RequestID != "" {
		parts = append(parts, "request_id="+err.RequestID)
	}
	if len(parts) == 0 {
		return err
	}
	return fmt.Errorf("ScopeDB append failed (%s): %w", strings.Join(parts, ", "), err)
}

func sdkCompression(mode string) scopedb.Compression {
	switch mode {
	case "gzip":
		return scopedb.CompressionGzip
	default:
		return scopedb.CompressionZstd
	}
}
