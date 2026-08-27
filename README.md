# Telescope

Telescope is an OpenTelemetry Collector distribution that receives OTLP telemetry and appends it to ScopeDB. It is the data plane between OpenTelemetry producers and user-managed ScopeDB tables.

It provides:

- OTLP/gRPC and OTLP/HTTP receivers for logs, traces, and metrics
- user-owned signal-to-table mappings
- a ScopeDB exporter built on the Go SDK append API
- batching, retry, memory limiting, and a persistent sending queue
- mapping preview, additive ScopeDB table planning, preflight validation, live OTLP capture, and exact delivery probes
- operational health, readiness, and ingestion status endpoints

Telescope never executes DDL. You choose the signals, destination tables, and fields to append. `telescope plan` can compare that logical write contract with the live catalog and render reviewable, additive ScopeQL; table creation, physical design, and DDL execution remain user-owned. At runtime Telescope's responsibility ends when ScopeDB reports the append result, and it does not query or interpret stored data.

## Requirements

- Docker with Docker Compose for the recommended deployment path
- a reachable ScopeDB endpoint and API key
- pre-existing destination tables, or the ScopeQL CLI to apply a generated table plan
- OpenTelemetry clients, SDKs, or Collectors that can export OTLP

## Quick Start

Create the local configuration files:

```bash
cp deploy/.env.example deploy/.env
cp deploy/telescope.example.yaml deploy/telescope.yaml
```

Set the ScopeDB credentials in `deploy/.env`:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_...
```

Then edit `deploy/telescope.yaml`. Only configured signals are accepted and started:

```yaml
signals:
  traces:
    table: scopedb.otel.traces
    mapping:
      timestamp: span.start_time
      trace_id: span.trace_id
      span_id: span.span_id
      service:
        sources:
          - resource.attributes["service.name"]
          - resource.attributes["service"]
        default: unknown
        cast: string
      name: span.name
      duration_ns: span.duration_ns
      status_code: span.status.code
```

A mapping value can stay as a source-selector shorthand, or use an expanded rule for ordered fallback, a missing-value default, a constant, and an explicit output cast. Object and array sources support chained access such as `log.body["request"]["id"]`. See [ScopeDB Mapping and Table Management](docs/table-management.md) for the complete contract.

For runtime-typed selectors such as attributes and `log.body`, and for casts whose input values vary at runtime, preview a representative OTLP JSON or protobuf payload before deployment:

```bash
telescope preview --offline \
  --strict \
  --sample traces=traces.otlp.json \
  deploy/telescope.yaml
```

The preview shows destination-column coverage, observed output types, and which ordered source or default supplied each value, then prints projected NDJSON without writing to ScopeDB. Mapping failures are collected across the sample and identify the record, column, and selected source. `--strict` also fails on unobserved, partial, or default-only columns. Omit `--offline` to include destination column types and detect sample/type mismatches.

Plan missing tables or columns, review the generated ScopeQL, and apply it explicitly:

```bash
docker run --rm \
  --env-file deploy/.env \
  -v "$PWD/deploy/telescope.yaml:/etc/telescope/telescope.yaml:ro" \
  ghcr.io/scopedb/telescope:latest \
  plan --format scopeql /etc/telescope/telescope.yaml > tables.scopeql

scopeql run -f tables.scopeql
```

`plan` generates only the missing `CREATE DATABASE`, `CREATE SCHEMA`, `CREATE TABLE`, and `ALTER TABLE ... ADD COLUMN` statements, in dependency order. It blocks on runtime-dependent output types, sample conversion failures, shared-column type disagreements, and incompatible existing columns. A sample can provide evidence, but it never chooses a table type; add an explicit mapping `cast` when the source type is dynamic. Telescope does not infer retention, clustering, distinct keys, or indexes, so review and extend the ScopeQL before applying it.

Validate the applied table contract before deployment:

```bash
docker run --rm \
  --env-file deploy/.env \
  -v "$PWD/deploy/telescope.yaml:/etc/telescope/telescope.yaml:ro" \
  ghcr.io/scopedb/telescope:latest \
  validate /etc/telescope/telescope.yaml
```

Start Telescope:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml up -d
```

For a source build, run `make docker-build` and set `IMAGE=scopedb-telescope:ci` when invoking Docker Compose.

## Send Telemetry

The default listeners are:

- `localhost:4317` for OTLP/gRPC
- `localhost:4318` for OTLP/HTTP

For an OpenTelemetry SDK using OTLP/HTTP:

```bash
export OTEL_SERVICE_NAME=my-service
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=development
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

If another OpenTelemetry Collector already receives the telemetry, add Telescope as an OTLP exporter and include it in the existing pipelines:

```yaml
exporters:
  otlp/telescope:
    endpoint: telescope:4317
    tls:
      insecure: true

