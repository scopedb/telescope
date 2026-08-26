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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusRegistryTracksLifecycleAndWrites(t *testing.T) {
	registry := NewStatusRegistry()
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	registry.now = func() time.Time { return now }
	cfg := createDefaultConfig().(*Config)

	registry.configure(signalLogs, cfg)
	registry.markReady(signalLogs)
	registry.recordWrite(signalLogs, 2, now.Add(-25*time.Millisecond), nil, false)
	registry.recordProbeSuccess(signalLogs, []string{"probe-1", "probe-2"})

	status := registry.Snapshot().Signals[signalLogs]
	assert.True(t, status.Ready)
	assert.Equal(t, defaultLogsTable, status.Table)
	assert.True(t, status.QueueEnabled)
	assert.Equal(t, int64(10_000), status.QueueCapacity)
	assert.Equal(t, now, status.LastWriteSuccess)
	assert.Equal(t, 25*time.Millisecond, status.LastWriteDuration)
	assert.Empty(t, status.LastError)
	assert.Equal(t, []string{"probe-1", "probe-2"}, status.LastProbeIDs)
	assert.Equal(t, now, status.LastProbeSuccess)

	now = now.Add(time.Second)
	registry.recordWrite(signalLogs, 3, now.Add(-10*time.Millisecond), &mappingError{
		reason: mappingReasonUnsupportedMetricType,
		err:    errors.New("invalid payload"),
	}, true)
	status = registry.Snapshot().Signals[signalLogs]
	assert.Equal(t, now, status.LastWriteFailure)
	assert.Equal(t, "invalid payload", status.LastError)
	assert.Equal(t, uint64(3), status.PermanentFailedRecords)
	assert.Equal(t, map[string]uint64{mappingReasonUnsupportedMetricType: 1}, status.InvalidItemsByReason)

	registry.markStopped(signalLogs)
	assert.False(t, registry.Snapshot().Signals[signalLogs].Ready)
}

func TestStatusRegistrySnapshotIsIndependent(t *testing.T) {
	registry := NewStatusRegistry()
	registry.configure(signalTraces, createDefaultConfig().(*Config))
	registry.recordProbeSuccess(signalTraces, []string{"probe-1"})

	snapshot := registry.Snapshot()
	status := snapshot.Signals[signalTraces]
	status.Table = "changed"
	status.LastProbeIDs[0] = "changed"
	status.InvalidItemsByReason["changed"] = 1
	snapshot.Signals[signalTraces] = status

	require.Equal(t, defaultTracesTable, registry.Snapshot().Signals[signalTraces].Table)
	require.Equal(t, []string{"probe-1"}, registry.Snapshot().Signals[signalTraces].LastProbeIDs)
	require.NotContains(t, registry.Snapshot().Signals[signalTraces].InvalidItemsByReason, "changed")
}
