# CHANGELOG

All notable changes to this project will be documented in this file.

## Unreleased

### Breaking changes

* Replace the v0.2 `path`, `schema_version`, and `create_tables_if_not_exist` configuration with explicit per-signal tables and mappings. Tables are user-managed; Telescope no longer creates them while ingesting. Use the [v0.2 compatibility mapping and migration procedure](docs/migration-v0.2.md) to preserve existing queries, drain the old queue, and switch to a new queue volume.
* Rename `telescope daemon` to `telescope run`. Move raw Collector commands to `telescope advanced collector` and remove the application-layer query server.
* Use the Collector batch processor for batching; `sending_queue.batch` is unsupported. Native operational HTTP listens on `127.0.0.1:8080` by default; set `--http-addr` or `TELESCOPE_HTTP_ADDR` for remote access.
* Custom Go distributions import `github.com/scopedb/telescope/scopedbexporter` and register `NewFactory`. Append clients and intermediate records are private. Builds require Go 1.27; macOS binaries require macOS 13 or later.

### New features

* Map logs, traces, and metrics into selected ScopeDB columns, with ordered fallback sources, defaults, constants, and explicit casts. Inspect samples, preview rows, plan additive table changes, and validate destinations before receiving traffic.
* Capture bounded OTLP samples with a standalone listener or from a running instance. Verify synthetic delivery, inspect local delivery status, and execute a single ScopeQL statement from the CLI.
* Preserve pending telemetry in disk-backed queues across restarts. Report confirmed writes, known drops, and unknown append outcomes separately, with Prometheus metrics and alert rules.
* Provide Docker Compose and Kubernetes StatefulSet examples, Linux amd64/arm64 images, and self-contained archives for macOS arm64 and Linux amd64/arm64.

### Improvements

* Use synchronous ScopeDB Append acknowledgements and honor `Retry-After`. Isolate identifiable invalid rows while delivering valid neighbors; retry only unsettled rows during a running export. Unknown outcomes are terminal, while crash recovery retains the documented [queue replay boundary](docs/exporter.md#append-and-retry-semantics).
* Keep the receiver available during a ScopeDB outage. Readiness reflects local Collector health; destination verification is reported separately.

For earlier releases, see [GitHub Releases](https://github.com/scopedb/telescope/releases).
