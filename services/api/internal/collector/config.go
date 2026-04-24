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
    timeout: 5s
    send_batch_size: 512

exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    path: /v1/ingest
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    env: ${env:TELESCOPE_ENV}
    create_tables_if_not_exist: true
    schema_version: v1
    sending_queue:
      enabled: true
      storage: file_storage
      queue_size: 1000
      num_consumers: 2
    retry_on_failure:
      enabled: true
      initial_interval: 1s
      max_interval: 30s
      max_elapsed_time: 5m

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
