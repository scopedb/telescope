# CHANGELOG

All notable changes to this project will be documented in this file.

## Unreleased

### Breaking changes

* Rename delivery configuration `unknown_retry` to `ambiguous_retry`, status fields and metrics from `unknown` to `ambiguous`, and the permanent-drop reason `permanent_rejected` to `permanent_failed`. Old configuration keys are rejected. See the [v0.3 migration guide](docs/migration-v0.3.md) for Go APIs, JSON fields, metrics, and rollout steps.
* Use whole-batch ScopeDB outcomes: permanent server rejection drops that batch without isolating rows from legacy diagnostics, then delivery continues with later batches. Local mapping failures still affect individual records.
* Upgrade the ScopeDB Go SDK to v0.8.0. Client timeouts after connection acquisition are no longer replayed, even with `ambiguous_retry` enabled. Malformed HTTP 429 responses are ambiguous and no longer qualify for safe automatic retry.

### Bug fixes

* Honor explicit server retry refusal during ambiguous replay. A later rejection cannot resolve an earlier ambiguous write; final accounting keeps those records separate from confirmed failures.
* Send the CLI query timeout using the server's supported `timeout_seconds` field so `telescope query --timeout` limits remote execution as well as local waiting.

### Improvements

* Retain each pending batch's mapped bytes across ordinary retries and ambiguous replay, and advance past settled records without rebuilding OTLP subsets. Collector owns persistence and backoff; the SDK performs and classifies each append attempt.
* Custom Go builds now require Go 1.27.3 or later.

## v0.3.0 (2026-10-26)

### Breaking changes

* Replace the v0.2 `path`, `schema_version`, and `create_tables_if_not_exist` configuration with explicit per-signal tables and mappings. Tables are user-managed; Telescope no longer creates them while ingesting. Use the [v0.2 compatibility mapping and migration procedure](docs/migration-v0.2.md) to preserve existing queries, drain the old queue, and switch to a new queue volume.
* Rename `telescope daemon` to `telescope run`. Move raw Collector commands to `telescope advanced collector` and remove the application-layer query server.
* Use the Collector batch processor for batching; `sending_queue.batch` is unsupported. Native operational HTTP listens on `127.0.0.1:8080` by default; set `--http-addr` or `TELESCOPE_HTTP_ADDR` for remote access.
* Custom Go distributions import `github.com/scopedb/telescope/scopedbexporter` and register `NewFactory`. Append clients and intermediate records are private. Builds require Go 1.27; macOS binaries require macOS 13 or later.

### New features

* Map logs, traces, and metrics into selected ScopeDB columns, with ordered fallback sources, defaults, constants, and explicit casts. Inspect samples, preview rows, plan additive table changes, and validate destinations before receiving traffic.
* Capture bounded OTLP samples with a standalone listener or from a running instance. Verify synthetic delivery, inspect local delivery status, and execute a single ScopeQL statement from the CLI.
* Preserve pending telemetry in disk-backed queues across restarts, or select an explicit memory queue. Configure receiver addresses, memory limits, batching, delivery, retries, and self-metrics in the same YAML as signal mappings. Inspect effective values and sources with `validate`, receive warnings about environment overrides, and derive memory queue defaults from the shared process budget.
* Opt into bounded replay of transient unknown append outcomes, including per-attempt timeouts, when duplicate rows are acceptable, with separate replay counters and conservative final accounting.
* Report confirmed writes, known drops, and unknown append outcomes separately. Push delivery and Collector metrics to an OTLP gateway with configurable resource attributes and gzip compression while retaining operational HTTP and Prometheus metrics with consistent delivery counters.
* Provide Docker Compose and Kubernetes StatefulSet examples, Linux amd64/arm64 images, and self-contained archives for macOS arm64 and Linux amd64/arm64.

### Improvements

* Bound shutdown waiting with `shutdown_timeout` (30 seconds by default) and configure runtime logging with `telemetry.log_level` (`warn` by default). Deadline expiry exits with an error; remaining delivery and final metrics are best-effort. See the [shutdown and queue guidance](docs/deployment.md#queue-ownership-and-sizing).
* Use synchronous ScopeDB Append acknowledgements and honor `Retry-After`. Retry connection failures known to precede dispatch and unstructured HTTP 429 rejections; keep unstructured HTTP 503 and post-dispatch failures unknown. Isolate identifiable invalid rows while delivering valid neighbors; retry only unsettled rows during a running export. Unknown outcomes are terminal by default, while crash recovery retains the documented [queue replay boundary](docs/exporter.md#append-and-retry-semantics).
* Keep the receiver available during a ScopeDB outage. Readiness reflects local Collector health; destination verification is reported separately.

For earlier releases, see [GitHub Releases](https://github.com/scopedb/telescope/releases).
