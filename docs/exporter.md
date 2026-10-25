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

Each synchronous `Table.AppendNDJSON` call sends one mapped chunk. `append_batch.max_rows` and `max_bytes` cap rows and uncompressed NDJSON bytes, including newlines. Zero uses the protocol limits of 200,000 rows and 8 MiB; `telescope run` defaults to 8,000 rows and 4 MiB. Use the upstream batch processor to combine input; `sending_queue.batch` is unsupported because a queued request must retain one completion boundary across partial retries.

| Append result | Delivery behavior |
| --- | --- |
| `committed` with the expected inserted count | Count as written. |
| Retryable `rejected` | Retry through Collector. Includes failures proven to precede dispatch and HTTP 429 without structured ingest details. |
| Permanent rejection | Count as dropped. Local projection, encoding, and oversized-row failures affect only that record. |
| `unknown` | Count separately from written and dropped; terminal unless eligible for the opt-in policy below. Includes connection loss after acquisition, unstructured HTTP 503, and malformed or mismatched acknowledgements. |

A complete, non-truncated row-error list in a permanent ScopeDB rejection allows one isolation pass: remove the rejected rows and attempt the remainder once. Incomplete details fail the uncommitted chunk. During a running export, retries exclude confirmed, permanently rejected, and terminal unknown records; unsent rows remain eligible for delivery.

Collector's sending queue owns persistence and retry scheduling. It honors `Retry-After` as a minimum delay, even above the configured backoff interval. The default is one consumer per signal and unlimited elapsed retries; set `retry_on_failure.max_elapsed_time` to expire safely retryable records. A full queue reports enqueue failures. Consumers and replicas share the destination's request quota, so added concurrency can increase throttling.

The exporter `timeout` covers mapping and all chunks in an attempt; the SDK's independent write deadline is disabled. Defaults are 10 seconds for `NewFactory()` and 30 seconds for `telescope run`. Cancellation and certificate verification failures are terminal. A timeout after connection acquisition is unknown; work not dispatched can still retry. `compression` accepts `zstd` (default) or `gzip`. SDK clients refuse HTTPS-to-HTTP redirects.

### Opt-in unknown replay

Applications that accept possible duplicates can enable `unknown_retry` (`delivery.unknown_retry` in `telescope run`):

```yaml
unknown_retry:
  enabled: true
  max_retries: 20
  max_elapsed_time: 5m
```

This defaults to disabled, with limits of 20 retries and five minutes when enabled. Eligible outcomes include unstructured HTTP 5xx, connection loss, attempt timeouts, and malformed success acknowledgements. Cancellation and permanent HTTP errors remain terminal. A new attempt gets a fresh attempt timeout but shares the remaining replay budget. The policy preserves the SDK classification and certificate verification.

Telescope retains the pending chunk's exact mapped bytes while Collector schedules retries. Both limits must be positive: `max_retries` counts additional attempts for previously unknown chunks, including safe rejections during replay; elapsed time starts at the first replayable unknown. Both budgets are shared across one dequeued request's chunks, and the first exhausted limit stops replay. Waiting in the queue consumes neither budget. With one consumer, queued requests wait behind the request being retried.

A replay cannot start after expiry, and an in-flight replay is bounded by the remaining budget and normal attempt timeout. If `Retry-After` cannot fit, replay ends without resending. Otherwise final accounting may wait for Collector's next backoff wakeup. The ordinary retry horizon starts earlier, at the first delivery attempt, and can stop replay even when both elapsed limits are equal. Its default of zero removes that outer elapsed limit; cancellation and shutdown still stop retries.

A later success confirms at least one copy was written. A later rejection cannot resolve the earlier uncertainty. If delivery ends without confirmation, the ambiguous chunk counts as unknown, while an unsent suffix exhausting ordinary retries counts as dropped. Pending replay has no final outcome yet.

`telescope_ingestion_unknown_replay_attempts_total` counts replay attempts. `telescope_ingestion_unknown_replayed_records_total` counts records once per replayed chunk in each queue delivery. These overlap written or unknown outcomes and must not be added to final outcome totals. Collector's raw send-failure counter also includes terminal unknown results; use Telescope's delivery counters to distinguish them.

### Crash recovery

The persistent queue retains the original OTLP request until export completes. A crash between a partial append and queue acknowledgement can replay previously committed or unknown chunks. Replay state and budgets are process-local and restart on recovery; they are not a lifetime limit across restarts. Counters also reset and cannot resolve missing crash acknowledgements. This is an at-least-once crash boundary; request deduplication and shared queue takeover are not provided.

Collector's generic `Dropping data` logs describe failed export requests, including unknown outcomes and file entries retained at shutdown. Telescope logs terminal unknown outcomes and retained requests separately, and reports final record counts in its shutdown summary. Use those counts and delivery metrics to measure loss; a retained original request may also contain already committed chunks.

See [deployment](deployment.md#queue-ownership-and-sizing) for storage ownership, capacity, and memory queue shutdown, and [operations](operations.md#drain-and-upgrade) for draining before a mapping change.
