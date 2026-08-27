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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckIngestionDestinationsChecksOnlyEnabledSignals(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		name := strings.TrimPrefix(r.URL.Path, "/v1/databases/scopedb/schemas/public/tables/")
		column := map[string]string{"check_traces": "name"}[name]
		require.NotEmpty(t, column)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"database":     "scopedb",
			"schema":       "public",
			"name":         name,
			"columns":      []map[string]any{{"name": column, "data_type": "string"}},
			"partition_by": []string{},
			"cluster_by":   []string{},
			"distinct_on":  map[string]any{"on": []string{}, "by": []string{}},
		}))
	}))
	defer server.Close()

	err := CheckIngestionDestinations(context.Background(), server.URL, "test-key", IngestionConfig{
		Signals: IngestionSignalsConfig{
			Traces: SignalIngestionConfig{
				Table:   "public.check_traces",
				Mapping: shorthandMapping(map[string]string{"name": "span.name"}),
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), requests.Load())
}

func TestIngestionConfigRequiresOneCompleteSignal(t *testing.T) {
	err := (IngestionConfig{}).Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "at least one signal is required")

	err = (IngestionConfig{Signals: IngestionSignalsConfig{
		Traces: SignalIngestionConfig{Mapping: shorthandMapping(map[string]string{"name": "span.nam"})},
	}}).Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "signals.traces.table is required")
	assert.ErrorContains(t, err, `did you mean "span.name"?`)
}
