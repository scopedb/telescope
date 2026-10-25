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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
)

const defaultOperationalEndpoint = "http://127.0.0.1:8080"

func requestCapture(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	signal string,
	limit int,
	timeout time.Duration,
) ([]byte, int, error) {
	captureURL, err := appendEndpointPath(endpoint, "/v1/ingestion/capture")
	if err != nil {
		return nil, 0, fmt.Errorf("invalid capture endpoint %q: %w", endpoint, err)
	}
	parsed, err := url.Parse(captureURL)
	if err != nil {
		return nil, 0, fmt.Errorf("parse capture endpoint: %w", err)
	}
	query := parsed.Query()
	query.Set("signal", signal)
	query.Set("limit", strconv.Itoa(limit))
	query.Set("timeout", timeout.String())
	parsed.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build capture request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("capture %s: %w", signal, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if response.StatusCode == http.StatusRequestTimeout {
			return nil, 0, fmt.Errorf(
				"capture %s: no new exporter input observed within %s; start capture before generating traffic and allow for Collector batch delay",
				signal,
				timeout,
			)
		}
		return nil, 0, fmt.Errorf("capture %s: %s: %s", signal, response.Status, strings.TrimSpace(string(body)))
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read captured %s sample: %w", signal, err)
	}
	if len(payload) == 0 {
		return nil, 0, fmt.Errorf("capture %s returned an empty sample", signal)
	}
	records, _ := strconv.Atoi(response.Header.Get("X-Telescope-Records"))
	return payload, records, nil
}

func appendEndpointPath(base string, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("endpoint must include scheme and host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("endpoint must not include a query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String(), nil
}

func readIngestionStatus(ctx context.Context, client *http.Client, endpoint string) (statusapi.IngestionStatusResponse, error) {
	var status statusapi.IngestionStatusResponse
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return status, fmt.Errorf("invalid status endpoint %q", endpoint)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1/ingestion/status"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return status, err
	}
	response, err := client.Do(request)
	if err != nil {
		return status, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return status, fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return status, fmt.Errorf("decode response: %w", err)
	}
	return status, nil
}
