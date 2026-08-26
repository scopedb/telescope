# Telescope

Local telemetry runtime for developer agents, powered by ScopeDB.

Telescope receives OpenTelemetry data, stores it in ScopeDB, and exposes a small set of debugging tools that agents can use without learning your raw telemetry schema.

It is designed for the moment when an incident starts with partial context like a trace id, request id, service name, error string, or user report, and a developer agent needs to search evidence, aggregate trends, and hand findings back to a human.

## Why Telescope

- Bring telemetry closer to the developer agent instead of forcing every investigation through dashboards.
- Map the OpenTelemetry fields you need into ScopeDB tables you control.
- Give agents a tiny tool surface: discover schema, search details, aggregate trends.
- Run as a local or edge data-plane component, powered by your ScopeDB deployment.

## Requirements

- Docker with Docker Compose, for the recommended runtime path.
- A reachable ScopeDB endpoint and API key.
- OpenTelemetry clients, SDKs, or collectors that can export OTLP telemetry.

Telescope uses ScopeDB as the storage and query backend. The daemon needs valid ScopeDB credentials and pre-existing destination tables, but a temporary ScopeDB outage does not prevent the OTLP listeners and persistent queue from starting. Use `telescope ingestion check` before deployment to verify mapped columns and statically known types. Telescope does not create or modify tables.

## Quick Start

Create a local environment file:

```bash
cp services/gateway/deploy/.env.example services/gateway/deploy/.env
cp services/gateway/deploy/ingestion.example.yaml services/gateway/deploy/ingestion.yaml
```

Set your ScopeDB credentials in `services/gateway/deploy/.env`, then edit `services/gateway/deploy/ingestion.yaml` to select the signals, tables, and mappings you need:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_...
```

The example file leaves both values empty on purpose, so Docker Compose fails fast instead of starting a container with placeholder credentials.

The example enables only traces and targets `scopedb.otel.traces`. Create that table using the trace layout in [Mapping and Table Management](docs/table-management.md#starter-profile-tables), or replace the example route and mapping with an existing table.

Run the published GHCR image:

```bash
docker compose --env-file services/gateway/deploy/.env \
  -f services/gateway/deploy/docker-compose.yaml up -d
```

For source builds during development:

```bash
make docker-build

IMAGE=scopedb-telescope:ci \
docker compose --env-file services/gateway/deploy/.env \
  -f services/gateway/deploy/docker-compose.yaml up -d
```

Docker Compose mounts the ingestion file selected by `TELESCOPE_INGESTION_CONFIG`. It keeps the default ports unless you add explicit `TELESCOPE_*_PORT` overrides.

### Verify The Runtime

Check that the HTTP API is alive:

```bash
curl -sS http://127.0.0.1:8080/healthz
```

Expected response, with `version` matching the image or binary you are running:

```json
{"status":"ok","service":"telescope","version":"<version>"}
```

Check that the configured OTLP pipelines and listeners are ready. ScopeDB may still be temporarily degraded while this endpoint remains ready:

```bash
curl -sS http://127.0.0.1:8080/readyz
```

Check the OTLP-to-ScopeDB data path:

```bash
curl -sS http://127.0.0.1:8080/v1/ingestion/status
```

The response reports only configured signals, together with cumulative receiver/write counters, persistent queue size and capacity, table route, destination verification, and the latest ScopeDB write result. `ready`, `degraded`, and `refusing` describe current component health; counters and timestamps describe actual data flow.

Read the LLM-facing runtime map:

```bash
curl -sS http://127.0.0.1:8080/llms.txt
```

Initialize MCP over HTTP:

```bash
curl -sS http://127.0.0.1:8080/mcp \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
```

The Docker deployment publishes the HTTP API/MCP port on `127.0.0.1:${TELESCOPE_HTTP_PORT:-8080}` by default, so agent tools on the same host can use it without exposing query access on every interface.

### Send Telemetry

Send OTLP telemetry to the local runtime:

- `localhost:4317` for OTLP gRPC
- `localhost:4318` for OTLP HTTP

The deployment accepts only the signals present in `ingestion.yaml` and appends their configured mappings to ScopeDB.

One daemon can receive telemetry from every deployment environment. Set the standard OpenTelemetry resource attribute `deployment.environment.name` on producers, for example with `OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=production,service.name=api`. To store it, add `resource.attributes["deployment.environment.name"]` to each signal mapping and provide the corresponding destination column.

For an application using a standard OpenTelemetry SDK, point the common OTLP environment variables at Telescope's HTTP listener before starting the application:

```bash
export OTEL_SERVICE_NAME=my-service
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=development
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

