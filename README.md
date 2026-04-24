# Telescope

Local telemetry runtime for developer agents, powered by ScopeDB.

Telescope receives OpenTelemetry data, stores it in ScopeDB, and exposes a small set of debugging tools that agents can use without learning your raw telemetry schema.

It is designed for the moment when an incident starts with partial context like a trace id, request id, service name, error string, or user report, and a developer agent needs to search evidence, aggregate trends, and hand findings back to a human.

## Why Telescope

- Bring telemetry closer to the developer agent instead of forcing every investigation through dashboards.
- Keep raw telemetry available as evidence while exposing a safer semantic query layer.
- Give agents a tiny tool surface: discover schema, search details, aggregate trends.
- Run as a local or edge data-plane component, powered by your ScopeDB deployment.

## Quick Start

Start the local runtime:

```bash
cp services/gateway/deploy/.env.example services/gateway/deploy/.env
```

Set your ScopeDB credentials in `services/gateway/deploy/.env`:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_...
TELESCOPE_ENV=default
TELESCOPE_OTLP_GRPC_PORT=4317
TELESCOPE_OTLP_HTTP_PORT=4318
TELESCOPE_HTTP_PORT=8080
TELESCOPE_HEALTH_PORT=13133
```

Run Telescope:

```bash
docker compose -f services/gateway/deploy/docker-compose.yaml up -d --build
```

Send OTLP telemetry to:

- `localhost:4317` for OTLP gRPC
- `localhost:4318` for OTLP HTTP

The Docker deployment publishes the HTTP API/MCP port on `127.0.0.1:${TELESCOPE_HTTP_PORT:-8080}` by default, so agent tools on the same host can use it without exposing query access on every interface.

Change the `TELESCOPE_*_PORT` values if those ports are already in use.

## Local Binary

Build and run the same daemon directly:

```bash
make build

TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
./bin/telescope daemon
```

Check that it is alive:

```bash
curl -sS http://127.0.0.1:8080/healthz
```

Initialize MCP over HTTP:

```bash
curl -sS http://127.0.0.1:8080/mcp \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
```

For local agents that prefer stdio:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
./bin/telescope mcp
```

## Tools

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

## How Agents Should Use It

1. Call `schema` or read `scopedb://telemetry/schema`.
2. Use `schema_guide` to choose the right relation and fields.
3. Use `search` when evidence rows matter.
4. Use `aggregate` when volume, trend, or grouping matters.
5. Hand off the result with cited rows and `applied_query`.

The query surface accepts promoted semantic fields only. Raw `record` payloads remain available as evidence, but arbitrary `record.*` filters are intentionally not part of the default API.

## Develop

For table creation and routing details, see [docs/table-management.md](docs/table-management.md).

Run all tests:

```bash
make test
```

Run a focused package test from the repository root:

```bash
go test ./services/api/...
```

Build the local runtime:

```bash
make build
```

Validate collector configs:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
make validate

TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
make validate-deploy
```

## Project Map

- `services/gateway/collector`: collector configs and Docker packaging
- `services/gateway/deploy`: Docker Compose deployment assets
- `services/api`: `telescope` binary, semantic HTTP API, MCP server, and embedded collector runtime
- `packages/scopedbexporter`: ScopeDB OpenTelemetry Collector exporter
- `docs`: design notes

## Status

Telescope is an early prototype. The current focus is the agent-facing debugging loop, not dashboards or a managed control plane.

For contribution rules, see `CONTRIBUTING.md`.
