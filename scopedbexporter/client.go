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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	scopedb "github.com/scopedb/goscopedb"
	"github.com/scopedb/telescope/internal/httpclient"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.uber.org/zap"
)

const (
	maxAppendRequestBytes = 8 * 1024 * 1024
	maxAppendRequestRows  = 200_000
)

type appendClient struct {
	*httpclient.ScopeDBClient
	logger *zap.Logger
	plans  map[string]*mappingPlan
	batch  AppendBatchConfig
}

type recordFailure struct {
	index int
	err   error
}

type sendOutcome struct {
	committed   []int
	rejected    []recordFailure
	unknown     []recordFailure
	uncommitted []int
	err         error
}

func newClient(cfg *Config, logger *zap.Logger) (*appendClient, error) {
	compiled, err := cfg.ingestionConfig().compile()
	if err != nil {
		return nil, err
	}
	sdkClient, err := httpclient.NewScopeDB(scopedb.Config{
		Endpoint:    cfg.Endpoint,
		APIKey:      string(cfg.APIKey),
		Compression: sdkCompression(cfg.compressionMode()),
		// Collector's per-attempt context owns the timeout, including mapping
		// and all chunks. Do not add the SDK's independent 30-second deadline.
		WriteTimeout: new(time.Duration(0)),
	})
	if err != nil {
		return nil, err
	}

	plans := make(map[string]*mappingPlan, len(compiled))
	for _, plan := range compiled {
		plans[plan.signal] = plan
	}

	return &appendClient{
		ScopeDBClient: sdkClient,
		logger:        logger.Named("scopedbexporter/client"),
		plans:         plans,
		batch:         cfg.AppendBatch,
	}, nil
}

func (c *appendClient) send(ctx context.Context, signal string, payload *ingestPayload, replay *unknownRetryState) sendOutcome {
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
	table := plan.table.table(c.Client)
	maxBytes, maxRows := c.batch.limits()
	body := make([]byte, 0, min(maxBytes, len(payload.Records)*256))
	chunkIndexes := make([]int, 0, min(maxRows, len(payload.Records)))
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
	appendChunk := func(ctx context.Context, chunkBody []byte, rows int) error {
		result, err := table.AppendNDJSON(ctx, chunkBody)
		if err != nil {
			return err
		}
		if result.Status != scopedb.AppendStateCommitted || result.NumRowsInserted != int64(rows) {
			return &scopedb.Error{
				Kind:          scopedb.ErrorKindAppendRowsFailed,
				Message:       fmt.Sprintf("append to %s did not confirm all rows committed: status=%s inserted=%d expected=%d", plan.table.String(), result.Status, result.NumRowsInserted, rows),
				AppendDetails: &scopedb.AppendErrorDetails{Status: scopedb.AppendStateUnknown},
			}
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
	settleUnknown := func(err error) bool {
		if !isUnknownAppend(err) {
			return false
		}
		for _, index := range chunkIndexes {
			outcome.unknown = append(outcome.unknown, recordFailure{index: index, err: err})
		}
		resetChunk()
		return true
	}
	flush := func(remainingFrom int) bool {
		if len(chunkIndexes) == 0 {
			return true
		}
		err := appendChunk(ctx, body, len(chunkIndexes))
		if err == nil {
			outcome.committed = append(outcome.committed, chunkIndexes...)
			resetChunk()
			return true
		}
		if replay.retain(ctx, body, len(chunkIndexes), err) {
			setDeliveryError(unknownRetryError(err), remainingFrom)
			return false
		}
		if settleUnknown(err) {
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
		if err := appendChunk(ctx, body, len(chunkIndexes)); err != nil {
			if replay.retain(ctx, body, len(chunkIndexes), err) {
				setDeliveryError(unknownRetryError(err), remainingFrom)
				return false
			}
			if settleUnknown(err) {
				return true
			}
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

	startIndex := 0
	if replay != nil && replay.pending != nil {
		pending := replay.pending
		chunkIndexes = recordIndexes(0, pending.rows)
		err := pending.err
		// Shutdown ends scheduling; an unresolved file entry must remain recoverable.
		if replay.canReplay(ctx) && !replay.exporter.stopping.Load() {
			replay.recordReplay()
			attemptCtx, cancel := context.WithDeadline(ctx, replay.started.Add(replay.config.MaxElapsedTime))
			err = appendChunk(attemptCtx, pending.body, pending.rows)
			cancel()
		}
		if err == nil {
			outcome.committed = append(outcome.committed, chunkIndexes...)
		} else {
			pending.err = err
			if replay.canRetryAfter(ctx, err) && retryableAfterUnknown(err) {
				setDeliveryError(unknownRetryError(err), pending.rows)
				return outcome
			}
			settleUnknown(unresolvedUnknown(err))
		}
		startIndex = pending.rows
		replay.pending = nil
		resetChunk()
	}
	for index := startIndex; index < len(payload.Records); index++ {
		record := payload.Records[index]
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
		if lineBytes > maxBytes {
			reject(index, &mappingError{
				reason: mappingReasonRowTooLarge,
				err: fmt.Errorf(
					"mapped row %d is %d bytes; maximum is %d", index, lineBytes, maxBytes,
				),
			})
			continue
		}
		if len(chunkIndexes) > 0 && (len(body)+lineBytes > maxBytes || len(chunkIndexes) == maxRows) {
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
	scopeErr, ok := errors.AsType[*scopedb.Error](err)
	if !ok || scopeErr.Retryable || scopeErr.AppendDetails == nil {
		return nil, false
	}
	details := scopeErr.AppendDetails
	if details.Status != scopedb.AppendStateRejected || details.RowErrorsTruncated || len(details.RowErrors) == 0 {
		return nil, false
	}
	rows := slices.Clone(details.RowErrors)
	slices.SortStableFunc(rows, func(left, right scopedb.AppendRowError) int { return cmp.Compare(left.RowIndex, right.RowIndex) })
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

func classifyAppendError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}

	scopeErr, ok := errors.AsType[*scopedb.Error](err)
	if !ok {
		return consumererror.NewRetryableError(err)
	}
	formatted := formatAppendError(scopeErr)
	if scopeErr.AppendDetails == nil || scopeErr.AppendDetails.Status != scopedb.AppendStateRejected || !scopeErr.Retryable {
		return consumererror.NewPermanent(formatted)
	}

	retryable := consumererror.NewRetryableError(formatted)
	if scopeErr.RetryAfter > 0 {
		return exporterhelper.NewThrottleRetry(retryable, scopeErr.RetryAfter)
	}
	return retryable
}

func isUnknownAppend(err error) bool {
	scopeErr, ok := errors.AsType[*scopedb.Error](err)
	return ok && scopeErr.AppendDetails != nil && scopeErr.AppendDetails.Status == scopedb.AppendStateUnknown
}

func formatAppendError(err *scopedb.Error) error {
	parts := make([]string, 0, 3)
	if err.AppendDetails != nil {
		parts = append(parts, "state="+string(err.AppendDetails.Status))
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
