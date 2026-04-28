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
	"net/url"

	"go.opentelemetry.io/collector/consumer/consumererror"
)

type httpStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *httpStatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("ingest request failed with %s", e.Status)
	}
	return fmt.Sprintf("ingest request failed with %s: %s", e.Status, e.Body)
}

func classifyHTTPStatus(statusCode int, err error) error {
	switch {
	case statusCode == 408 || statusCode == 409 || statusCode == 425 || statusCode == 429:
		return consumererror.NewRetryableError(err)
	case statusCode >= 500 && statusCode <= 599:
		return consumererror.NewRetryableError(err)
	case statusCode == 400 || statusCode == 401 || statusCode == 403 || statusCode == 404 || statusCode == 422:
		return consumererror.NewPermanent(err)
	case statusCode >= 400 && statusCode <= 499:
		return consumererror.NewPermanent(err)
	default:
		return err
	}
}

func classifyRequestError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return consumererror.NewRetryableError(err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return consumererror.NewRetryableError(err)
		}
		return consumererror.NewRetryableError(err)
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return consumererror.NewRetryableError(err)
	}

	return err
}
