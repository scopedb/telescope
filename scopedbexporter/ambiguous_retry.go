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
	"time"

	scopedb "github.com/scopedb/goscopedb"
)

// AmbiguousRetryConfig bounds opt-in replay of writes that may have committed.
// Limits apply to one dequeued OTLP request and restart after crash recovery.
type AmbiguousRetryConfig struct {
	Enabled        bool          `mapstructure:"enabled" yaml:"enabled"`
	MaxRetries     int           `mapstructure:"max_retries" yaml:"max_retries"`
	MaxElapsedTime time.Duration `mapstructure:"max_elapsed_time" yaml:"max_elapsed_time"`
}

func (c AmbiguousRetryConfig) Validate() error {
	if c.MaxRetries < 0 || c.MaxElapsedTime < 0 || (c.Enabled && (c.MaxRetries == 0 || c.MaxElapsedTime == 0)) {
		return errors.New("ambiguous_retry requires positive max_retries and max_elapsed_time when enabled")
	}
	return nil
}

// Follow the SDK's replay eligibility, including safe rejections after an
// ambiguous attempt. A later rejection does not resolve the earlier write.
func retryableAfterAmbiguous(err error) bool {
	failure, ok := errors.AsType[*scopedb.Error](err)
	if !ok || failure.AppendDetails == nil || (failure.ServerRetryable != nil && !*failure.ServerRetryable) {
		return false
	}
	if failure.AppendDetails.Status == scopedb.AppendOutcomeFailed {
		return failure.Retryable
	}
	if failure.AppendDetails.Status != scopedb.AppendOutcomeAmbiguous || errors.Is(err, context.Canceled) || failure.WriteDetails != nil {
		return false
	}
	return failure.HTTPStatus == 0 || (failure.HTTPStatus >= 200 && failure.HTTPStatus < 300) || failure.HTTPStatus == 408 || failure.HTTPStatus == 429 || failure.HTTPStatus >= 500
}
