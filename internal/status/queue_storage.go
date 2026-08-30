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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type queueStorageReader interface {
	AllocatedBytes() (int64, error)
}

type directoryQueueStorageReader struct {
	directory string
}

func newDirectoryQueueStorageReader() *directoryQueueStorageReader {
	return &directoryQueueStorageReader{directory: strings.TrimSpace(os.Getenv("TELESCOPE_QUEUE_DIR"))}
}

func (r *directoryQueueStorageReader) AllocatedBytes() (int64, error) {
	if r == nil || r.directory == "" {
		return 0, fmt.Errorf("TELESCOPE_QUEUE_DIR is not configured")
	}

	var total int64
	err := filepath.WalkDir(r.directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat queue file %s: %w", path, err)
		}
		total += allocatedFileBytes(info)
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measure queue directory %s: %w", r.directory, err)
	}
	return total, nil
}
