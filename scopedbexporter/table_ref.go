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
	"fmt"
	"strings"
)

type tableRef struct {
	Database string
	Schema   string
	Table    string
}

func parseTableRef(raw string) (tableRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tableRef{}, fmt.Errorf("table route is required")
	}

	parts := strings.Split(raw, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return tableRef{}, fmt.Errorf("table route must be table, schema.table, or database.schema.table")
	}

	for _, part := range parts {
		if !tablePartPattern.MatchString(part) {
			return tableRef{}, fmt.Errorf("table route must be table, schema.table, or database.schema.table")
		}
	}

	switch len(parts) {
	case 1:
		return tableRef{Table: parts[0]}, nil
	case 2:
		return tableRef{Schema: parts[0], Table: parts[1]}, nil
	default:
		return tableRef{Database: parts[0], Schema: parts[1], Table: parts[2]}, nil
	}
}

func (r tableRef) String() string {
	parts := make([]string, 0, 3)
	if r.Database != "" {
		parts = append(parts, r.Database)
	}
	if r.Schema != "" {
		parts = append(parts, r.Schema)
	}
	parts = append(parts, r.Table)
	return strings.Join(parts, ".")
}
