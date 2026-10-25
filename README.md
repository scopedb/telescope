# Telescope

Telescope is an OpenTelemetry Collector distribution that delivers OTLP logs, traces, and metrics to ScopeDB. It maps the fields you select into columns in user-managed tables, with Collector batching, retries, memory limits, and persistent queues.

Telescope never applies DDL at runtime. Its setup commands inspect samples, preview mappings, compare destination schemas, and generate additive ScopeQL for review. Delivery status distinguishes confirmed writes, known drops, and unknown append outcomes.

## Get started

Use Go 1.26.10 to build from a reviewed commit while v0.3 is unreleased:

```bash
make build
cp deploy/.env.example deploy/.env
cp deploy/telescope.example.yaml deploy/telescope.yaml
```

Set `TELESCOPE_SCOPEDB_ENDPOINT` and `TELESCOPE_SCOPEDB_API_KEY` in `deploy/.env`, then edit `deploy/telescope.yaml` to select signals, destination tables, and columns. For example:

```yaml
signals:
  traces:
    table: scopedb.otel.traces
    mapping:
      timestamp: span.start_time
      trace_id: span.trace_id
      span_id: span.span_id
      service:
        source: resource.attributes["service.name"]
        default: unknown
        cast: string
      name: span.name
      duration_ns: span.duration_ns
```

Only configured signals are enabled. Use representative application samples to check dynamic values before deployment:

```bash
./bin/telescope inspect --sample deploy/samples/traces.otlp.json traces
./bin/telescope preview --offline --strict \
  --sample traces=deploy/samples/traces.otlp.json deploy/telescope.yaml
./bin/telescope plan --env-file deploy/.env --out tables.scopeql deploy/telescope.yaml
```

Review `tables.scopeql`, including retention, clustering, distinct keys, and indexes. Apply it with a separately configured ScopeQL CLI connection, then validate and run Telescope:

```bash
scopeql config set-connection telescope-target
scopeql config use-connection telescope-target
scopeql config get-connections
scopeql run -f tables.scopeql

./bin/telescope validate --env-file deploy/.env deploy/telescope.yaml
./bin/telescope run --env-file deploy/.env deploy/telescope.yaml
```

The ScopeQL CLI owns its connection settings; Telescope's environment file does not configure it. `plan` emits only additive DDL and blocks on ambiguous or incompatible column types. See [mapping and table management](docs/table-management.md) for the full setup contract and [operations](docs/operations.md#capture-before-configuration) to capture samples before creating a configuration.

Release archives contain the binary at `./telescope`, documentation, and deployment examples. From an extracted archive, use `./telescope` in place of `./bin/telescope`; no Go installation is required.

## Deploy

For Docker Compose, prepare the same `deploy/.env` and `deploy/telescope.yaml` files, then build and start the image:

```bash
make docker-build
IMAGE=scopedb-telescope:ci docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml up -d
```

After v0.3 is published, select an immutable release image with `IMAGE=ghcr.io/scopedb/telescope:v0.3.0`. The [Kubernetes deployment guide](deploy/kubernetes/README.md) provides a StatefulSet with an independent persistent queue per replica.

The default OTLP listeners are gRPC on port 4317 and HTTP on port 4318. Configure an OpenTelemetry SDK for OTLP/HTTP with:

```bash
export OTEL_SERVICE_NAME=my-service
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

Docker Compose exposes operational HTTP on `127.0.0.1:8080`. `telescope status` reports local delivery facts; `telescope verify` sends synthetic telemetry and waits for its append acknowledgement. See [operations](docs/operations.md) for captures, queries, metrics, alerts, and queue draining.

Each enabled signal has a separate logical queue budget (`TELESCOPE_QUEUE_MAX_BYTES`, 512 MiB by default). Size the queue volume for the sum of those budgets plus storage overhead. Each running replica must own its queue directory exclusively. Review [migration from v0.2](docs/migration-v0.2.md) before upgrading an existing deployment.

## Commands and configuration

| Command | Purpose |
| --- | --- |
| `inspect` | Discover selectors and types in an OTLP sample |
| `preview` | Project a sample through a mapping without appending |
| `plan` | Compare mappings with the live catalog and generate additive DDL |
| `validate` | Check configuration and destination columns; `--offline` skips the catalog |
| `run` | Start the Collector and operational server |
| `status` | Report receiver, queue, and delivery state |
| `capture` | Capture bounded OTLP from a temporary listener or running instance |
| `verify` | Send synthetic OTLP and wait for confirmed appends |
| `query` | Execute one ScopeQL statement |
| `version` | Print the build version |

Run `telescope <command> --help` for options. `advanced collector` exposes upstream Collector commands and raw configuration for custom pipelines.

`preview`, `plan`, `validate`, and `run` share the Telescope YAML contract. `inspect` and standalone `capture` require no configuration or ScopeDB connection. `status`, running-instance `capture`, and `verify` use the operational endpoint. `query` uses the ScopeDB connection directly.

Connection precedence is command flags, `TELESCOPE_SCOPEDB_*`, then `SCOPEDB_ENDPOINT` / `SCOPEDB_API_KEY` fallbacks. An `--env-file` fills missing environment values using literal `KEY=VALUE` lines with optional matching quotes; shell expansion and inline comments are unsupported. [The environment example](deploy/.env.example) lists queue, append, timeout, and batch controls, all forwarded by Docker Compose.

## Reference and development

- [Mapping and table management](docs/table-management.md): rules, casts, schema planning, and mapping changes.
- [Ingestion compatibility](docs/ingestion-compatibility.md): supported OTLP sources, rejection behavior, and limitations.
- [Exporter integration](docs/exporter.md): the reusable Go package and append/retry contract.
- [Operations](docs/operations.md): sampling, delivery diagnostics, metrics, and upgrades.
- [v0.2 migration](docs/migration-v0.2.md) and [draft v0.3 release notes](docs/releases/v0.3.0.md).
- [Contributing](CONTRIBUTING.md): repository layout, tests, build tools, and release checks.

Telescope is licensed under the [Apache License, Version 2.0](LICENSE).
