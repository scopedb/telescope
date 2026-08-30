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

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoryQueueStorageReader(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "queue.db")
	require.NoError(t, os.WriteFile(path, make([]byte, 4096), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)

	allocated, err := (&directoryQueueStorageReader{directory: directory}).AllocatedBytes()

	require.NoError(t, err)
	assert.Equal(t, allocatedFileBytes(info), allocated)
}

func TestDirectoryQueueStorageReaderAllowsMissingDirectory(t *testing.T) {
	allocated, err := (&directoryQueueStorageReader{directory: filepath.Join(t.TempDir(), "missing")}).AllocatedBytes()

	require.NoError(t, err)
	assert.Zero(t, allocated)
}

func TestDirectoryQueueStorageReaderRequiresConfiguration(t *testing.T) {
	_, err := (&directoryQueueStorageReader{}).AllocatedBytes()

	require.Error(t, err)
	assert.ErrorContains(t, err, "TELESCOPE_QUEUE_DIR")
}
