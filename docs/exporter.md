# ScopeDB Exporter for OpenTelemetry Collector

`scopedbexporter` is the ScopeDB exporter used by Telescope and is available to custom OpenTelemetry Collector distributions through `scopedbexporter.NewFactory()`. The component is named `scopedb`. It converts Collector `pdata` into user-selected ScopeDB columns and writes NDJSON with the ScopeDB Go SDK `Table.AppendNDJSON` API.

The exporter does not impose a fixed telemetry schema. Each mapping entry projects one destination column from OpenTelemetry input, and only mapped columns are serialized and stored.

For custom distributions, import `github.com/scopedb/telescope/scopedbexporter` and register `NewFactory()`. It keeps observation state private to each exporter instance. Telescope uses `NewFactoryWithRegistries` to share status and capture with its operational server; callers using that constructor must supply a separate registry pair for each named exporter component. There are no process-global registries.

## Configuration

```yaml
exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    tables:
      logs: observability.events
      traces: observability.spans
      metrics: observability.metric_points
    mappings:
      logs:
        ts: log.timestamp
        service_name:
          sources:
            - resource.attributes["service.name"]
            - resource.attributes["service"]
          default: unknown
          cast: string
        level: log.severity_text
        message: log.message
        labels: log.attributes
      traces:
        ts: span.start_time
        trace_id: span.trace_id
        span_id: span.span_id
        service_name: resource.attributes["service.name"]
        operation: span.name
        duration_ns: span.duration_ns
        labels: span.attributes
      metrics:
        ts: datapoint.timestamp
        service_name: resource.attributes["service.name"]
        metric: metric.name
        int_value: datapoint.int_value
        double_value: datapoint.double_value
        distribution: datapoint.distribution
        labels: datapoint.attributes
    compression: zstd
    timeout: 30s
    append_batch:
      max_rows: 8000
      max_bytes: 4194304
    retry_on_failure:
      enabled: true
      initial_interval: 5s
      max_interval: 60s
      max_elapsed_time: 0s
    sending_queue:
      enabled: true
      storage: file_storage
      sizer: bytes
      queue_size: 536870912
      num_consumers: 1
```

`tables` and `mappings` are both required for each enabled signal. Tables must already exist. Startup validates local configuration without contacting ScopeDB, allowing receivers and persistent queue recovery to start during an outage.

Use the [mapping contract](table-management.md#mapping-model) for rule syntax, casts, and missing values, and the [source reference](ingestion-compatibility.md) for supported OTLP fields and limitations. The Telescope CLI's `inspect`, `preview`, `plan`, and `validate` commands use the same mapping implementation as this exporter. See [operations](operations.md) for sample capture and diagnostics.

## Append and Retry Semantics

The writer uses synchronous `Table.AppendNDJSON`, one ScopeDB request per chunk. `append_batch.max_rows` and `append_batch.max_bytes` cap each request after mapping; bytes refer to uncompressed NDJSON, including newlines. Zero selects the protocol limit (200,000 rows or 8 MiB). The embedded profile uses 8,000 rows and 4 MiB. Use the upstream Collector batch processor to combine input records; `sending_queue.batch` is not supported. This keeps one completion boundary per queued request, including partial retries. A single mapped row above the configured byte cap is a permanent local rejection.

A chunk succeeds only when ScopeDB reports `committed` and the inserted row count matches. A structured `rejected` result is retried only when the SDK marks it retryable. Collector honors `Retry-After` as a minimum delay, including when it exceeds the configured backoff interval. Each signal defaults to one queue consumer; `sending_queue.num_consumers` changes its concurrency. Consumers and replicas share the destination's request budget, so adding consumers can increase throttling.

An `unknown` outcome is terminal: Telescope does not automatically replay those rows. This includes an interrupted request, write timeout, malformed acknowledgement, or an HTTP error without a structured rejection. Only the affected chunk is counted as unknown; unsent rows can still be delivered. `unknown` in ingestion status and `telescope_ingestion_unknown_total` count these records separately from `written` and `dropped`. Collector's raw send-failure counter also includes terminal unknown outcomes; use Telescope's counters to distinguish them.

Projection, JSON encoding, and row-size failures are isolated to the offending records. When ScopeDB returns a non-retryable `rejected` result with a complete, non-truncated row-error list, Telescope removes those rows and makes one append attempt for the remainder. It never bisects or repeatedly probes a rejected chunk. Rejections without a complete row list remain permanent for the uncommitted records. During a running export, Collector retries only the remaining records, excluding confirmed, rejected, and unknown chunks.

The OpenTelemetry Collector `exporterhelper` sending queue is the only persistent queue and retry owner. Retryable requests have no elapsed-time expiry by default; configure `retry_on_failure.max_elapsed_time` for a finite budget. A full bounded queue reports enqueue failures. The queue stores the original OTLP request until export finishes: a crash or restart between a partial append and queue acknowledgement can replay that request, including previously committed or unknown chunks. This is an at-least-once crash boundary, not an exactly-once guarantee. Counters describe observations in the current process and cannot resolve a crash's missing acknowledgement.

The exporter `timeout` owns each attempt's context, including mapping and all append chunks. Telescope explicitly disables the SDK's independent `WriteTimeout`, so a configured 60-second attempt is not cut short at the SDK's default 30 seconds. The embedded profile defaults to 30 seconds; the reusable exporter's default is 10 seconds. A timeout after dispatch is unknown; work not yet dispatched remains eligible for retry.

`compression` accepts `zstd` (default) or `gzip`. ScopeDB Go SDK `v0.7.0` applies it to direct append requests and supplies outcome classification and `Retry-After` parsing. Telescope's ScopeDB SDK clients refuse HTTPS-to-HTTP redirects.
