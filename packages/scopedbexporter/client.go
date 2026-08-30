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
	"sort"
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

type recordFailure struct {
	index int
	err   error
}

type sendOutcome struct {
	committed   []int
	rejected    []recordFailure
	uncommitted []int
	err         error
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
	ingestion := cfg.ingestionConfig()
	for _, signal := range ingestion.EnabledSignals() {
		signalConfig, _ := ingestion.Signal(signal)
		plan, err := compileMappingPlan(signal, signalConfig.Table, signalConfig.Mapping)
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
	outcome := c.send(ctx, signal, payload)
	if outcome.err != nil {
		return outcome.err
	}
	if len(outcome.rejected) == 0 {
		return nil
	}
	errs := make([]error, 0, len(outcome.rejected))
	for _, failure := range outcome.rejected {
		errs = append(errs, failure.err)
	}
	return consumererror.NewPermanent(fmt.Errorf(
		"%d mapped rows rejected: %w",
		len(outcome.rejected),
		errors.Join(errs...),
	))
}

func (c *Client) send(ctx context.Context, signal string, payload *IngestPayload) sendOutcome {
	if payload == nil {
		return sendOutcome{err: consumererror.NewPermanent(errors.New("nil append payload"))}
	}
	plan, ok := c.plans[signal]
	if !ok {
		return sendOutcome{
			uncommitted: recordIndexes(0, len(payload.Records)),
			err:         consumererror.NewPermanent(fmt.Errorf("no mapping plan for signal %q", signal)),
		}
	}
	if len(payload.Records) == 0 {
		return sendOutcome{}
	}

	outcome := sendOutcome{}
	table := c.table(plan.table)
	body := make([]byte, 0, min(maxAppendRequestBytes, len(payload.Records)*256))
	chunkIndexes := make([]int, 0, min(maxAppendRequestRows, len(payload.Records)))
	chunkStarts := make([]int, 0, cap(chunkIndexes))
	resetChunk := func() {
		body = body[:0]
		chunkIndexes = chunkIndexes[:0]
		chunkStarts = chunkStarts[:0]
	}
	setDeliveryError := func(err error, remainingFrom int) {
		outcome.uncommitted = append(outcome.uncommitted[:0], chunkIndexes...)
		outcome.uncommitted = append(outcome.uncommitted, recordIndexes(remainingFrom, len(payload.Records))...)
		outcome.err = err
	}
	appendChunk := func(chunkBody []byte, rows int) error {
		result, err := c.appendFn(ctx, table, chunkBody)
		if err != nil {
			return err
		}
		if result.AppendState != scopedb.AppendStateCommitted || result.NumRowsInserted != int64(rows) {
			return consumererror.NewRetryableError(fmt.Errorf(
				"append to %s did not confirm all rows committed: state=%s inserted=%d expected=%d",
				plan.table.String(), result.AppendState, result.NumRowsInserted, rows,
			))
		}
		c.logger.Debug(
			"Appended rows to ScopeDB",
			zap.String("signal", signal),
			zap.String("table", plan.table.String()),
			zap.Int("records", rows),
			zap.Int("uncompressed_bytes", len(chunkBody)),
		)
		return nil
	}
	flush := func(remainingFrom int) bool {
		if len(chunkIndexes) == 0 {
			return true
		}
		err := appendChunk(body, len(chunkIndexes))
		if err == nil {
			outcome.committed = append(outcome.committed, chunkIndexes...)
			resetChunk()
			return true
		}

		rowErrors, ok := completeRejectedRows(err, len(chunkIndexes))
		if !ok {
			setDeliveryError(classifyAppendError(err), remainingFrom)
			return false
		}

		badRows := make(map[int]struct{}, len(rowErrors))
		for _, rowErr := range rowErrors {
			position := int(rowErr.RowIndex)
			badRows[position] = struct{}{}
			outcome.rejected = append(outcome.rejected, recordFailure{
				index: chunkIndexes[position],
				err:   formatRejectedRowError(plan.table.String(), chunkIndexes[position], rowErr),
			})
		}
		body, chunkIndexes, chunkStarts = removeChunkRows(body, chunkIndexes, chunkStarts, badRows)
		if len(chunkIndexes) == 0 {
			resetChunk()
			return true
		}
		if err := appendChunk(body, len(chunkIndexes)); err != nil {
			setDeliveryError(classifyAppendError(err), remainingFrom)
			return false
		}
		outcome.committed = append(outcome.committed, chunkIndexes...)
		resetChunk()
		return true
	}
	reject := func(index int, err error) {
		outcome.rejected = append(outcome.rejected, recordFailure{index: index, err: err})
	}

	for index, record := range payload.Records {
		row, err := plan.project(record)
		if err != nil {
			reject(index, fmt.Errorf("project mapped row %d: %w", index, err))
			continue
		}
		line, err := json.Marshal(row)
		if err != nil {
			reject(index, &mappingError{
				reason: mappingReasonEncodingFailed,
				err:    fmt.Errorf("marshal mapped row %d: %w", index, err),
			})
			continue
		}
		lineBytes := len(line) + 1
		if lineBytes > maxAppendRequestBytes {
			reject(index, &mappingError{
				reason: mappingReasonRowTooLarge,
				err: fmt.Errorf(
					"mapped row %d is %d bytes; maximum is %d", index, lineBytes, maxAppendRequestBytes,
				),
			})
			continue
		}
		if len(chunkIndexes) > 0 && (len(body)+lineBytes > maxAppendRequestBytes || len(chunkIndexes) == maxAppendRequestRows) {
			if !flush(index) {
				return outcome
			}
		}
		chunkStarts = append(chunkStarts, len(body))
		body = append(body, line...)
		body = append(body, '\n')
		chunkIndexes = append(chunkIndexes, index)
	}
	flush(len(payload.Records))
	return outcome
}

func completeRejectedRows(err error, rowCount int) ([]scopedb.AppendRowError, bool) {
	var scopeErr *scopedb.Error
	if !errors.As(err, &scopeErr) || scopeErr.Retryable || scopeErr.AppendDetails == nil {
		return nil, false
	}
	details := scopeErr.AppendDetails
	if details.AppendState != scopedb.AppendStateRejected || details.RowErrorsTruncated || len(details.RowErrors) == 0 {
		return nil, false
	}
	rows := append([]scopedb.AppendRowError(nil), details.RowErrors...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].RowIndex < rows[j].RowIndex })
	unique := rows[:0]
	for _, row := range rows {
		if row.RowIndex >= uint64(rowCount) {
			return nil, false
		}
		if len(unique) == 0 || row.RowIndex != unique[len(unique)-1].RowIndex {
			unique = append(unique, row)
		}
	}
	return unique, true
}

