# Migrate from v0.2

The v0.3 configuration uses explicit tables and mappings. Start from [`deploy/telescope.v0.2-compatible.yaml`](../deploy/telescope.v0.2-compatible.yaml) when existing queries depend on the v0.2 `otel.logs`, `otel.traces`, and `otel.metrics` columns. The template retains those column names and types, including the nested `record` object. Change the table routes if your database or schema differs.

## Stored data

`record` selects the full normalized record. Existing keys such as `attributes`, `resource`, `scope`, trace `events` and `links`, and log `body`, `message`, and timestamps retain their meaning. The object also contains additive OTLP metadata supported by v0.3. This is the normalized source-mapping contract, not a raw OTLP export envelope.

The template keeps `schema_version: v1`. Two exporter-generated columns change:

- `ingest_ts` is omitted and remains null in an existing nullable column. v0.2 generated it at mapping time; it was neither the event timestamp nor a database commit timestamp. Use the signal's event timestamp for time-series queries.
- `row_id` uses `record.id`: the lowercase SHA-256 of the signal name, a NUL separator, and the canonical JSON encoding of the normalized record. Object keys are sorted; batch position and arrival time are excluded. Identical records produce identical IDs across queue replay and rebatching with the same source-mapping contract. This differs from v0.2's generated IDs. It can support duplicate analysis, but two legitimate identical events also share an ID; do not blindly deduplicate all logs by this field. Added source fields in a future mapping-contract version can change the hash.

Attribute aliases use ordinary ordered `sources` rules. Missing or null values fall through; an empty string is a present value. Explicit casts enforce the destination type: malformed numeric attributes can reject a record instead of silently clearing the column. Preview representative application samples before switching.

The compatibility tests compare all mapped columns with rows produced by the v0.2.0 exporter at `6352d6fc`, excluding the two generated columns above. They also preserve every legacy `record` path while allowing additive metadata. Live integration tests append all three signals to tables created with the frozen v0.2 schema and read the results back.

## Prepare and switch traffic

1. Install the `v0.3.0` release or build from an exact commit. Run offline validation and preview in CI; neither command needs a ScopeDB connection:

   ```bash
   telescope validate --offline deploy/telescope.v0.2-compatible.yaml
   telescope preview --offline \
     --sample traces=deploy/samples/traces.otlp.json \
     deploy/telescope.v0.2-compatible.yaml
   ```

2. Run `telescope validate` against the intended destination before deployment. Existing v0.2 tables need no DDL for this template. For new tables, review `telescope plan` output and apply it separately. Telescope never applies DDL at runtime.
3. Start a parallel service with a separate discovery name and a new persistent queue. Give each replica its own stable queue directory. Keep the old service receiving traffic while checking the new service's `/readyz` and sending representative synthetic telemetry through each enabled signal. Verify both append acknowledgement and stored values.
4. Switch the OTLP discovery name or sender endpoints to the new service. Allow for DNS caches and long-lived gRPC connections; keep the old service reachable until existing senders have reconnected. Avoid deliberate dual delivery unless duplicate telemetry is acceptable.
5. Stop new traffic to the old service, let its batch processor and in-flight exports finish, and check its queue and final-outcome counters before stopping it. A v0.2 memory queue cannot be recovered after termination: allow it to empty while the process remains alive. A v0.2 persistent queue must drain under the original binary and configuration. Do not mount it into v0.3.
6. Compare event-time row counts and continuity by signal and relevant resource dimensions. Monitor unknown outcomes, final drops, queue saturation, and the last confirmed append. Retain the old configuration for rollback. A rollback must also preserve and drain the new service's queue under its original mapping.

## Runtime settings

Use the [runtime configuration reference](deployment.md#configuration-and-capacity) for queue type and directory, memory, batching, deadlines, retry budgets, and self-metrics. The default file queue needs exclusive ownership and retained storage. A task-local directory or memory queue is suitable when replacement durability is unnecessary.

Size queues from measured serialized OTLP bytes per second and the expected outage. Account for all consumers and replicas sharing the workspace request quota; `Retry-After` is not a cluster-wide rate limiter. See [delivery semantics](exporter.md#append-and-retry-semantics) for unknown outcomes and crash replay.

Startup no longer contacts ScopeDB. Readiness requires running receivers and available internal telemetry; destination health is reported separately. Verify cold start, recovery with a non-empty queue, and termination grace periods in the target deployment before switching traffic.
