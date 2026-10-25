# Deploy Telescope

Prepare and validate the ScopeDB tables with the [quickstart](quickstart.md) before routing application traffic. Pin the binary or image to a reviewed commit while v0.3 is unreleased, and to an immutable release tag once published.

## Native process

```bash
telescope run --env-file deploy/.env deploy/telescope.yaml
```

Run under your service manager and send SIGTERM for a graceful stop. The default file queue uses `~/.telescope/queue`; configure `delivery.queue.directory` or `TELESCOPE_QUEUE_DIR` for each instance. Keep the directory across binary upgrades and follow the [drain procedure](operations.md#drain-and-upgrade) before changing mappings.

## Docker Compose

With `deploy/.env` and `deploy/telescope.yaml` prepared:

```bash
make docker-build
IMAGE=scopedb-telescope:ci docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml up -d
```

Compose publishes OTLP on ports 4317/4318 and operational HTTP on `127.0.0.1:8080`. Its named volume preserves the queue across container replacement. Use `docker compose --env-file deploy/.env -f deploy/docker-compose.yaml exec telescope telescope status` to inspect delivery. For a published release image, set `IMAGE=ghcr.io/scopedb/telescope:<version>` and omit the build step.

## Kubernetes

Use the [StatefulSet guide](../deploy/kubernetes/README.md). Each replica gets an independent queue volume; the Service exposes OTLP. The example overlay is the starting point for your image tag, mapping, storage class, and tuning.

## Configuration and capacity

`telescope.yaml` contains both `signals` and runtime settings. Omitted runtime fields use the defaults below. `telescope validate --offline` checks mappings, runtime policy, and environment references without connecting to ScopeDB.

```yaml
destination:
  endpoint: https://region.scopedb.com
  api_key: ${env:SCOPEDB_API_KEY}
receivers:
  grpc_addr: 0.0.0.0:4317
  http_addr: 0.0.0.0:4318
http_addr: 127.0.0.1:8080
resources:
  memory_limit_mib: 512
  memory_spike_mib: 128
batch:
  size: 2000
  max_size: 2000
  timeout: 30s
delivery:
  timeout: 30s
  max_rows: 8000
  max_bytes: 4194304
  queue:
    type: file
    directory: /var/lib/telescope/queue
    max_bytes: 536870912
    consumers: 1
  retry:
    initial_interval: 5s
    max_interval: 60s
    max_elapsed_time: 0s
telemetry:
  metrics_addr: 127.0.0.1:8888
  otlp:
    endpoint: https://metrics.example.com/v1/metrics
    protocol: http/protobuf
    headers:
      Authorization: ${env:METRICS_AUTHORIZATION}
    interval: 60s
    timeout: 10s
# Add the signals block from your mapping configuration.
```

The example selects a container queue directory and enables optional OTLP self-metrics. The native queue directory defaults to `~/.telescope/queue`; self-metrics push is disabled until `telemetry.otlp.endpoint` is set. OTLP supports `http/protobuf` and `grpc`, with TLS selected by the URL scheme. For HTTP, omit the path to use `/v1/metrics`, or provide the complete metrics path. Headers are passed literally after `${env:NAME}` substitution. Missing referenced variables fail validation; their values are never printed by `validate` or `run`.

Precedence is command flags, nonempty Telescope environment variables, YAML, then built-in defaults. Connection settings additionally accept `SCOPEDB_ENDPOINT` / `SCOPEDB_API_KEY` as environment fallbacks before YAML. An `--env-file` fills unset values using literal `KEY=VALUE` lines and optional matching quotes; it performs no shell expansion and does not support inline comments. `${env:NAME}` references are supported in destination settings, the queue directory, and OTLP endpoint and headers. Mapping values remain literal. Operational settings do not change the ingestion contract digest.

[The environment example](../deploy/.env.example) lists the existing batching, append, timeout, and queue overrides. Compose forwards these variables. Keep credentials in your deployment secret store and tune memory, retries, and self-metrics in YAML. `memory_spike_mib` must be smaller than `memory_limit_mib`; raise the limit to match the process budget. `delivery.timeout: 0s` disables the per-attempt deadline, and `delivery.retry.max_elapsed_time: 0s` permits unlimited retries. Set a finite duration, such as `5m`, when delivery must expire; exhausted records count as dropped.

Queue capacity is **per signal**, 512 MiB of logical serialized data by default. Size a file volume above the sum for enabled signals, plus queue database overhead. File queues require exclusive ownership: never share a directory between live processes. Each replacement must reopen the same directory only after its previous owner stops. A task-local file directory avoids shared locks, but data survives only as long as that directory does. `delivery.queue.type: memory` explicitly selects volatile buffering and needs no volume; pending data is lost when the process exits. Shared queue takeover is not provided. On network storage, validate locking, sync behavior, and startup latency in the actual deployment.

Use [health, readiness, and metrics](operations.md#status-and-metrics-reference) to distinguish a running process from a destination that is accepting writes.
