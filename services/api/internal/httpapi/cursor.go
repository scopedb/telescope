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

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type searchCursor struct {
	Source    string     `json:"source"`
	Start     *time.Time `json:"start,omitempty"`
	FrozenEnd time.Time  `json:"frozen_end"`
	LastTS    time.Time  `json:"last_ts"`
	LastRowID string     `json:"last_row_id"`
}

func encodeSearchCursor(cursor searchCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeSearchCursor(raw string) (searchCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return searchCursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var cursor searchCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return searchCursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	if cursor.Source == "" || cursor.LastTS.IsZero() || cursor.LastRowID == "" || cursor.FrozenEnd.IsZero() {
		return searchCursor{}, fmt.Errorf("cursor is incomplete")
	}
	return cursor, nil
}
