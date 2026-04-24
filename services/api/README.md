# Telescope Runtime

This directory contains the `telescope` binary: a local runtime that receives OTLP telemetry, writes it to ScopeDB, and exposes agent-facing HTTP/MCP debugging tools.

The semantic design for this service lives in [../../docs/semantic-debug-api.md](../../docs/semantic-debug-api.md).

## Purpose

The runtime:

- embeds an OpenTelemetry Collector runtime with the ScopeDB exporter
- exposes agent-oriented observability tools over the current OTel landing tables
- compiles semantic API requests into ScopeQL
- keeps the landing schema as the evidence layer
- evolves through a small semantic field registry and relation registry
- exposes both canonical JSON schema introspection and a Markdown schema guide for agent planning

The semantic registry source of truth currently lives in Go code under `internal/semantic/`.

## Layout

- `cmd/telescope`: unified CLI entrypoint
- `internal/appruntime`: shared API/MCP service wiring
- `internal/collector`: embedded Collector factories and default config
- `internal/httpapi`: Echo handlers and semantic API service
- `internal/mcpserver`: stdio and Streamable HTTP MCP implementation
- `internal/semantic`: field registry and ScopeQL query compiler
- `openapi/`: hand-written OpenAPI contracts for the Echo HTTP surface, with the agent contract as source of truth

## HTTP Contract

The primary HTTP contract should stay hand-written and live under `openapi/agent-openapi.yaml`.

- the contract is intended to be implemented with Echo
- request and response shapes should stay close to the current semantic query compiler
- the public surface should stay limited to `schema`, `schema/guide.md`, `search`, and `aggregate`
- generated artifacts can be added later if the service adopts them

The daemon also serves `/llms.txt` from the same HTTP server as an LLM-readable runtime map.

## Binary

The runnable binary is:

- `cmd/telescope`: unified local runtime with `daemon`, `mcp`, `collector`, and `version` commands

Required environment variables:

- `TELESCOPE_SCOPEDB_ENDPOINT`
- `TELESCOPE_SCOPEDB_API_KEY`

Optional environment variables:

- `TELESCOPE_ENV`: telemetry environment label, default `default`
- `TELESCOPE_HTTP_ADDR`: listen address, default `:8080`
- `TELESCOPE_PORT`: alternate way to set the listen port when `TELESCOPE_HTTP_ADDR` is unset
- `TELESCOPE_OTLP_GRPC_ADDR`: OTLP gRPC listen address, default `0.0.0.0:4317`
- `TELESCOPE_OTLP_HTTP_ADDR`: OTLP HTTP listen address, default `0.0.0.0:4318`
- `TELESCOPE_HEALTH_ADDR`: collector health listen address, default `0.0.0.0:13133`
- `TELESCOPE_QUEUE_DIR`: persistent queue directory, default `$HOME/.telescope/queue`
- `TELESCOPE_QUERY_TIMEOUT`: per-query timeout, default `15s`
- `TELESCOPE_COLLECTOR_CONFIG`: collector config URI or file path; unset uses the embedded default config

Example:

```bash
go run ./cmd/telescope daemon
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
go run ./cmd/telescope mcp
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