service:
  pipelines:
    traces:
      exporters: [otlp/telescope]
    metrics:
      exporters: [otlp/telescope]
    logs:
      exporters: [otlp/telescope]
```

## Operate Telescope

### Preview a Mapping with Live Data

Capture a bounded sample at the running exporter's input and pipe it directly through a candidate mapping:

```bash
telescope capture \
  --endpoint http://127.0.0.1:8080 \
  --limit 100 \
  --timeout 10s \
  traces |
  telescope preview \
    --sample traces=- \
    deploy/telescope.yaml
```

`capture` waits until it collects the requested number of log records, spans, or metric points, or until the timeout returns a partial sample. It retains nothing when no capture is active. The sample is copied before the exporter sending queue, so retries do not duplicate it. `preview` uses the production mapper and does not append the sample. Redirect `capture` to a file when the same input should be replayed later.

### Inspect Delivery Status

Telescope exposes its operational HTTP surface on `127.0.0.1:8080` in the Docker deployment:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/v1/ingestion/status
curl -sS http://127.0.0.1:8080/metrics
```

The ingestion status reports only configured signals, including received, written, and dropped counts; exhausted retries, permanent rejections, and queue refusals; queue utilization and allocated queue storage; table routes; destination validation; and the latest write result. These are local data-plane facts; Telescope does not query destination tables. Collector owns retries. Retryable requests have no elapsed-time expiry by default and remain in the bounded persistent queue across restarts; permanent failures stop immediately.

`/metrics` is the stable Prometheus surface for Telescope delivery and queue alerts. The bundled [Prometheus rules](deploy/prometheus-rules.yaml) cover queue saturation, final drops, stalled delivery, and unverified destinations, and record the queue directory's 24-hour disk high-water mark. Prometheus should also alert on its standard `up` metric: `/metrics` returns `503` instead of publishing false zeroes when Collector's internal telemetry is unavailable.

For a human-readable summary:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml \
  exec telescope telescope status
```

### Verify Delivery

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

## Local Binary

Build and run the embedded Collector:

```bash
make build

export SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
export SCOPEDB_API_KEY=sk_...

./bin/telescope preview --offline --sample traces=traces.otlp.json deploy/telescope.yaml
./bin/telescope plan --out tables.scopeql deploy/telescope.yaml
./bin/telescope validate deploy/telescope.yaml
./bin/telescope run deploy/telescope.yaml
```

The local CLI accepts the shared ScopeDB variables `SCOPEDB_ENDPOINT` and `SCOPEDB_API_KEY`, so the same credential file can be reused across ScopeDB tools. Command-line connection flags take precedence over `TELESCOPE_SCOPEDB_*`, which take precedence over the shared variables. The Docker Compose example keeps using its explicit `TELESCOPE_SCOPEDB_*` deployment variables.

Commands:

- Setup:
  - `telescope preview`: project OTLP samples through a candidate mapping without appending
  - `telescope plan`: compare mappings with live tables and render additive ScopeQL
  - `telescope validate`: validate mapping rules and destination tables
  - `telescope run`: run the OTLP-to-ScopeDB data plane from the same configuration
- Operations:
  - `telescope status`: report local receiver, queue, and ScopeDB delivery state
- Diagnostics:
  - `telescope capture`: capture a bounded live OTLP sample from a running instance
  - `telescope verify`: send synthetic OTLP and wait for confirmed ScopeDB appends
- `telescope version`: print the build version

`preview`, `plan`, `validate`, and `run` use the same `telescope.yaml` contract. `plan --out tables.scopeql` keeps the actionable human plan on stdout and atomically writes the executable DDL without a second catalog read; a blocked plan leaves any existing output file untouched. `plan --format json` exposes the versioned generated plan for tooling, while `plan --format scopeql` remains available for stdout pipelines. `capture`, `status`, and `verify` operate on a running instance. `preview --offline` projects a file or stdin sample without connecting to ScopeDB. `verify` uses a minimal synthetic record to confirm transport and append acknowledgement; it does not prove that application-specific fields are populated. The upstream Collector command remains available as the advanced escape hatch `telescope advanced collector --config <collector.yaml>`.

For the mapping contract and table ownership model, see [Mapping and Table Management](docs/table-management.md). For supported source selectors, see [Ingestion Compatibility](docs/ingestion-compatibility.md).

## Development

```bash
make test
make build
```

Project layout:

- `cmd/telescope`: Telescope CLI and runtime entrypoint
- `internal/collector`: embedded Collector configuration and component factories
- `internal/status`: operational health, readiness, and ingestion status endpoints
- `deploy`: Docker Compose deployment assets
- `packages/scopedbexporter`: ScopeDB OpenTelemetry Collector exporter
- `docs`: ingestion and table mapping documentation

Telescope is licensed under the [Apache License, Version 2.0](LICENSE).
