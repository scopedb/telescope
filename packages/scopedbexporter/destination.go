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
	"net"
	"time"

	scopedb "github.com/scopedb/goscopedb"
)

const defaultDestinationValidationTimeout = 30 * time.Second

func (e *dbExporter) validateDestination(ctx context.Context) error {
	timeout := e.cfg.Timeout.Timeout
	if timeout <= 0 {
		timeout = defaultDestinationValidationTimeout
	}

	validateCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := e.client.ValidateDestination(validateCtx, e.signal); err != nil {
		return fmt.Errorf("validate %s destination: %w", e.signal, err)
	}
	return nil
}

func isTransientDestinationError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}

	var scopeErr *scopedb.Error
	if !errors.As(err, &scopeErr) {
		return false
	}
	if scopeErr.Retryable {
		return true
	}
	return scopeErr.HTTPStatus == 0 && scopeErr.Kind != scopedb.ErrorKindConfigInvalid
}
