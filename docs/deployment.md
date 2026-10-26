# Deploy Telescope

Prepare and validate the ScopeDB tables with the [quickstart](quickstart.md) before routing application traffic. Pin the binary or image to a release tag such as `v0.3.0`, or an exact commit when building from source.

## Native process

```bash
telescope run --env-file deploy/.env deploy/telescope.yaml
```

Run under your service manager and send SIGTERM for a graceful stop. Keep the queue directory across binary upgrades and follow the [drain procedure](operations.md#drain-and-upgrade) before changing mappings.

## Docker Compose

With `deploy/.env` and `deploy/telescope.yaml` prepared:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml up -d
```

Compose uses `ghcr.io/scopedb/telescope:v0.3.0`, publishes OTLP on ports 4317/4318, and binds operational HTTP to `127.0.0.1:8080`. Its named volume preserves the queue across container replacement. Use `docker compose --env-file deploy/.env -f deploy/docker-compose.yaml exec telescope telescope status` to inspect delivery. To use a local build, run `make docker-build` and prefix the Compose command with `IMAGE=scopedb-telescope:ci`.

## Kubernetes

Use the [StatefulSet guide](../deploy/kubernetes/README.md). Each replica gets an independent queue volume; the Service exposes OTLP. The example overlay contains the image tag, mapping, storage class, and tuning.

## Configuration and capacity

`telescope.yaml` contains [signal mappings](table-management.md) and runtime settings. `telescope validate --offline` checks both, resolves environment references, and prints effective runtime values and their sources. API keys and OTLP headers are redacted. Runtime settings do not change the ingestion contract digest.

| YAML settings | Defaults and meaning |
| --- | --- |
| `destination.endpoint`, `destination.api_key` | ScopeDB connection; required for delivery and online validation. |
| `receivers.grpc_addr`, `receivers.http_addr` | `0.0.0.0:4317`, `0.0.0.0:4318`. |
| `http_addr` | `127.0.0.1:8080`; [operational endpoints](operations.md#status-and-metrics-reference). |
| `shutdown_timeout` | `30s`; positive limit on how long `run` waits for shutdown, including all signals, storage cleanup, and the final metrics push. |
| `resources.memory_limit_mib`, `resources.memory_spike_mib` | `512`, `128`; the spike allowance must be smaller than the limit. |
| `batch.size`, `batch.max_size`, `batch.timeout` | `2000`, `2000`, `30s`; flush by count or time. |
| `delivery.timeout`, `delivery.max_rows`, `delivery.max_bytes` | `30s`, `8000`, `4194304`; timeout per HTTP append and uncompressed NDJSON chunk limits. A zero timeout disables the deadline. |
| `delivery.queue.type`, `directory`, `max_bytes`, `consumers` | `file`, `~/.telescope/queue`, 512 MiB per signal, `1`. Memory queue capacity is derived as described below. |
| `delivery.retry.initial_interval`, `max_interval`, `max_elapsed_time` | `5s`, `60s`, `0s`; zero elapsed time permits unlimited retries. A finite elapsed limit must be at least `max_interval`; it expires known-unwritten records as dropped. |
| `delivery.unknown_retry` | Disabled by default; opt-in replay requires finite count and elapsed budgets. See [delivery semantics](exporter.md#append-and-retry-semantics). |
| `telemetry.metrics_addr`, `telemetry.level` | `127.0.0.1:8888`, `normal`. Level also accepts `detailed`; lower levels lack observations required by status. |
| `telemetry.log_level` | `warn`; set `info` to include normal startup and shutdown summaries, or `debug` for detailed diagnostics. |
| `telemetry.resource`, `telemetry.otlp` | Resource attributes and optional [OTLP self-metrics](operations.md#export-self-metrics). |

Precedence is command flags, nonempty Telescope environment variables, YAML, then defaults. Connection settings also accept `SCOPEDB_ENDPOINT` / `SCOPEDB_API_KEY` as environment fallbacks before YAML. `--env-file` fills unset values from literal `KEY=VALUE` lines with optional matching quotes; shell expansion and inline comments are unsupported. The [environment example](../deploy/.env.example) lists the overrides forwarded by Compose. `validate` and `run` warn when an override replaces an explicit YAML field; remove obsolete overrides when moving settings into YAML.

`${env:NAME}` references work in destination settings, the queue directory, and telemetry endpoint, headers, and resource attributes. Missing variables fail validation. Mapping values remain literal. Keep credentials in your deployment secret store.

### Queue ownership and sizing

Queue `max_bytes` is a **per-signal logical serialized byte capacity**. A file volume must hold the sum for enabled signals plus queue database overhead. File queues enable `fsync` and require exclusive ownership: a replacement may reopen its predecessor's directory only after that process stops. Separate directories on shared storage isolate live instances; they do not arrange recovery or ownership transfer. Increasing the file lock wait cannot coordinate a rolling deployment. Validate locking, sync, and startup latency on network storage.

A task-local directory retains data only as long as that directory exists. `delivery.queue.type: memory` needs no volume and loses pending data on process exit. A graceful stop uses Collector's existing queue drain with further retries disabled. Its normal delivery accounting and file queue recovery rules still apply.

Set the service manager's termination grace period above `shutdown_timeout`. The budget limits waiting; it does not cancel individual appends or reserve time for a final metrics push. At the deadline the CLI exits with an error, so remaining memory data may be lost without final counters or a shutdown summary. File entries still on disk can be recovered after restart, possibly with duplicate rows; failed entries already removed by Collector cannot be recovered. Keep `delivery.timeout` below the shutdown budget and allow time for queued requests and telemetry. Set `telemetry.log_level: info` to see normal shutdown summaries when shutdown completes.

For memory queues, omitting `max_bytes` divides one quarter of the soft memory limit (`memory_limit_mib - memory_spike_mib`) among enabled signals. Defaults give a 96 MiB total queue budget, or 32 MiB each with three signals. Validation rejects combined capacity at or above the soft limit and warns above the default budget. This is not an RSS bound: decoded data, batches, concurrent requests, and runtime allocations also consume memory. Size the container above the configured heap limit and test representative payloads under sustained backpressure. Monitor receiver refusals separately; those items never reach the queue and depend on the sender retrying them.
