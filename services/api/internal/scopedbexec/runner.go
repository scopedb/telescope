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

package scopedbexec

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	scopedb "github.com/scopedb/scopedb-sdk/go"
)

type Runner struct {
	client  *scopedb.Client
	timeout time.Duration
}

func New(endpoint string, apiKey string, timeout time.Duration) *Runner {
	return &Runner{
		client: scopedb.NewClient(&scopedb.Config{
			Endpoint:    endpoint,
			APIKey:      apiKey,
			Compression: scopedb.CompressionZstd,
		}),
		timeout: timeout,
	}
}

func (r *Runner) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	r.client.Close()
	return nil
}

func (r *Runner) Query(ctx context.Context, statement string) ([]map[string]any, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("nil scopedb runner")
	}

	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	stmt := r.client.Statement(statement)
	if r.timeout > 0 {
		stmt.ExecTimeout = r.timeout.String()
	}

	resultSet, err := stmt.Execute(ctx)
	if err != nil {
		return nil, err
	}
	return decodeRows(resultSet)
}

func decodeRows(resultSet *scopedb.ResultSet) ([]map[string]any, error) {
	if resultSet == nil || len(resultSet.Schema) == 0 {
		return nil, nil
	}

	values, err := resultSet.ToValues()
	if err != nil {
		return nil, err
	}

	rows := make([]map[string]any, 0, len(values))
	for _, valueList := range values {
		row := make(map[string]any, len(resultSet.Schema))
		for i, field := range resultSet.Schema {
			if i >= len(valueList) {
				row[field.Name] = nil
				continue
			}
			row[field.Name] = normalizeValue(field.Type, valueList[i])
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func normalizeValue(dataType scopedb.DataType, value any) any {
	if value == nil {
		return nil
	}

	switch dataType {
	case scopedb.ArrayDataType, scopedb.ObjectDataType, scopedb.AnyDataType:
		raw, ok := value.(string)
		if !ok || raw == "" {
			return value
		}

		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return value
		}
		return decoded
	default:
		return value
	}
}
