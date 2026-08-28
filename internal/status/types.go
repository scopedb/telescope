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

package status

import "time"

type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version,omitempty"`
}

type IngestionStatusResponse struct {
	Version           string                     `json:"version,omitempty"`
	ConfigDigest      string                     `json:"config_digest,omitempty"`
	State             string                     `json:"state"`
	GeneratedAt       time.Time                  `json:"generated_at"`
	Listeners         IngestionListeners         `json:"listeners"`
	InternalTelemetry IngestionInternalTelemetry `json:"internal_telemetry"`
	QueueStorage      IngestionQueueStorage      `json:"queue_storage"`
	Signals           []IngestionSignalStatus    `json:"signals"`
}

type IngestionListeners struct {
	GRPC string `json:"grpc"`
	HTTP string `json:"http"`
}

type IngestionInternalTelemetry struct {
	Available bool   `json:"available"`
	Endpoint  string `json:"endpoint"`
	Error     string `json:"error,omitempty"`
}

type IngestionQueueStorage struct {
	Available      bool   `json:"available"`
	AllocatedBytes int64  `json:"allocated_bytes"`
	Error          string `json:"error,omitempty"`
}

type IngestionSignalStatus struct {
	Signal               string               `json:"signal"`
	State                string               `json:"state"`
	Ready                bool                 `json:"ready"`
	DestinationVerified  bool                 `json:"destination_verified"`
	Table                string               `json:"table,omitempty"`
	Received             uint64               `json:"received"`
	ReceiverFailed       uint64               `json:"receiver_failed"`
	ReceiverRefused      uint64               `json:"receiver_refused"`
	Written              uint64               `json:"written"`
	Dropped              uint64               `json:"dropped"`
	RetryExhausted       uint64               `json:"retry_exhausted"`
	EnqueueFailed        uint64               `json:"enqueue_failed"`
	PermanentRejected    uint64               `json:"permanent_rejected"`
	InvalidItemsByReason map[string]uint64    `json:"invalid_items_by_reason"`
	Queue                IngestionQueueStatus `json:"queue"`
	LastWriteAttempt     *time.Time           `json:"last_write_attempt,omitempty"`
	LastWriteSuccess     *time.Time           `json:"last_write_success,omitempty"`
	LastWriteFailure     *time.Time           `json:"last_write_failure,omitempty"`
	LastWriteDurationMS  int64                `json:"last_write_duration_ms,omitempty"`
	LastError            string               `json:"last_error,omitempty"`
	LastProbeIDs         []string             `json:"last_probe_ids,omitempty"`
	LastProbeSuccess     *time.Time           `json:"last_probe_success,omitempty"`
}

type IngestionQueueStatus struct {
	Enabled  bool   `json:"enabled"`
	Observed bool   `json:"observed"`
	Size     int64  `json:"size"`
	Capacity int64  `json:"capacity"`
	Unit     string `json:"unit,omitempty"`
}
