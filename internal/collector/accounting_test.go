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

package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
	"github.com/scopedb/telescope/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
)

func TestPartialAppendAccountingAcrossRetries(t *testing.T) {
	for _, queue := range []string{"none", "memory", "persistent"} {
		t.Run(queue, func(t *testing.T) {
			for _, last := range []string{"committed", "permanent", "exhausted", "unknown"} {
				t.Run(last, func(t *testing.T) {
					var suffixAttempts atomic.Int32
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := decodeRecoveryAppendBody(r)
						require.NoError(t, err)
						var row struct {
							Value string `json:"value"`
						}
						require.NoError(t, json.Unmarshal(body, &row))
						w.Header().Set("Content-Type", "application/json")
						switch row.Value {
						case "committed":
							fmt.Fprint(w, `{"status":"committed","num_rows_inserted":1}`)
						case "unknown":
							http.Error(w, "connection outcome unavailable", http.StatusServiceUnavailable)
						case "suffix":
							attempt := suffixAttempts.Add(1)
							if attempt == 1 {
								w.WriteHeader(http.StatusServiceUnavailable)
								fmt.Fprint(w, `{"message":"temporary rejection","retryable":true,"details":{"status":"rejected"}}`)
								return
							}
							switch last {
							case "committed":
								fmt.Fprint(w, `{"status":"committed","num_rows_inserted":1}`)
							case "permanent":
								w.WriteHeader(http.StatusBadRequest)
								fmt.Fprint(w, `{"message":"permanent rejection","retryable":false,"details":{"status":"rejected"}}`)
							case "exhausted":
								w.Header().Set("Retry-After", "10")
								w.WriteHeader(http.StatusTooManyRequests)
								fmt.Fprint(w, `{"message":"quota","retryable":true,"details":{"status":"rejected"}}`)
							case "unknown":
								http.Error(w, "outcome unavailable", http.StatusServiceUnavailable)
							}
						}
					}))
					defer backend.Close()
					address, metricsAddress := freeTCPAddress(t), freeTCPAddress(t)
					config := retryOutcomeCollectorConfig(backend.URL, address, metricsAddress)
					config = strings.Replace(config, "compression: zstd", "compression: zstd\n    append_batch:\n      max_rows: 1", 1)
					config = strings.Replace(config, "max_elapsed_time: 100ms", "max_elapsed_time: 2s", 1)
					switch queue {
					case "none":
						config = strings.Replace(config, "enabled: true", "enabled: false", 1)
					case "persistent":
						config = fmt.Sprintf("extensions:\n  file_storage:\n    directory: %q\n", t.TempDir()) + config
						config = strings.Replace(config, "service:\n", "service:\n  extensions: [file_storage]\n", 1)
						config = strings.Replace(config, "sizer: bytes", "storage: file_storage\n      sizer: bytes", 1)
					}
					statuses, captures := scopedbexporter.NewStatusRegistry(), scopedbexporter.NewCaptureRegistry()
					runtime := startCollectorWithSettings(t, newSettings("yaml:"+config, "test", true, statuses, captures))
					defer runtime.stop(t)
					t.Setenv("TELESCOPE_INTERNAL_METRICS_URL", "http://"+metricsAddress+"/metrics")
					operational := httptest.NewServer(statusapi.NewWithRegistries("test", "test", statuses, captures))
					defer operational.Close()
					logs := plog.NewLogs()
					records := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
					for _, value := range []string{"committed", "unknown", "suffix"} {
						records.AppendEmpty().Body().SetStr(value)
					}
					body, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
					require.NoError(t, err)
					response, err := http.Post("http://"+address+"/v1/logs", "application/x-protobuf", bytes.NewReader(body))
					require.NoError(t, err)
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					written, unknown, dropped := uint64(1), uint64(1), uint64(1)
					switch last {
					case "committed":
						written = 2
						dropped = 0
					case "unknown":
						unknown = 2
						dropped = 0
					}
					require.EventuallyWithT(t, func(c *assert.CollectT) {
						response, err := http.Get(operational.URL + "/v1/ingestion/status")
						if !assert.NoError(c, err) {
							return
						}
						defer response.Body.Close()
						var status statusapi.IngestionStatusResponse
						if !assert.NoError(c, json.NewDecoder(response.Body).Decode(&status)) || !assert.Len(c, status.Signals, 1) {
							return
						}
						got := status.Signals[0]
						assert.Equal(c, written, got.Written)
						assert.Equal(c, unknown, got.Unknown)
						assert.Equal(c, dropped, got.Dropped)
						assert.Zero(c, got.Queue.Size)
					}, 5*time.Second, 20*time.Millisecond)
					require.Equal(t, int32(2), suffixAttempts.Load())
				})
			}
		})
	}
}