func removeChunkRows(
	body []byte,
	indexes []int,
	starts []int,
	rejected map[int]struct{},
) ([]byte, []int, []int) {
	filteredBody := make([]byte, 0, len(body))
	filteredIndexes := make([]int, 0, len(indexes)-len(rejected))
	filteredStarts := make([]int, 0, cap(filteredIndexes))
	for position, recordIndex := range indexes {
		if _, found := rejected[position]; found {
			continue
		}
		end := len(body)
		if position+1 < len(starts) {
			end = starts[position+1]
		}
		filteredStarts = append(filteredStarts, len(filteredBody))
		filteredBody = append(filteredBody, body[starts[position]:end]...)
		filteredIndexes = append(filteredIndexes, recordIndex)
	}
	return filteredBody, filteredIndexes, filteredStarts
}

func formatRejectedRowError(table string, recordIndex int, row scopedb.AppendRowError) error {
	return fmt.Errorf(
		"ScopeDB rejected mapped row %d for %s: column=%s reason=%s",
		recordIndex,
		table,
		row.Column,
		row.Message,
	)
}

func recordIndexes(from int, to int) []int {
	indexes := make([]int, 0, max(0, to-from))
	for index := from; index < to; index++ {
		indexes = append(indexes, index)
	}
	return indexes
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
