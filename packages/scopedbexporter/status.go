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
	"sync"
	"time"
)

// DefaultStatusRegistry is shared by the embedded Collector and Telescope's
// ingestion status endpoint. Standalone exporter users can provide an isolated
// registry through NewFactoryWithStatus.
var DefaultStatusRegistry = NewStatusRegistry()

type StatusRegistry struct {
	mu      sync.RWMutex
	now     func() time.Time
	signals map[string]SignalRuntimeStatus
}

type StatusSnapshot struct {
	Signals map[string]SignalRuntimeStatus
}

type SignalRuntimeStatus struct {
	Signal                 string
	Table                  string
	QueueEnabled           bool
	QueueCapacity          int64
	QueueUnit              string
	Ready                  bool
	LastWriteAttempt       time.Time
	LastWriteSuccess       time.Time
	LastWriteFailure       time.Time
	LastWriteDuration      time.Duration
	LastError              string
	PermanentFailedRecords uint64
	InvalidItemsByReason   map[string]uint64
}

func NewStatusRegistry() *StatusRegistry {
	return &StatusRegistry{
		now:     func() time.Time { return time.Now().UTC() },
		signals: make(map[string]SignalRuntimeStatus, 3),
	}
}

func (r *StatusRegistry) configure(signal string, cfg *Config) {
	if r == nil || cfg == nil {
		return
	}
	status := SignalRuntimeStatus{
		Signal:               signal,
		Table:                cfg.tableForSignal(signal),
		InvalidItemsByReason: make(map[string]uint64),
	}
	if cfg.SendingQueue.HasValue() {
		status.QueueEnabled = true
		status.QueueCapacity = cfg.SendingQueue.Get().QueueSize
		status.QueueUnit = cfg.SendingQueue.Get().Sizer.String()
	}

	r.mu.Lock()
	if current, ok := r.signals[signal]; ok {
		status.Ready = current.Ready
		status.LastWriteAttempt = current.LastWriteAttempt
		status.LastWriteSuccess = current.LastWriteSuccess
		status.LastWriteFailure = current.LastWriteFailure
		status.LastWriteDuration = current.LastWriteDuration
		status.LastError = current.LastError
		status.PermanentFailedRecords = current.PermanentFailedRecords
		status.InvalidItemsByReason = cloneReasonCounts(current.InvalidItemsByReason)
	}
	r.signals[signal] = status
	r.mu.Unlock()
}

func (r *StatusRegistry) markReady(signal string) {
	r.update(signal, func(status *SignalRuntimeStatus) {
		status.Ready = true
		status.LastError = ""
	})
}

func (r *StatusRegistry) markStopped(signal string) {
	r.update(signal, func(status *SignalRuntimeStatus) {
		status.Ready = false
	})
}

func (r *StatusRegistry) recordWrite(signal string, records int, started time.Time, err error, permanent bool) {
	if r == nil {
		return
	}
	finished := r.now()
	r.update(signal, func(status *SignalRuntimeStatus) {
		status.LastWriteAttempt = finished
		status.LastWriteDuration = finished.Sub(started)
		if err == nil {
			status.LastWriteSuccess = finished
			status.LastError = ""
			return
		}
		status.LastWriteFailure = finished
		status.LastError = err.Error()
		if permanent && records > 0 {
			status.PermanentFailedRecords += uint64(records)
		}
		if reason, ok := mappingErrorReason(err); ok {
			if status.InvalidItemsByReason == nil {
				status.InvalidItemsByReason = make(map[string]uint64)
			}
			status.InvalidItemsByReason[reason]++
		}
	})
}

func (r *StatusRegistry) recordStartFailure(signal string, err error) {
	if r == nil || err == nil {
		return
	}
	r.update(signal, func(status *SignalRuntimeStatus) {
		status.Ready = false
		status.LastWriteFailure = r.now()
		status.LastError = err.Error()
	})
}

func (r *StatusRegistry) update(signal string, apply func(*SignalRuntimeStatus)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	status := r.signals[signal]
	status.Signal = signal
	apply(&status)
	r.signals[signal] = status
	r.mu.Unlock()
}

func (r *StatusRegistry) Snapshot() StatusSnapshot {
	snapshot := StatusSnapshot{Signals: make(map[string]SignalRuntimeStatus, 3)}
	if r == nil {
		return snapshot
	}
	r.mu.RLock()
	for signal, status := range r.signals {
		status.InvalidItemsByReason = cloneReasonCounts(status.InvalidItemsByReason)
		snapshot.Signals[signal] = status
	}
	r.mu.RUnlock()
	return snapshot
}

func cloneReasonCounts(counts map[string]uint64) map[string]uint64 {
	cloned := make(map[string]uint64, len(counts))
	for reason, count := range counts {
		cloned[reason] = count
	}
	return cloned
}
