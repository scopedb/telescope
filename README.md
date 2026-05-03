# Telescope

Local telemetry runtime for developer agents, powered by ScopeDB.

Telescope receives OpenTelemetry data, stores it in ScopeDB, and exposes a small set of debugging tools that agents can use without learning your raw telemetry schema.

It is designed for the moment when an incident starts with partial context like a trace id, request id, service name, error string, or user report, and a developer agent needs to search evidence, aggregate trends, and hand findings back to a human.

## Why Telescope

- Bring telemetry closer to the developer agent instead of forcing every investigation through dashboards.
- Keep raw telemetry available as evidence while exposing a safer semantic query layer.
- Give agents a tiny tool surface: discover schema, search details, aggregate trends.
- Run as a local or edge data-plane component, powered by your ScopeDB deployment.

## Requirements

- Docker with Docker Compose, for the recommended runtime path.
- A reachable ScopeDB endpoint and API key.
- OpenTelemetry clients, SDKs, or collectors that can export OTLP telemetry.

Telescope uses ScopeDB as the storage and query backend. The daemon needs valid ScopeDB credentials to stay running, and the embedded collector config creates or verifies the default telemetry tables on startup.

## Quick Start

Create a local environment file:

```bash
cp services/gateway/deploy/.env.example services/gateway/deploy/.env
```

Set your ScopeDB credentials in `services/gateway/deploy/.env` before starting Telescope:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_...
```

The example file leaves both values empty on purpose, so Docker Compose fails fast instead of starting a container with placeholder credentials.

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

The bootstrap file only needs the ScopeDB endpoint and API key. Docker Compose keeps the default ports unless you add explicit `TELESCOPE_*_PORT` overrides.

### Verify The Runtime

Check that the HTTP API is alive:

```bash
curl -sS http://127.0.0.1:8080/healthz
```

Expected response, with `version` matching the image or binary you are running:

```json
{"status":"ok","service":"telescope","version":"<version>"}
```

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

The default deployment accepts logs, traces, and metrics, stores them in ScopeDB, and exposes supported fields through Telescope's semantic query layer.

One daemon can receive telemetry from every deployment environment. Set the OpenTelemetry resource attribute `deployment.environment.name` on producers, for example with `OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=production,service.name=api`; Telescope stores that value in the top-level `env` column for filtering and grouping. If no environment attribute is present, `env` is stored as `default`.

### Send A Test Trace

Use OpenTelemetry's `telemetrygen` container to send one trace without installing a local generator:

```bash
docker run --rm --add-host=host.docker.internal:host-gateway \
  ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:v0.150.0 \
  traces \
  --otlp-endpoint host.docker.internal:4317 \
  --otlp-insecure \
  --service telescope-smoke \
  --traces 1
```

Then query recent root spans through Telescope:

```bash
curl -sS http://127.0.0.1:8080/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "source": "executions_v1",
    "time_range": {"start": "1970-01-01T00:00:00Z"},
    "filter": {"eq": {"field": "service", "value": "telescope-smoke"}},
    "project": ["ts", "service", "trace_id", "operation", "duration_ns"],
    "limit": 5
  }'
```

For existing tables with substantial telemetry, narrow the `time_range.start` value before querying.

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

./bin/telescope daemon --env-file services/gateway/deploy/.env
```

The same bootstrap config can also come from environment variables:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
./bin/telescope daemon
```

Or from command flags:

```bash
./bin/telescope daemon \
  --scopedb-endpoint https://<region>.scopedb.cloud \
  --scopedb-api-key sk_...
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

- `GET /llms.txt`
- `GET /v1/schema`
- `GET /v1/schema/guide.md`
- `POST /v1/search`
- `POST /v1/aggregate`
- `POST /mcp`

## Development

For table creation and routing details, see [Table Management](docs/table-management.md).

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

Telescope is an early prototype. The current focus is the agent-facing debugging loop, not dashboards or a managed control plane.

### License

This project is licensed under [Apache License, Version 2.0](LICENSE).
