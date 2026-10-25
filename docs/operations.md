# Operate Telescope

Use `telescope status` to inspect delivery, `telescope verify` to test a synthetic write, and `telescope query` to read stored rows. All counters describe the current process; the [delivery contract](exporter.md#append-and-retry-semantics) explains retries and crash recovery.

## Troubleshoot delivery

```bash
telescope status
telescope verify traces
telescope query --env-file deploy/.env \
  "FROM scopedb.otel.traces SELECT start_timestamp, service, trace_id LIMIT 5"
```

| Observation | Check |
| --- | --- |
| `received` does not increase | Application instrumentation, OTLP endpoint/protocol, network access, and enabled signals. |
| Queue grows and `last_error` is set | Destination credentials, table schema, rate limits, or ScopeDB availability. |
| Queue is empty but accepted items have no final outcome | Allow for Collector batching and an in-flight export; the queue does not include either. |
| `dropped` increases | Inspect permanent rejection, enqueue failure, and exhausted retry counts. Preview a sample for mapping failures. |
| `unknown` increases | Inspect append errors and the [unknown replay policy](exporter.md#append-and-retry-semantics); ambiguous outcomes are counted separately. |
| Internal telemetry is unavailable | Check Collector health and its private metrics endpoint. |

`verify` waits for an exact append acknowledgement, normally within its 45-second timeout, and prints `ScopeDB append committed synthetic probe` on success. It does not test application-specific values or query stored columns. `query` executes one ScopeQL statement from an argument, stdin, or `--file`; `--format json` and `--format jsonl` provide machine-readable results. It uses Telescope's ScopeDB connection and attempts cancellation after timeout or interruption.

## Capture and preview

To see the running exporter's input and test a candidate mapping:

```bash
telescope capture --limit 100 traces |
  telescope preview --sample traces=- telescope.yaml
```

Capture observes input after Collector batching and before the exporter queue. It retains nothing when no capture is active, stops at the limit or timeout, and cannot include duplicates from append retries. The default 45-second timeout allows for the bundled 30-second batch delay. A timeout returns a partial sample if any data arrived, otherwise it fails. Use `--endpoint` for another Telescope instance.

### Capture before configuration

When no Telescope instance exists yet, start a temporary OTLP/HTTP listener:

```bash
telescope capture --listen-http 127.0.0.1:14318 \
  --limit 100 --timeout 2m traces > traces.otlp.json
```

Point a temporary second exporter at `http://127.0.0.1:14318`. This listener only samples; it does not forward telemetry, so keep the original destination if that stream must continue. It needs no configuration or ScopeDB connection and accepts JSON or protobuf, including gzip, with a 20 MiB request limit. `--listen-http` and `--endpoint` are mutually exclusive.

```bash
telescope inspect traces --sample traces.otlp.json
telescope preview --offline --strict \
  --sample traces=traces.otlp.json telescope.yaml
```

`inspect` reports selectors, types, and coverage without printing sample values. It expands nested objects, keeps arrays whole, and highlights partial or mixed-type fields. Both `capture` output and `preview` rows can contain application data.

## Status and metrics reference

Native operational HTTP defaults to `127.0.0.1:8080`. `--http-addr` overrides `TELESCOPE_HTTP_ADDR`. These endpoints have no authentication; keep them on a trusted network or behind an authenticated proxy. Compose publishes them on host loopback, and the Kubernetes Service exposes only OTLP.

| Endpoint | Contract |
| --- | --- |
| `/healthz` | Process liveness; independent of ScopeDB availability. |
| `/readyz` | Receivers are running and internal telemetry is available; does not wait for ScopeDB. |
| `/v1/ingestion/status` | Delivery counters, queues, configured tables, destination verification, latest writes/errors, and successful probe IDs. |
| `/v1/ingestion/capture` | Bounded OTLP sample from the running exporter input. |
| `/metrics` | Prometheus delivery and queue metrics; returns HTTP 503 when Collector metrics are unavailable. |

Status lists only configured signals. Counts use each signal's native unit: log records, spans, or metric points.

| Status field | Meaning |
| --- | --- |
| `received` | Items accepted by the OTLP receiver. Receiver failures/refusals are also reported. |
| `written` | Items in an append confirmed `committed` with the expected row count. Partial successes remain counted. |
| `unknown` | Ambiguous append outcomes, excluded from both `written` and `dropped`. |
| `unknown_replay_attempts` / `unknown_replayed_records` | Replay attempts and records exposed to potential duplication. These overlap final outcomes and reset on restart. |
| `dropped` | Final permanent rejections, exhausted retries, and queue enqueue failures. |
| `retry_exhausted` | Retryable items with a final failure. Normally zero with unlimited retries; memory-queue shutdown or a custom finite retry horizon can increase it. |
| `permanent_rejected` | Locally invalid items and permanent ScopeDB rejections. |
| `invalid_items_by_reason` | Locally identifiable mapping failures by [reason](ingestion-compatibility.md#rejection-behavior). A metric with no data type counts as an invalid item but has zero metric points. |
| `queue.size` / `queue.capacity` | Collector queue usage in its configured unit. With `bytes`, these are logical serialized bytes. |
| `queue_storage.allocated_bytes` | Filesystem blocks allocated to queue files, distinct from logical queue bytes or file length. |

An empty queue with `received > written + unknown + dropped` can reflect batching or an export in flight. `status` calls out these unsettled items; persistent entries retained at shutdown are not counted as dropped. Counters reset on restart and do not establish table queryability or resolve missing crash acknowledgements.

Scrape operational `/metrics` and load the [Prometheus rules](../deploy/prometheus-rules.yaml) for queue saturation, final drops, stalled delivery, unverified destinations, and disk high-water history. Also monitor Prometheus `up`, `telescope_ingestion_unknown_total`, and Collector `otelcol_receiver_refused_*`. Receiver refusals are excluded from final drops because the sender may retry. Byte metrics apply only to byte queues; utilization applies to every queue sizer.

Telescope reads Collector metrics at the resolved `telemetry.metrics_addr`, defaulting to `http://127.0.0.1:8888/metrics`. This private endpoint is not the stable Telescope metrics API. If it fails, status reports `internal_telemetry.available: false` and a degraded state. Signal states (`starting`, `ready`, `degraded`, `refusing`) describe component health rather than traffic flow. `TELESCOPE_INTERNAL_METRICS_URL` does not override `run` configuration.

## Export self-metrics

Add this to the YAML used by `telescope run`:

```yaml
telemetry:
  resource:
    deployment.environment: ${env:DEPLOYMENT_ENVIRONMENT}
  otlp:
    endpoint: https://metrics.example.com/v1/metrics
    headers:
      Authorization: ${env:METRICS_AUTHORIZATION}
```

| Setting under `telemetry` | Contract |
| --- | --- |
| `resource` | String attributes, with `${env:NAME}` support. `service.name` defaults to `telescope`; Collector supplies the version and a process instance ID. |
| `otlp.endpoint` | Empty disables push. HTTP(S) selects transport security. For HTTP, omit the path to use `/v1/metrics` or provide the complete metrics path. |
| `otlp.protocol`, `otlp.compression` | `http/protobuf` or `grpc`; `none` or `gzip`. Defaults are `http/protobuf` and `none`. |
| `otlp.interval`, `otlp.timeout` | Defaults `60s` and `10s`. |
| `otlp.headers` | Request headers; values support `${env:NAME}` and are redacted in diagnostics. |

Push includes Collector receiver, exporter, and queue metrics plus Telescope's written, unknown, dropped, invalid-item, and unknown-replay counters. It also includes the latest successful append timestamp and destination verification gauge. The delivery counters come from the same registry as status; dropped records are partitioned by `permanent_rejected`, `retry_exhausted`, and `enqueue_failed`. Pushed queue metrics use `otelcol_exporter_queue_size` and `otelcol_exporter_queue_capacity`; filesystem allocation is available only through operational status and Prometheus.

The local reader remains enabled for readiness, status, and `/metrics`. The OTLP reader has its own timeout and no persistent queue; gateway outages do not block ingestion or change readiness. Counters reset on restart. Prometheus readers may translate metric names and add unit or counter suffixes, so check OTLP names when migrating gateway queries. Custom Collector distributions configure readers through `service.telemetry` and register the [ScopeDB exporter](exporter.md).

## Drain and upgrade

Stop new OTLP traffic, then wait for both the exporter queue and accepted items without a final outcome to reach zero before stopping the process. Queue size alone is insufficient. Keep each replica's queue directory exclusive and persistent.

For a binary-only upgrade, keep the same config digest. For a mapping change without a pause, route new traffic to a separate deployment and queue volume while the old deployment drains under its original config. Follow the [v0.2 migration procedure](migration-v0.2.md) when crossing that version boundary.
