# Upgrade from v0.3

The next release uses ScopeDB Go SDK v0.8.0 and a batch-oriented delivery model. Upgrade custom Go builds to Go 1.27.3 or later. Review delivery policy and update configuration, status clients, dashboards, and alerts together.

## Configuration and terminology

Rename `delivery.unknown_retry` to `delivery.ambiguous_retry` in Telescope YAML. Custom Collector distributions rename `exporters.scopedb.unknown_retry` to `exporters.scopedb.ambiguous_retry`. The `enabled`, `max_retries`, and `max_elapsed_time` fields retain their meanings and defaults. The old key is rejected rather than silently ignored.

```yaml
delivery:
  ambiguous_retry:
    enabled: true
    max_retries: 20
    max_elapsed_time: 5m
```

Leave replay disabled unless duplicate rows are acceptable. An ambiguous outcome means a write may have committed; it is neither confirmed written nor confirmed dropped.

| Surface                       | v0.3                                                                | Replacement                                                               |
| ----------------------------- | ------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| Go exporter configuration     | `Config.UnknownRetry`, `UnknownRetryConfig`                         | `Config.AmbiguousRetry`, `AmbiguousRetryConfig`                           |
| Go runtime status             | `UnknownRecords`, `UnknownReplayAttempts`, `UnknownReplayedRecords` | `AmbiguousRecords`, `AmbiguousReplayAttempts`, `AmbiguousReplayedRecords` |
| Status JSON                   | `unknown`                                                           | `ambiguous`                                                               |
| Status JSON                   | `unknown_replay_attempts`, `unknown_replayed_records`               | `ambiguous_replay_attempts`, `ambiguous_replayed_records`                 |
| Status JSON                   | `permanent_rejected`                                                | `permanent_failed`                                                        |
| Prometheus and OTLP metrics   | `telescope_ingestion_unknown_total`                                 | `telescope_ingestion_ambiguous_total`                                     |
| Prometheus and OTLP metrics   | `telescope_ingestion_unknown_replay_attempts_total`                 | `telescope_ingestion_ambiguous_replay_attempts_total`                     |
| Prometheus and OTLP metrics   | `telescope_ingestion_unknown_replayed_records_total`                | `telescope_ingestion_ambiguous_replayed_records_total`                    |
| Dropped metric `reason` label | `permanent_rejected`                                                | `permanent_failed`                                                        |
| CLI status column             | `UNKNOWN`                                                           | `AMBIGUOUS`                                                               |

Metric names change for both the operational Prometheus endpoint and pushed OTLP telemetry. Deploy updated alerts and dashboards with the binary. Use the matching CLI with the new status API. The `verify` success message is now `ScopeDB append succeeded for synthetic probe`.

## Delivery behavior

Collector owns the queue and retry schedule. Each dequeued request has a cursor and one pending NDJSON batch. The SDK sends one attempt and supplies `Succeeded`, `Failed`, or `Ambiguous`; Telescope records final outcomes and decides whether to ask Collector for another attempt.

* A successful batch is counted once and never resent during the running delivery. Ordinary retries and ambiguous replay retain the pending batch's exact mapped bytes.
* A permanent ScopeDB rejection fails the whole batch. Telescope no longer removes individual rows using legacy server row diagnostics and resends their neighbors. It continues with later batches; tune `append_batch` to control the batch failure boundary. Local mapping and encoding failures still reject only the invalid record.
* Only valid HTTP 429 refusals and temporary failures proven to precede dispatch qualify for safe automatic retry. Bodyless, plain-text, or malformed 429 responses are ambiguous. With replay disabled, those batches stop and count as ambiguous.
* Client timeouts after connection acquisition and explicit server retry refusal stop replay, even when `ambiguous_retry` is enabled. A rejection after an ambiguous attempt does not prove the earlier attempt failed; unresolved records remain ambiguous.
* Final outcomes are written, dropped, or ambiguous. Pending batches are unsettled. Replay counters overlap these outcomes and must not be added to them.

## Rollout and queue recovery

Drain before upgrading when practical: stop incoming traffic, wait for upstream batching and active delivery to settle, verify the queue is empty, and shut down cleanly. An empty queue alone does not prove that no append is in flight. Update the configuration and observability consumers, start the new binary, and use `validate`, `status`, and `verify` to check the deployment.

The persistent format remains the original OTLP request, and existing v0.3 queue files remain readable. A queue-format conversion or new volume is not required solely for this upgrade. Keep the same exporter identity, destination, and mapping when resuming a nonempty queue, and never run two processes against the same queue directory.

The delivery cursor is not persisted. A crash or interruption before queue acknowledgement can replay already written records; this upgrade does not provide exactly-once delivery. Replay budgets and counters restart with the process. If mappings or destinations also change, follow the [drain and upgrade procedure](operations.md#drain-and-upgrade) before applying them. A rollback requires the old configuration keys and monitoring names as well as the old binary.
