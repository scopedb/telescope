# Operating Telescope

## Capture Before Configuration

Start a temporary OTLP/HTTP listener when representative application data is needed before the mapping and destination table exist:

```bash
telescope capture \
  --listen-http 127.0.0.1:14318 \
  --limit 100 \
  --timeout 2m \
  traces > traces.otlp.json
```

Point an application or a temporary second exporter in an existing Collector at `http://127.0.0.1:14318`. The command accepts standard OTLP/HTTP JSON or protobuf, including gzip requests, on `/v1/traces` and writes a standard OTLP JSON export request to stdout. It exits when the record limit is reached, returns a partial sample when the timeout expires after receiving data, and fails when no data arrives.

This mode starts only the selected OTLP/HTTP endpoint. It does not load `telescope.yaml`, connect to ScopeDB, map, persist, queue, retry, or forward telemetry. Only the bounded sample is retained, so use a second exporter when the original telemetry must continue to its existing destination. A single request is limited to 20 MiB.

Inspect the sample before writing a mapping:

```bash
telescope inspect traces --sample traces.otlp.json
```

`inspect` uses the same mapper as `run` to list exact source selectors, observed output types, and the records where each selector is populated, grouped by the OTLP resource, scope, and signal layers. It omits empty protocol defaults, marks partial and mixed-type selectors, expands nested objects into copyable selectors, and keeps arrays as whole values rather than suggesting brittle numeric indexes. It does not require a Telescope configuration or ScopeDB connection and never prints sample values. Use `--format json` for structured output.

Capture, retain, and inspect the first sample in one pipeline:

```bash
telescope capture --listen-http 127.0.0.1:14318 traces |
  tee traces.otlp.json |
  telescope inspect traces --sample -
```

With Docker, publish a temporary host port and keep stdout redirected to the host:

```bash
docker run --rm \
  -p 127.0.0.1:14318:4318 \
  ghcr.io/scopedb/telescope:latest \
  capture --listen-http 0.0.0.0:4318 \
    --limit 100 --timeout 2m traces > traces.otlp.json
```

Then feed the result directly into the normal setup sequence:

```bash
telescope preview --offline --strict \
  --sample traces=traces.otlp.json \
  deploy/telescope.yaml
```

## Preview a Mapping with Live Data

Capture a bounded sample at the running exporter's input and pipe it directly through a candidate mapping:

```bash
telescope capture \
  --endpoint http://127.0.0.1:8080 \
  --limit 100 \
  --timeout 45s \
  traces |
  telescope preview \
    --sample traces=- \
    deploy/telescope.yaml
```

`capture` waits until it collects the requested number of log records, spans, or metric points, or until the timeout returns a partial sample. It retains nothing when no capture is active. Capture observes exporter input after Collector batch processing and before the sending queue. The 45-second default exceeds the bundled 30-second batch timeout, while retries cannot duplicate the sample. `preview` uses the same mapper as `run` and does not append the sample. Redirect `capture` to a file when the same input should be replayed later.

## Inspect Delivery Status

Native `telescope run` binds operational HTTP to `127.0.0.1:8080` by default. `--http-addr` overrides `TELESCOPE_HTTP_ADDR` when remote access is needed. These endpoints have no authentication and capture can return application telemetry, so expose them only through a trusted network or authenticated proxy. Docker Compose publishes them on host loopback; the Kubernetes baseline exposes only OTLP through its Service.

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/v1/ingestion/status
curl -sS http://127.0.0.1:8080/metrics
```

The ingestion status reports only configured signals, including received, ScopeDB-confirmed written, unknown, and dropped counts; exhausted retries, isolated or batch-level permanent rejections, and queue refusals; queue utilization and allocated queue storage; table routes; destination validation; and the latest write result. These facts are observed by the running Telescope process; Telescope does not query destination tables. The exporter queue does not include telemetry still waiting in the Collector batch processor or an in-flight export; human-readable `telescope status` calls out accepted items that do not yet have a final outcome when the queue is empty. Collector owns retries. Retryable requests have no elapsed-time expiry by default and remain in the bounded persistent queue across restarts. An identifiable bad record is dropped without blocking valid neighbors; an unisolated permanent chunk failure stops immediately.

Unknown append outcomes are not automatically replayed and do not count as written or dropped. Monitor `telescope_ingestion_unknown_total` independently. Counters reset with the process; the [exporter delivery contract](exporter.md#append-and-retry-semantics) describes the persistent queue crash boundary. `/readyz` waits for Collector receivers to start and internal telemetry to be available; it does not wait for ScopeDB. Destination verification and failures remain visible in ingestion status.

`/metrics` is the stable Prometheus surface for Telescope delivery and queue alerts. The bundled [Prometheus rules](../deploy/prometheus-rules.yaml) cover queue saturation, final drops, stalled delivery, and unverified destinations, and record the queue directory's 24-hour disk high-water mark. Prometheus should also alert on its standard `up` metric: `/metrics` returns `503` instead of publishing false zeroes when Collector's internal telemetry is unavailable.

For a human-readable summary:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml \
  exec telescope telescope status
```

