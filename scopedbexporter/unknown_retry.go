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
	"errors"
	"fmt"
	"slices"
	"time"

	scopedb "github.com/scopedb/goscopedb"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
)

// UnknownRetryConfig opts into replay that may duplicate previously committed
// rows. Budgets apply to one dequeued request and restart after persisted replay.
type UnknownRetryConfig struct {
	Enabled        bool          `mapstructure:"enabled" yaml:"enabled"`
	MaxRetries     int           `mapstructure:"max_retries" yaml:"max_retries"`
	MaxElapsedTime time.Duration `mapstructure:"max_elapsed_time" yaml:"max_elapsed_time"`
}

func (c UnknownRetryConfig) Validate() error {
	if c.MaxRetries < 0 || c.MaxElapsedTime < 0 || (c.Enabled && (c.MaxRetries == 0 || c.MaxElapsedTime == 0)) {
		return errors.New("unknown_retry requires positive max_retries and max_elapsed_time when enabled")
	}
	return nil
}

// A pending unknown chunk is always the prefix of the remaining OTLP items.
// Keep its exact mapped bytes until resolved; later rejections cannot prove
// whether a previous unknown attempt committed.
type unknownChunk struct {
	body     []byte
	rows     int
	err      error
	replayed bool
}

type unknownRetryState struct {
	config   UnknownRetryConfig
	started  time.Time
	retries  int
	pending  *unknownChunk
	exporter *dbExporter
}

func (s *unknownRetryState) canReplay(ctx context.Context) bool {
	return s != nil && s.config.Enabled && ctx.Err() == nil &&
		s.retries < s.config.MaxRetries && (s.started.IsZero() || time.Since(s.started) < s.config.MaxElapsedTime)
}

func (s *unknownRetryState) canRetryAfter(ctx context.Context, err error) bool {
	if !s.canReplay(ctx) {
		return false
	}
	if e, ok := errors.AsType[*scopedb.Error](err); ok && !s.started.IsZero() && e.RetryAfter > 0 {
		return time.Until(s.started.Add(s.config.MaxElapsedTime)) > e.RetryAfter
	}
	return true
}

func (s *unknownRetryState) retain(ctx context.Context, body []byte, rows int, err error) bool {
	if !s.canReplay(ctx) || !replayableUnknown(err) {
		return false
	}
	if s.started.IsZero() {
		s.started = time.Now()
	}
	if !s.canRetryAfter(ctx, err) {
		return false
	}
	s.pending = &unknownChunk{body: slices.Clone(body), rows: rows, err: err}
	return true
}

// Request timeouts may be replayed, but cancellation remains terminal. The
// delivery context and replay budget are checked separately from this error.
func replayableUnknown(err error) bool {
	e, ok := errors.AsType[*scopedb.Error](err)
	if !ok || e.AppendDetails == nil || e.AppendDetails.Status != scopedb.AppendStateUnknown {
		return false
	}
	if errors.Is(err, context.Canceled) || (e.WriteDetails != nil && e.WriteDetails.Reason != scopedb.WriteErrorReasonTimeout) {
		return false
	}
	return e.HTTPStatus == 0 || (e.HTTPStatus >= 200 && e.HTTPStatus < 300) || e.HTTPStatus == 408 || e.HTTPStatus == 429 || e.HTTPStatus >= 500
}

func retryableAfterUnknown(err error) bool {
	e, ok := errors.AsType[*scopedb.Error](err)
	return replayableUnknown(err) || (ok && e.AppendDetails != nil && e.AppendDetails.Status == scopedb.AppendStateRejected && e.Retryable)
}

func unknownRetryError(err error) error {
	retryable := consumererror.NewRetryableError(err)
	if e, ok := errors.AsType[*scopedb.Error](err); ok && e.RetryAfter > 0 {
		return exporterhelper.NewThrottleRetry(retryable, e.RetryAfter)
	}
	return retryable
}

func unresolvedUnknown(err error) error {
	return &scopedb.Error{
		Kind:          scopedb.ErrorKindAppendRowsFailed,
		Message:       fmt.Sprintf("an earlier append may have committed; final attempt: %s", err),
		AppendDetails: &scopedb.AppendErrorDetails{Status: scopedb.AppendStateUnknown},
	}
}

func (s *unknownRetryState) recordReplay() {
	s.retries++
	s.exporter.statuses.update(s.exporter.signal, func(status *SignalRuntimeStatus) {
		status.UnknownReplayAttempts++
		if !s.pending.replayed {
			status.UnknownReplayedRecords += uint64(s.pending.rows)
		}
	})
	s.pending.replayed = true
}
