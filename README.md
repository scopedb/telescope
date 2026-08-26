# Telescope

Telescope is an OpenTelemetry Collector distribution that receives OTLP telemetry and appends it to ScopeDB.

It provides:

- OTLP/gRPC and OTLP/HTTP receivers for logs, traces, and metrics
- user-owned signal-to-table mappings
- a ScopeDB exporter built on the Go SDK append API
- batching, retry, memory limiting, and a persistent sending queue
- preflight validation and an end-to-end ingestion probe
- operational health, readiness, and ingestion status endpoints

Telescope does not create or modify ScopeDB tables. You choose the signals to enable, the destination table for each signal, and the fields to append.

## Requirements

- Docker with Docker Compose for the recommended deployment path
- a reachable ScopeDB endpoint and API key
- pre-existing destination tables
- OpenTelemetry clients, SDKs, or Collectors that can export OTLP

## Quick Start

Create the local configuration files:

```bash
cp deploy/.env.example deploy/.env
cp deploy/ingestion.example.yaml deploy/ingestion.yaml
```

Set the ScopeDB credentials in `deploy/.env`:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_...
```

Then edit `deploy/ingestion.yaml`. Only configured signals are accepted and started:

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
  -v "$PWD/deploy/ingestion.yaml:/etc/telescope/ingestion.yaml:ro" \
  ghcr.io/scopedb/telescope:latest \
  ingestion check --config /etc/telescope/ingestion.yaml
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

## Verify Ingestion

The daemon exposes a small operational HTTP surface on `127.0.0.1:8080` in the Docker deployment:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/v1/ingestion/status
```

The ingestion status reports only configured signals, including receiver and write counters, queue utilization, table routes, destination validation, and the latest write result.

To send a synthetic signal and wait for the exact exporter acknowledgement:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml \
  exec telescope telescope ingestion test --signal traces
```

Expected output:

```text
probe probe-...: OTLP accepted
probe probe-...: ScopeDB write confirmed
```

## Local Binary

Build and run the embedded Collector:

```bash
make build

./bin/telescope daemon \
  --env-file deploy/.env \
  --ingestion-config deploy/ingestion.yaml
```

Commands:

- `telescope daemon`: run the configured Collector and operational endpoints
- `telescope ingestion check`: validate an ingestion configuration and its destination tables
- `telescope ingestion test`: test one running signal pipeline end to end
- `telescope collector`: run the upstream Collector command with Telescope's component set
- `telescope version`: print the build version

The daemon requires one explicit ingestion choice: `--ingestion-config`, `--ingestion-profile starter`, or `--collector-config`. The equivalent environment variables are `TELESCOPE_INGESTION_CONFIG`, `TELESCOPE_INGESTION_PROFILE`, and `TELESCOPE_COLLECTOR_CONFIG`.

For the mapping contract and table ownership model, see [Mapping and Table Management](docs/table-management.md). For supported source selectors, see [Ingestion Compatibility](docs/ingestion-compatibility.md).

## Development

```bash
make test
make build
```

Project layout:

- `cmd/telescope`: Telescope CLI and daemon entrypoint
- `internal/collector`: embedded Collector configuration and component factories
- `internal/status`: operational health, readiness, and ingestion status endpoints
- `deploy`: Docker Compose deployment assets
- `packages/scopedbexporter`: ScopeDB OpenTelemetry Collector exporter
- `docs`: ingestion and table mapping documentation

Telescope is licensed under the [Apache License, Version 2.0](LICENSE).
