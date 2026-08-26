# Telescope

Telescope is an OpenTelemetry Collector distribution that receives OTLP telemetry and appends it to ScopeDB. It is the data plane between OpenTelemetry producers and user-managed ScopeDB tables.

It provides:

- OTLP/gRPC and OTLP/HTTP receivers for logs, traces, and metrics
- user-owned signal-to-table mappings
- a ScopeDB exporter built on the Go SDK append API
- batching, retry, memory limiting, and a persistent sending queue
- preflight validation and exact ScopeDB delivery probes
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
      service: resource.attributes["service.name"]
      name: span.name
      duration_ns: span.duration_ns
      status_code: span.status.code
```

Validate the destination table and mapping before deployment:

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

### Inspect Delivery Status

Telescope exposes its operational HTTP surface on `127.0.0.1:8080` in the Docker deployment:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/v1/ingestion/status
```

The ingestion status reports only configured signals, including received, written, and dropped counts; exhausted retries, permanent rejections, and queue refusals; queue utilization; table routes; destination validation; and the latest write result. These are local data-plane facts; Telescope does not query destination tables. Collector owns retries and stops retrying a request after 10 minutes by default.

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
traces: OTLP accepted (probe-...)
traces: ScopeDB append committed (probe-...)
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
  - `telescope validate`: validate the configuration and its destination tables
  - `telescope run`: run the OTLP-to-ScopeDB data plane from the same configuration
- Operations:
  - `telescope status`: report local receiver, queue, and ScopeDB delivery state
- Diagnostics:
  - `telescope verify`: send synthetic OTLP and wait for confirmed ScopeDB appends
- `telescope version`: print the build version

`validate` and `run` use the same `telescope.yaml` contract. `status` and `verify` are operational tools for a running instance, not additional setup stages; both discover enabled signals from that instance. `validate --offline` checks the file without connecting to ScopeDB. The upstream Collector command remains available as the advanced escape hatch `telescope advanced collector --config <collector.yaml>`.

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
