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
	"time"

	scopedb "github.com/scopedb/goscopedb"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

// appendDelivery owns one dequeued request. Only the pending batch can be retried;
// next points past records already mapped into that batch or settled locally.
// Collector owns persistence and retry scheduling, and the SDK owns each attempt.
type appendDelivery struct {
	payload           *ingestPayload
	plan              *mappingPlan
	table             *scopedb.Table
	maxBytes, maxRows int
	next              int
	pending           *deliveryBatch
	retry             AmbiguousRetryConfig
	replayStarted     time.Time
	replays           int
	terminalErr       error
}

type deliveryBatch struct {
	body      []byte
	indexes   []int
	ambiguous bool
	replayed  bool
	err       error
}

type deliveryFailure struct {
	records int
	err     error
}

// Each result contains only records settled during this call.
type deliveryResult struct {
	succeeded                       []int
	failed                          []deliveryFailure
	ambiguous                       []deliveryFailure
	replayAttempts, replayedRecords int
	err                             error
}

func (c *appendClient) newDelivery(signal string, payload *ingestPayload, retry AmbiguousRetryConfig) (*appendDelivery, error) {
	if payload == nil {
		return nil, errors.New("nil append payload")
	}
	plan, ok := c.plans[signal]
	if !ok {
		return nil, fmt.Errorf("no mapping plan for signal %q", signal)
	}
	maxBytes, maxRows := c.batch.limits()
	return &appendDelivery{payload: payload, plan: plan, table: plan.table.table(c.Client), maxBytes: maxBytes, maxRows: maxRows, retry: retry}, nil
}

func (d *appendDelivery) remaining() int {
	n := len(d.payload.Records) - d.next
	if d.pending != nil {
		n += len(d.pending.indexes)
	}
	return n
}

func (d *appendDelivery) prepare(result *deliveryResult) {
	batch := &deliveryBatch{}
	for d.next < len(d.payload.Records) {
		index := d.next
		row, err := d.plan.project(d.payload.Records[index])
		var line []byte
		if err == nil {
			line, err = json.Marshal(row)
			if err != nil {
				err = &mappingError{reason: mappingReasonEncodingFailed, err: fmt.Errorf("marshal mapped row %d: %w", index, err)}
			}
		}
		if err == nil && len(line)+1 > d.maxBytes {
			err = &mappingError{reason: mappingReasonRowTooLarge, err: fmt.Errorf("mapped row %d is %d bytes; maximum is %d", index, len(line)+1, d.maxBytes)}
		}
		if err != nil {
			result.failed = append(result.failed, deliveryFailure{records: 1, err: err})
			d.next++
			continue
		}
		if len(batch.indexes) > 0 && (len(batch.body)+len(line)+1 > d.maxBytes || len(batch.indexes) == d.maxRows) {
			break
		}
		batch.body = append(batch.body, line...)
		batch.body = append(batch.body, '\n')
		batch.indexes = append(batch.indexes, index)
		d.next++
	}
	if len(batch.indexes) > 0 {
		d.pending = batch
	}
}

func (d *appendDelivery) send(ctx context.Context) deliveryResult {
	result := deliveryResult{}
	for d.pending != nil || d.next < len(d.payload.Records) {
		if err := ctx.Err(); err != nil {
			result.err = err
			return result
		}
		if d.pending == nil {
			d.prepare(&result)
		}
		batch := d.pending
		if batch == nil {
			break
		}
		attemptCtx := ctx
		cancel := func() {}
		if batch.ambiguous {
			if !d.canReplayAt(batch.err, time.Now()) {
				d.settleAmbiguous(&result, batch.err)
				continue
			}
			d.replays++
			result.replayAttempts++
			if !batch.replayed {
				result.replayedRecords += len(batch.indexes)
				batch.replayed = true
			}
			attemptCtx, cancel = context.WithDeadline(ctx, d.replayStarted.Add(d.retry.MaxElapsedTime))
		}
		_, err := d.table.AppendNDJSON(attemptCtx, batch.body)
		cancel()
		if err == nil {
			result.succeeded = append(result.succeeded, batch.indexes...)
			d.pending = nil
			continue
		}
		batch.err = err
		if appendOutcome(err) == scopedb.AppendOutcomeAmbiguous {
			batch.ambiguous = true
		}
		// Leave interrupted work unsettled so shutdown can retain the queue entry.
		if ctx.Err() != nil {
			result.err = err
			return result
		}
		if batch.ambiguous {
			if d.replayStarted.IsZero() && d.retry.Enabled && retryableAfterAmbiguous(err) {
				d.replayStarted = time.Now()
			}
			if failure, ok := errors.AsType[*scopedb.Error](err); ok && d.canReplayAt(err, time.Now().Add(failure.RetryAfter)) {
				result.err = retryAppend(err)
				return result
			}
			d.settleAmbiguous(&result, err)
			continue
		}
		if failure, ok := errors.AsType[*scopedb.Error](err); ok && failure.Retryable {
			result.err = retryAppend(err)
			return result
		}
		// The current API rejects whole requests. Never infer which rows are safe
		// from legacy row diagnostics or probe a rejected batch with more writes.
		result.failed = append(result.failed, deliveryFailure{records: len(batch.indexes), err: err})
		d.terminalErr = err
		d.pending = nil
	}
	if d.terminalErr != nil {
		result.err = consumererror.NewPermanent(d.terminalErr)
	}
	return result
}

func (d *appendDelivery) settleAmbiguous(result *deliveryResult, err error) {
	err = fmt.Errorf("append outcome is ambiguous; an attempt may have committed: %w", err)
	result.ambiguous = append(result.ambiguous, deliveryFailure{records: len(d.pending.indexes), err: err})
	d.terminalErr = err
	d.pending = nil
}

func (d *appendDelivery) canReplayAt(err error, notBefore time.Time) bool {
	if !d.retry.Enabled || d.replays >= d.retry.MaxRetries || !retryableAfterAmbiguous(err) {
		return false
	}
	return notBefore.Before(d.replayStarted.Add(d.retry.MaxElapsedTime))
}

func appendOutcome(err error) scopedb.AppendOutcome {
	if failure, ok := errors.AsType[*scopedb.Error](err); ok {
		if failure.AppendDetails != nil {
			return failure.AppendDetails.Status
		}
		// SDK validation errors without AppendDetails precede dispatch.
		return scopedb.AppendOutcomeFailed
	}
	return scopedb.AppendOutcomeAmbiguous
}

func retryAppend(err error) error {
	retryable := consumererror.NewRetryableError(err)
	if failure, ok := errors.AsType[*scopedb.Error](err); ok && failure.RetryAfter > 0 {
		return exporterhelper.NewThrottleRetry(retryable, failure.RetryAfter)
	}
	return retryable
}