## Verify Delivery

To send a synthetic signal and wait for the exact ScopeDB append acknowledgement:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml \
  exec telescope telescope verify
```

Expected output:

```text
traces: OTLP accepted synthetic probe (probe-...)
traces: ScopeDB append committed synthetic probe (probe-...)
```

## Query Stored Results

Use the diagnostic query command to inspect rows produced by a mapping with the same ScopeDB connection already available to Telescope:

```bash
telescope query \
  "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' SELECT start_timestamp, trace_id, service LIMIT 1"

telescope query --format json \
  "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' LIMIT 1"

kubectl -n telescope exec telescope-0 -- \
  telescope query --format jsonl \
  "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' LIMIT 1"
```

It submits exactly one ScopeQL statement read from the argument, stdin, or `--file`, and supports human-readable `table` plus scriptable `json` and `jsonl` output. It reuses the normal `--scopedb-endpoint`, `--scopedb-api-key`, `--env-file`, and ScopeDB environment precedence. `--timeout` sets the server execution limit; interrupting the command also requests cancellation of the submitted statement.

Use a selective trace ID or time predicate when inspecting telemetry. `LIMIT` bounds returned rows, but it should not be treated as a scan budget.

The command executes one statement and does not provide a REPL, connection profiles, history, or client-side multi-statement script handling.

## Upgrade or Change a Mapping

`telescope status` reports the running version and a normalized config digest. A binary-only upgrade may reuse the persistent queue when that digest is unchanged; Telescope's test suite verifies that the current binary can drain the frozen `v1` queue format for logs, traces, and metrics.

The queue retains OTLP before destination mapping. Do not start a changed table or mapping contract against a non-empty queue: older telemetry would be projected by the new mapping. Remove the old instance from its OTLP upstream, then wait until every queue is empty and `telescope status` reports no accepted items without a final outcome before changing the config. For a zero-downtime change, send new traffic to a separate deployment and queue volume while the old deployment drains with its original config.

For the v0.2 configuration and queue break, follow the [migration procedure](migration-v0.2.md).

## Status and metrics reference

`GET /v1/ingestion/status` reports the current data path without querying ScopeDB telemetry tables. It lists only configured signals. For each one it includes:

- OTLP receiver accepted, failed, and refused records;
- successfully written, unknown, and ultimately dropped records;
- drops split into exhausted retry, queue refusal, and permanent rejection counts;
- exporter queue enqueue failures, current size, capacity, and configured unit;
- filesystem blocks allocated to the persistent queue directory;
- the configured ScopeDB table;
- last write attempt, success, failure, duration, and error;
- whether the destination has been verified by a table check or successful append;
- the synthetic probe IDs in the last successfully appended batch and their confirmation time;
- records rejected by permanent mapper or ScopeDB errors observed by the exporter;
- invalid mapper items grouped by stable reason.

This endpoint reports data-plane delivery state only. It does not report table queryability.

Receiver and queue counts come from the OpenTelemetry Collector's internal metrics. Final delivery counts come from the exporter and request completion, so partial retries do not turn settled records into drops. `written` counts native signal items only when ScopeDB confirms a chunk as `committed` with the expected inserted-row count, so a committed prefix remains visible even if a later chunk fails. `unknown` counts ambiguous appends that Telescope will not automatically replay; it is excluded from `written` and `dropped`. `dropped` combines exhausted retry items, queue enqueue failures, invalid items isolated locally, and complete ScopeDB row rejections. `retry_exhausted` is the retryable part of final exporter failures. The embedded profile has no elapsed-time retry cutoff, so this normally remains zero; it can increase when a memory queue cannot retry during shutdown or an advanced Collector configuration sets a finite horizon. Persistent entries retained during shutdown are not counted as drops. `permanent_rejected` includes permanent mapping and ScopeDB rejections. Telescope does not add a second retry or queue implementation.

The reported exporter queue excludes telemetry held by the Collector batch processor and an export currently in flight. Therefore a transient `received > written + unknown + dropped` with an empty exporter queue does not imply data loss. Human-readable `telescope status` calls this out as accepted items without a final outcome; it does not publish a synthetic durable counter for that transient state.

`invalid_items_by_reason` counts locally identifiable mapping failures, while the delivery counters use each signal's native unit: log records, spans, or metric points. Human-readable `telescope status` prints the non-zero reason counts for each signal. A metric with no data type therefore increments `unsupported_metric_type` but contributes zero metric points to `permanent_rejected` and `dropped`.

When the embedded profile uses `unit: bytes`, queue size and capacity are logical serialized telemetry bytes reported by Collector. `queue_storage.allocated_bytes` separately reports filesystem blocks allocated to files in `TELESCOPE_QUEUE_DIR`; it does not treat the queue database's logical file length as current disk use.

`GET /metrics` exposes the same delivery facts as stable Prometheus metrics. This includes accepted, written, unknown, and finally dropped items; logical queue size, capacity, and utilization; last write success; destination verification; and allocated queue storage. `telescope_ingestion_queue_bytes` and `telescope_ingestion_queue_capacity_bytes` are published only for the byte sizer, while `telescope_ingestion_queue_utilization` is published for every sizer and is what the baseline alerts use. Load [`deploy/prometheus-rules.yaml`](../deploy/prometheus-rules.yaml) for the baseline alerts. Disk high-water history belongs in Prometheus and is calculated with `max_over_time`; Telescope does not retain another history database. The endpoint returns `503` rather than publishing false zeroes when Collector metrics are unavailable.

The status and metrics endpoints read Collector's private Prometheus endpoint at `http://127.0.0.1:8888/metrics` by default. Set `TELESCOPE_INTERNAL_METRICS_URL` when a custom Collector configuration moves it. Scrape Telescope's operational `/metrics` endpoint, not port `8888`; the latter is an internal implementation detail. If internal metrics cannot be read, the status response remains available but reports `internal_telemetry.available: false` and a `degraded` signal state. State is limited to component health (`starting`, `ready`, `degraded`, or `refusing`); counters, queue size, and timestamps describe data movement without inferring flow from an old success.

Counters reset when the process restarts. See the [delivery contract](exporter.md#append-and-retry-semantics) for the crash window between an append and queue acknowledgement.

### Forward Collector Metrics over OTLP

An advanced Collector configuration can forward internal metrics using the standard [`service.telemetry`](https://opentelemetry.io/docs/collector/internal-telemetry/) settings. Keep the pull reader when Telescope status needs to read these metrics. The embedded profile already sets `service.name=telescope`.

```yaml
service:
  telemetry:
    resource:
      service.name: telescope
    metrics:
      readers:
        - pull:
            exporter:
              prometheus:
                host: 127.0.0.1
                port: 8888
        - periodic:
            interval: 10000
            exporter:
              otlp:
                protocol: http/protobuf
                endpoint: https://metrics.example.com/v1/metrics
```

This forwards Collector queue, receiver, and exporter metrics. Telescope-specific delivery counters remain on `/metrics`; scrape that endpoint to distinguish unknown outcomes from known drops.
