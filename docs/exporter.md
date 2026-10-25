# ScopeDB Exporter for OpenTelemetry Collector

`scopedbexporter` is the ScopeDB exporter used by Telescope and is available to custom OpenTelemetry Collector distributions through `scopedbexporter.NewFactory()`. The component is named `scopedb`. It converts Collector `pdata` into user-selected ScopeDB columns and writes NDJSON with the ScopeDB Go SDK `Table.AppendNDJSON` API.

For custom distributions, import `github.com/scopedb/telescope/scopedbexporter` and register `NewFactory()`. It keeps observation state private to each exporter instance. Telescope uses `NewFactoryWithRegistries` to share status and capture with its operational server; callers using that constructor must supply a separate registry pair for each named exporter component. There are no process-global registries.

## Configuration

```yaml
exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    tables:
      traces: observability.spans
    mappings:
      traces:
        ts: span.start_time
        trace_id: span.trace_id
        span_id: span.span_id
        service_name:
          source: resource.attributes["service.name"]
          cast: string
        operation: span.name
        duration_ns: span.duration_ns
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

A chunk succeeds only when ScopeDB reports `committed` and the inserted row count matches. A `rejected` result is retried only when the SDK marks it retryable. This includes connection failures proven to occur before dispatch and HTTP 429 responses without structured ingest details. Certificate verification errors and caller cancellation are terminal. A lost response after connection acquisition or an unstructured HTTP 503 remains `unknown`; an unavailable response alone cannot prove that no write committed. Collector honors `Retry-After` as a minimum delay, including when it exceeds the configured backoff interval. Each signal defaults to one queue consumer; `sending_queue.num_consumers` changes its concurrency. Consumers and replicas share the destination's request budget, so adding consumers can increase throttling.

By default, an `unknown` outcome is terminal: Telescope does not automatically replay those rows. This includes an interrupted request, write timeout, malformed acknowledgement, or an HTTP error without a structured rejection. Only the affected chunk is counted as unknown; unsent rows can still be delivered. `unknown` in ingestion status and `telescope_ingestion_unknown_total` count these records separately from `written` and `dropped`. Collector's raw send-failure counter also includes terminal unknown outcomes; use Telescope's counters to distinguish them.

Applications that accept possible duplicate rows can enable `unknown_retry` on the exporter (`delivery.unknown_retry` in `telescope run`):

```yaml
unknown_retry:
  enabled: true
  max_retries: 3
  max_elapsed_time: 5m
```

This policy defaults to disabled. It permits replay after transient unknown results, including unstructured HTTP 5xx, connection loss, and malformed success acknowledgements. It follows the SDK stream's opt-in boundary: client cancellation and timeouts after connection acquisition remain terminal, as do permanent HTTP errors. It does not change the SDK error classification or disable certificate verification. A later successful acknowledgement confirms the logical rows were written at least once; it does not establish how many copies exist. A later rejection cannot resolve an earlier unknown result.

Collector remains the retry scheduler and honors backoff and `Retry-After`. Telescope retains the exact mapped bytes of the pending unknown chunk. `max_retries` counts additional append attempts for previously unknown chunks across one dequeued request, including safe rejections received during replay. The elapsed budget starts at that request's first replayable unknown outcome and is shared by its chunks. No replay starts after expiry, and in-flight replay is limited to the remaining budget and the normal exporter timeout. A `Retry-After` value that cannot fit in the remaining budget ends replay without sending again. Retirement can otherwise occur later, when Collector's existing backoff wakes; the policy never shortens a server-requested delay. Both limits must be positive when enabled. The ordinary retry horizon or shutdown can stop delivery earlier.

`telescope_ingestion_unknown_replay_attempts_total` counts replay attempts, and `telescope_ingestion_unknown_replayed_records_total` counts the affected records once per replayed chunk in a queue delivery. These counters overlap confirmed writes or final unknown records; they are not additional final outcomes. While awaiting replay, records remain unsettled. If delivery ends without confirmation, the ambiguous chunk is counted as unknown even if its last attempt was rejected; an unsent suffix that exhausts the ordinary retry budget is counted separately as dropped.

Replay state and budgets are process-local. The persistent queue format remains the original OTLP request: recovery starts a new delivery budget and can replay previously settled chunks. These limits are not a lifetime bound across process restarts. Shared queue takeover and request deduplication are not provided.

Projection, JSON encoding, and row-size failures are isolated to the offending records. When ScopeDB returns a non-retryable `rejected` result with a complete, non-truncated row-error list, Telescope removes those rows and makes one append attempt for the remainder. It never bisects or repeatedly probes a rejected chunk. Rejections without a complete row list remain permanent for the uncommitted records. During a running export, Collector retries only the remaining records, excluding confirmed, rejected, and terminal unknown chunks.

The OpenTelemetry Collector `exporterhelper` sending queue is the only persistent queue and retry owner. Retryable requests have no elapsed-time expiry by default; configure `retry_on_failure.max_elapsed_time` for a finite budget. A full bounded queue reports enqueue failures. The queue stores the original OTLP request until export finishes: a crash or restart between a partial append and queue acknowledgement can replay that request, including previously committed or unknown chunks. This is an at-least-once crash boundary, not an exactly-once guarantee. Counters describe observations in the current process and cannot resolve a crash's missing acknowledgement.

The exporter `timeout` owns each attempt's context, including mapping and all append chunks. Telescope explicitly disables the SDK's independent `WriteTimeout`, so a configured 60-second attempt is not cut short at the SDK's default 30 seconds. The embedded profile defaults to 30 seconds; the reusable exporter's default is 10 seconds. A timeout after dispatch is unknown; work not yet dispatched remains eligible for retry.

`compression` accepts `zstd` (default) or `gzip`. ScopeDB Go SDK `v0.7.0` applies it to direct append requests and supplies outcome classification and `Retry-After` parsing. Telescope's ScopeDB SDK clients refuse HTTPS-to-HTTP redirects.
