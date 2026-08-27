# Telescope

Telescope is an OpenTelemetry Collector distribution that receives OTLP telemetry and appends it to ScopeDB. It is the data plane between OpenTelemetry producers and user-managed ScopeDB tables.

It provides:

- OTLP/gRPC and OTLP/HTTP receivers for logs, traces, and metrics
- user-owned signal-to-table mappings
- a ScopeDB exporter built on the Go SDK append API
- batching, retry, memory limiting, and a persistent sending queue
- preflight validation, live OTLP capture, mapping preview, and exact ScopeDB delivery probes
- operational health, readiness, and ingestion status endpoints

Telescope does not create or modify ScopeDB tables. You choose the signals to enable, the destination table for each signal, and the fields to append. Its responsibility ends when ScopeDB reports the append result; it does not query or interpret stored data.

## Requirements

- Docker with Docker Compose for the recommended deployment path
- a reachable ScopeDB endpoint and API key
- pre-existing destination tables
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

Validate the destination table and mapping before deployment:

```bash
docker run --rm \
  --env-file deploy/.env \
  -v "$PWD/deploy/telescope.yaml:/etc/telescope/telescope.yaml:ro" \
  ghcr.io/scopedb/telescope:latest \
  validate /etc/telescope/telescope.yaml
```

For runtime-typed selectors such as attributes and `log.body`, and for casts whose input values vary at runtime, preview a representative OTLP JSON or protobuf payload before deployment:

```bash
telescope preview --offline \
  --strict \
  --sample traces=traces.otlp.json \
  deploy/telescope.yaml
```

The preview shows destination-column coverage, observed output types, and which ordered source or default supplied each value, then prints projected NDJSON without writing to ScopeDB. Mapping failures are collected across the sample and identify the record, column, and selected source. `--strict` also fails on unobserved, partial, or default-only columns. Omit `--offline` to include destination column types and detect sample/type mismatches.

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

./bin/telescope validate --env-file deploy/.env deploy/telescope.yaml
./bin/telescope run --env-file deploy/.env deploy/telescope.yaml
```

Commands:

- Setup:
  - `telescope validate`: validate mapping rules and destination tables
  - `telescope preview`: project OTLP samples through a candidate mapping without appending
  - `telescope run`: run the OTLP-to-ScopeDB data plane from the same configuration
- Operations:
  - `telescope status`: report local receiver, queue, and ScopeDB delivery state
- Diagnostics:
  - `telescope capture`: capture a bounded live OTLP sample from a running instance
  - `telescope verify`: send synthetic OTLP and wait for confirmed ScopeDB appends
- `telescope version`: print the build version

`validate`, `preview`, and `run` use the same `telescope.yaml` contract. `capture`, `status`, and `verify` operate on a running instance. `preview --offline` projects a file or stdin sample without connecting to ScopeDB. `verify` uses a minimal synthetic record to confirm transport and append acknowledgement; it does not prove that application-specific fields are populated. The upstream Collector command remains available as the advanced escape hatch `telescope advanced collector --config <collector.yaml>`.

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