If an OpenTelemetry Collector already receives the application's telemetry, add Telescope as its OTLP destination:

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

Merge the exporter into the existing pipelines; keep their current receivers and processors. Replace `telescope` with the Telescope host name or address visible from that Collector.

### Test The Complete Write Path

Send one synthetic trace and wait until that exact probe is appended to ScopeDB:

```bash
docker compose --env-file services/gateway/deploy/.env \
  -f services/gateway/deploy/docker-compose.yaml \
  exec scopedb-telescope telescope ingestion test --signal traces
```

Expected output:

```text
probe probe-...: OTLP accepted
probe probe-...: ScopeDB write confirmed
```

The probe uses runtime exporter acknowledgement and does not assume that any particular probe field is present in the user's mapping.

### Signal Coverage

Telescope currently focuses on traces and logs. Metrics ingestion is available, but the semantic fields, query patterns, and agent-facing guidance are still limited compared with trace and log workflows.

## Using Telescope

### Agent / MCP Usage

Telescope is intended to be used by developer agents as a small observability tool surface, not as a dashboard.

Recommended flow:

1. Call `schema` or read `scopedb://telemetry/schema`.
2. Use `schema_guide` to choose the right relation and fields.
3. Use `search` when evidence rows matter.
4. Use `aggregate` when volume, trend, or grouping matters.
5. Hand off the result with cited rows and `applied_query`.

The query surface accepts promoted semantic fields only. Raw `record` payloads remain available as evidence, but arbitrary `record.*` filters are intentionally not part of the default API.

### Local Binary

Build and run the same daemon directly:

```bash
make build

./bin/telescope daemon \
  --env-file services/gateway/deploy/.env \
  --ingestion-config services/gateway/deploy/ingestion.yaml
```

The same bootstrap config can also come from environment variables:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
./bin/telescope daemon --ingestion-config services/gateway/deploy/ingestion.yaml
```

Or from command flags:

```bash
./bin/telescope daemon \
  --scopedb-endpoint https://<region>.scopedb.cloud \
  --scopedb-api-key sk_... \
  --ingestion-config services/gateway/deploy/ingestion.yaml
```

For local agents that prefer stdio MCP:

```bash
./bin/telescope mcp --env-file services/gateway/deploy/.env
```

### HTTP API And MCP Tools

Telescope exposes five MCP tools:

- `health`: check service status
- `schema`: get the machine-readable semantic schema
- `schema_guide`: get an agent-readable Markdown guide
- `search`: inspect detail telemetry rows
- `aggregate`: summarize trends and breakdowns

The daemon HTTP server exposes:

- `GET /healthz`
- `GET /readyz`
- `GET /llms.txt`
- `GET /v1/ingestion/status`
- `GET /v1/schema`
- `GET /v1/schema/guide.md`
- `POST /v1/search`
- `POST /v1/aggregate`
- `POST /mcp`

## Development

For mapping, table routing, and DDL ownership, see [Mapping and Table Management](docs/table-management.md). The current source-selector contract is in [Ingestion Compatibility](docs/ingestion-compatibility.md).

Run all tests:

```bash
make test
```

Check license headers:

```bash
cargo install hawkeye --locked
make license-check
```

Run a focused package test from the repository root:

```bash
go test ./services/api/...
```

Build the local runtime:

```bash
make build
```

Validate the embedded collector config:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.<provider>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
make validate
```

### Release Artifacts

Build local release artifacts:

```bash
make artifacts
make docker-build
```

The artifact pipeline writes compressed binary bundles and `SHA256SUMS` under `dist/`.

Publish the release image by pushing a version tag such as `v0.2.0`; CI publishes `ghcr.io/scopedb/telescope`.

## Project

### Project Map

- `services/gateway/collector`: collector configs and Docker packaging
- `services/gateway/deploy`: Docker Compose deployment assets
- `services/api`: `telescope` binary, semantic HTTP API, MCP server, and embedded collector runtime
- `packages/scopedbexporter`: ScopeDB OpenTelemetry Collector exporter
- `docs`: design notes

### Status

Telescope is an early prototype. The next roadmap phase focuses on making OTLP ingestion into ScopeDB complete, efficient, and operable. MCP and query features remain available but are outside the ingestion roadmap.

### License

This project is licensed under [Apache License, Version 2.0](LICENSE).
