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

Collector owns admission, the persistent OTLP queue, consumer concurrency, and retry scheduling. Telescope normalizes each dequeued request once, prepares one bounded NDJSON batch at a time, and advances a delivery cursor as records settle. The Go SDK's synchronous `Table.AppendNDJSON` performs one HTTP attempt and classifies its outcome. Telescope does not create a second queue or retry scheduler with `AppendStream`.

`append_batch.max_rows` and `max_bytes` cap rows and uncompressed NDJSON bytes, including newlines. Zero uses the protocol limits of 200,000 rows and 8 MiB; `telescope run` defaults to 8,000 rows and 4 MiB. Each active consumer retains its original OTLP request, normalized records, and at most one prepared batch between attempts. Use the upstream batch processor to bound and combine input; `sending_queue.batch` is unsupported because a queued request has one completion boundary.

| SDK attempt outcome | Telescope action                                                                                                                                                  |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Succeeded`         | Count the batch as written and advance. The SDK checks the inserted row count.                                                                                    |
| Retryable `Failed`  | Retain the batch and return control to Collector for backoff. This includes valid HTTP 429 refusals and temporary connection failures proven to precede dispatch. |
| Permanent `Failed`  | Count the whole batch as dropped and continue with later batches.                                                                                                 |
| `Ambiguous`         | Retain the batch only when eligible for opt-in replay; otherwise count it separately from written and dropped, then continue.                                     |

Local projection, encoding, and oversized-row failures affect only the invalid record. Server errors describe whole requests: Telescope never removes individual rows from a rejected batch and probes the remainder, even when an older server supplies row diagnostics. Already settled records are never resent during a running delivery. Ordinary retries and ambiguous replay both reuse the pending batch's exact NDJSON bytes.

Collector honors `Retry-After` as a minimum delay, even above its configured backoff interval. The default is one consumer per signal and unlimited elapsed retries; set `retry_on_failure.max_elapsed_time` to bound ordinary retries. If scheduling ends, an unresolved ambiguous batch counts as ambiguous; remaining records not known to have been written count as dropped with reason `retry_exhausted`. A full queue reports enqueue failures. Consumers and replicas share the destination's request quota, so added concurrency can increase throttling.

The exporter `timeout` configures the SDK deadline for each HTTP append, including reading the response. Mapping, encoding, and compression are outside this deadline; each batch and retry gets a fresh timeout. Defaults are 10 seconds for `NewFactory()` and 30 seconds for `telescope run`; zero disables it. Caller cancellation and deadlines still stop delivery. `compression` accepts `zstd` (default) or `gzip`. Certificate verification failures are terminal, and Telescope's SDK transport refuses HTTPS-to-HTTP redirects.

### Opt-in ambiguous replay

Applications that accept possible duplicates can enable `ambiguous_retry` (`delivery.ambiguous_retry` in `telescope run`):

```yaml
ambiguous_retry:
  enabled: true
  max_retries: 20
  max_elapsed_time: 5m
```

This defaults to disabled, with limits of 20 retries and five minutes when enabled. Eligible outcomes include HTTP 5xx, connection loss, malformed success acknowledgements, and malformed HTTP 429 responses. Explicit server retry refusal, client timeouts after connection acquisition, cancellation, and permanent HTTP errors remain terminal even with this option enabled. Failures proven to precede dispatch still follow the SDK's safe-retry classification.

Both limits must be positive. `max_retries` counts additional attempts for previously ambiguous batches, including safe rejections during replay. Elapsed time starts at the first replayable ambiguous outcome. Both budgets are shared across one dequeued request's batches. Waiting in the queue consumes neither budget. With one consumer, queued requests wait behind the request being retried.

A replay cannot start after expiry, and an in-flight replay is bounded by the remaining budget and HTTP request timeout. If `Retry-After` cannot fit, replay ends without resending. Otherwise final accounting may wait for Collector's next backoff wakeup. The ordinary retry horizon starts at the first delivery attempt and can stop replay earlier. Its default of zero removes that outer elapsed limit; cancellation and shutdown still stop retries.

A later success confirms at least one copy was written. A later rejection cannot resolve an earlier ambiguous attempt. Pending replay has no final outcome yet. `telescope_ingestion_ambiguous_replay_attempts_total` counts replay attempts, and `telescope_ingestion_ambiguous_replayed_records_total` counts records once per replayed batch in each queue delivery. These overlap written or ambiguous outcomes and must not be added to final outcome totals.

### Crash recovery

The persistent queue retains the original OTLP request until export completes. A crash between a successful append and queue acknowledgement can replay previously written or ambiguous batches. The cursor, prepared bytes, and replay budgets are process-local and restart on recovery. Counters also reset and cannot resolve missing crash acknowledgements. This is an at-least-once crash boundary; request deduplication and shared queue takeover are not provided.

At shutdown, pending persistent requests remain recoverable instead of being counted as dropped. Collector's generic `Dropping data` logs can include ambiguous outcomes and retained file entries; use Telescope's final counters and shutdown summary to distinguish them. A retained original request may contain batches already written before shutdown.

See [deployment](deployment.md#queue-ownership-and-sizing) for storage ownership, capacity, and memory queue shutdown, and [operations](operations.md#drain-and-upgrade) for draining before a mapping change.
