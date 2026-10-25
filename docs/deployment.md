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
  resource:
    deployment.environment: ${env:DEPLOYMENT_ENVIRONMENT}
  level: normal
  metrics_addr: 127.0.0.1:8888
  otlp:
    endpoint: https://metrics.example.com/v1/metrics
    protocol: http/protobuf
    compression: gzip
    headers:
      Authorization: ${env:METRICS_AUTHORIZATION}
    interval: 60s
    timeout: 10s
# Add the signals block from your mapping configuration.
```

The example selects a container queue directory and enables optional OTLP self-metrics. The native queue directory defaults to `~/.telescope/queue`; self-metrics push is disabled until `telemetry.otlp.endpoint` is set. OTLP supports `http/protobuf` and `grpc`, with TLS selected by the URL scheme. For HTTP, omit the path to use `/v1/metrics`, or provide the complete metrics path. OTLP compression accepts `none` (default) or `gzip`. `telemetry.resource` adds string resource attributes; `service.name` defaults to `telescope`, and Collector supplies the binary version and a process instance ID. `telemetry.level` accepts `normal` (default) or `detailed`; lower levels omit observations required by operational status. Headers and resource attributes support `${env:NAME}` substitution. Missing referenced variables fail validation. API keys and all OTLP header values are redacted in configuration diagnostics.

Precedence is command flags, nonempty Telescope environment variables, YAML, then built-in defaults. Connection settings additionally accept `SCOPEDB_ENDPOINT` / `SCOPEDB_API_KEY` as environment fallbacks before YAML. An `--env-file` fills unset values using literal `KEY=VALUE` lines and optional matching quotes; it performs no shell expansion and does not support inline comments. `${env:NAME}` references are supported in destination settings, the queue directory, and OTLP endpoint, headers, and resource attributes. Mapping values remain literal. `validate`, including `--offline`, reports resolved runtime values and their sources, plus total queue capacity across enabled signals. `validate` and `run` warn when an environment override replaces an explicitly configured YAML field. Remove obsolete deployment environment overrides when moving settings into YAML. Operational settings do not change the ingestion contract digest.

[The environment example](../deploy/.env.example) lists the existing batching, append, timeout, and queue overrides. Compose forwards these variables. Keep credentials in your deployment secret store and tune memory, retries, and self-metrics in YAML. `memory_spike_mib` must be smaller than `memory_limit_mib`; raise the limit to match the process budget. `delivery.timeout: 0s` disables the per-attempt deadline, and `delivery.retry.max_elapsed_time: 0s` permits unlimited retries. Set a finite duration, such as `5m`, when delivery must expire; exhausted records count as dropped.

Queue capacity (`delivery.queue.max_bytes`) is **per signal**, measured in logical serialized bytes. File queues default to 512 MiB per signal. Size a file volume above the sum for enabled signals, plus queue database overhead. File queues require exclusive ownership: never share a directory between live processes. Each replacement must reopen the same directory only after its previous owner stops. A task-local file directory avoids shared locks, but data survives only as long as that directory does. `delivery.queue.type: memory` explicitly selects volatile buffering and needs no volume; pending data is lost when the process exits. A shared filesystem is not a shared queue service: separate directories isolate live instances, but a replacement can recover pending data only by reopening its predecessor's directory after the predecessor stops. Increasing the file lock wait does not establish ownership or coordinate rolling deployment. Shared queue takeover is not provided. On network storage, validate locking, sync behavior, and startup latency in the actual deployment.

For memory queues, omitting `max_bytes` shares one quarter of the memory soft limit equally among enabled signals. The soft limit is `memory_limit_mib - memory_spike_mib`; the default 512/128 MiB settings give a 96 MiB total serialized queue budget, or 32 MiB each with three signals. Explicit capacities remain per signal. Validation rejects a combined capacity at or above the soft limit and warns above the default budget. These are sizing guards, not an RSS bound: decoded pdata, batching, concurrent requests, and runtime allocations also consume memory. Size the process/container above the configured heap limit and measure representative payloads under sustained backpressure. Monitor receiver refusals separately; a sender that does not retry refused telemetry loses those items before queue admission.

A graceful stop attempts to drain memory queues, with further retries disabled. A slow destination can outlast the process manager's termination grace period; a failed final attempt counts as `retry_exhausted`. A forced termination loses remaining memory entries. Allow time for batching and queued requests to drain, or use a file queue with a retained, exclusively owned directory.

Use [health, readiness, and metrics](operations.md#status-and-metrics-reference) to distinguish a running process from a destination that is accepting writes.
