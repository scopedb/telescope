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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	statusapi "github.com/scopedb/telescope/internal/status"
	"github.com/stretchr/testify/assert"
)

func TestReadIngestionStatusAcceptsBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ingestion/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(statusapi.IngestionStatusResponse{State: "ready"})
	}))
	defer server.Close()

	status, err := readIngestionStatus(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("readIngestionStatus() error = %v", err)
	}
	assert.Equal(t, "ready", status.State)
}

func TestWriteStatus(t *testing.T) {
	var output bytes.Buffer
	writeStatus(&output, statusapi.IngestionStatusResponse{
		State: "degraded",
		Signals: []statusapi.IngestionSignalStatus{{
			Signal:   "traces",
			State:    "degraded",
			Table:    "app.spans",
			Received: 12,
			Written:  10,
			Dropped:  2,
			Queue: statusapi.IngestionQueueStatus{
				Enabled:  true,
				Observed: true,
				Size:     256,
				Capacity: 1024,
				Unit:     "bytes",
			},
			LastError: "destination unavailable",
		}},
	})

	assert.Contains(t, output.String(), "state: degraded")
	assert.Contains(t, output.String(), "traces")
	assert.Contains(t, output.String(), "DROPPED")
	assert.Contains(t, output.String(), "256 B/1 KiB")
	assert.Contains(t, output.String(), "destination unavailable")
}
