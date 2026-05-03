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

const DefaultConfig = `
extensions:
  health_check:
    endpoint: ${env:TELESCOPE_HEALTH_ADDR}
  file_storage:
    directory: ${env:TELESCOPE_QUEUE_DIR}
    create_directory: true

receivers:
  otlp:
    protocols:
      grpc:
        endpoint: ${env:TELESCOPE_OTLP_GRPC_ADDR}
      http:
        endpoint: ${env:TELESCOPE_OTLP_HTTP_ADDR}

processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
  batch:
    timeout: 30s
    send_batch_size: 2000
    send_batch_max_size: 2000

exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    path: /v1/ingest
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    create_tables_if_not_exist: true
    schema_version: v1
    sending_queue:
      enabled: true
      storage: file_storage
      queue_size: 5000
      num_consumers: 1
    retry_on_failure:
      enabled: true
      initial_interval: 5s
      max_interval: 60s
      max_elapsed_time: 10m

service:
  extensions: [health_check, file_storage]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [scopedb]
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [scopedb]
    logs:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [scopedb]
`
