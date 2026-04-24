# API Service

This directory is reserved for the next service in this repository: an agent-facing observability API built on top of the ScopeDB OTel landing schema.

The semantic design for this service lives in [../../docs/semantic-debug-api.md](../../docs/semantic-debug-api.md).

## Purpose

The service is expected to:

- expose agent-oriented observability tools over the current OTel landing tables
- compile semantic API requests into ScopeQL
- keep the landing schema as the evidence layer
- evolve through a small semantic field registry and relation registry
- expose both canonical JSON schema introspection and a Markdown schema guide for agent planning

The semantic registry source of truth currently lives in Go code under `internal/semantic/`.

## Planned Layout

- `cmd/`: service entrypoints
- `internal/`: query compiler, semantic registry, ScopeDB execution, and Echo handlers
- `openapi/`: hand-written OpenAPI contracts for the Echo HTTP surface, with the agent contract as source of truth

## Expected First Milestones

1. Define the semantic field registry and relation registry.
2. Implement `GET /v1/schema`.
3. Implement `POST /v1/search`.
4. Implement `POST /v1/aggregate`.

## HTTP Contract

The primary HTTP contract should stay hand-written and live under `openapi/agent-openapi.yaml`.

- the contract is intended to be implemented with Echo
- request and response shapes should stay close to the current semantic query compiler
- the public surface should stay limited to `schema`, `schema/guide.md`, `search`, and `aggregate`
- generated artifacts can be added later if the service adopts them

## Binary

The runnable binaries are:

- `cmd/scopedb-otel-debug-api`: Echo HTTP API for the agent-oriented routes described in `openapi/agent-openapi.yaml`, plus a Streamable HTTP MCP endpoint at `/mcp`
- `cmd/scopedb-otel-mcp`: stdio MCP server exposing the same telemetry service as tools and resources

Required environment variables:

- `TELESCOPE_SCOPEDB_ENDPOINT`
- `TELESCOPE_SCOPEDB_API_KEY`

Optional environment variables:

- `TELESCOPE_HTTP_ADDR`: listen address, default `:8080`
- `TELESCOPE_PORT`: alternate way to set the listen port when `TELESCOPE_HTTP_ADDR` is unset
- `TELESCOPE_QUERY_TIMEOUT`: per-query timeout, default `15s`

Example:

```bash
go run ./cmd/scopedb-otel-debug-api
```

Streamable HTTP MCP example:

```bash
curl -sS http://127.0.0.1:8080/mcp \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
```

stdio MCP example:

```bash
go run ./cmd/scopedb-otel-mcp
```

MCP tools:

- `health`
- `schema`
- `schema_guide`
- `search`
- `aggregate`

MCP resources:

- `scopedb://telemetry/schema`
- `scopedb://telemetry/guide.md`
