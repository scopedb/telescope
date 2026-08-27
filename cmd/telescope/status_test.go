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
		QueueStorage: statusapi.IngestionQueueStorage{
			Available:      true,
			AllocatedBytes: 8192,
		},
		Signals: []statusapi.IngestionSignalStatus{{
			Signal:   "traces",
			State:    "degraded",
			Table:    "app.spans",
			Received: 12,
			Written:  10,
			Dropped:  2,
			InvalidItemsByReason: map[string]uint64{
				"mapped_row_too_large": 1,
				"mapping_cast_failed":  2,
			},
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
	assert.Contains(t, output.String(), "queue storage: 8 KiB allocated")
	assert.Contains(t, output.String(), "traces invalid: mapped_row_too_large=1, mapping_cast_failed=2")
	assert.Contains(t, output.String(), "destination unavailable")
}

func TestWriteStatusExplainsAcceptedItemsWithoutFinalOutcome(t *testing.T) {
	var output bytes.Buffer
	writeStatus(&output, statusapi.IngestionStatusResponse{
		State: "ready",
		Signals: []statusapi.IngestionSignalStatus{{
			Signal:   "traces",
			State:    "ready",
			Received: 2,
			Queue: statusapi.IngestionQueueStatus{
				Enabled:  true,
				Observed: true,
			},
		}},
	})

	assert.Contains(t, output.String(), "2 accepted items have no final outcome yet")
	assert.Contains(t, output.String(), "Collector batch processing")
}

func TestUnsettledRecordsRequiresAnObservedEmptyQueue(t *testing.T) {
	base := statusapi.IngestionSignalStatus{Received: 2}
	assert.Zero(t, unsettledRecords(base))

	base.Queue.Observed = true
	base.Queue.Size = 1
	assert.Zero(t, unsettledRecords(base))

	base.Queue.Size = 0
	assert.Equal(t, uint64(2), unsettledRecords(base))
}

func TestWriteStatusReportsUnavailableQueueStorage(t *testing.T) {
	var output bytes.Buffer
	writeStatus(&output, statusapi.IngestionStatusResponse{
		State:        "ready",
		QueueStorage: statusapi.IngestionQueueStorage{Error: "permission denied"},
	})

	assert.Contains(t, output.String(), "queue storage: unavailable (permission denied)")
}
