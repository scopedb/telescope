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
- the agent query surface should stay limited to `schema`, `schema/guide.md`, `search`, and `aggregate`
- generated artifacts can be added later if the service adopts them

The daemon also serves `/readyz` for ingestion readiness, `/llms.txt` as an LLM-readable runtime map, and `/v1/ingestion/status` as an operational OTLP-to-ScopeDB status endpoint. The ingestion endpoint is not an agent query primitive.

## Binary

The runnable binary is:

- `cmd/telescope`: unified local runtime with `daemon`, `ingestion`, `mcp`, `collector`, and `version` commands

Required environment variables:

- `TELESCOPE_SCOPEDB_ENDPOINT`
- `TELESCOPE_SCOPEDB_API_KEY`

The `daemon` and `mcp` commands can also load these values from `--env-file` or receive them through `--scopedb-endpoint` and `--scopedb-api-key` flags. Flag values override the process environment; the process environment overrides values loaded from an env file.

Optional environment variables:

- `TELESCOPE_HTTP_ADDR`: listen address, default `:8080`
- `TELESCOPE_PORT`: alternate way to set the listen port when `TELESCOPE_HTTP_ADDR` is unset
- `TELESCOPE_OTLP_GRPC_ADDR`: OTLP gRPC listen address, default `0.0.0.0:4317`
- `TELESCOPE_OTLP_HTTP_ADDR`: OTLP HTTP listen address, default `0.0.0.0:4318`
- `TELESCOPE_HEALTH_ADDR`: collector health listen address, default `0.0.0.0:13133`
- `TELESCOPE_QUEUE_DIR`: persistent queue directory, default `$HOME/.telescope/queue`
- `TELESCOPE_QUEUE_MAX_BYTES`: logical queued telemetry byte capacity, default `536870912` (512 MiB); file encoding and storage overhead make actual disk use different
- `TELESCOPE_OTEL_BATCH_TIMEOUT`: embedded Collector batch timeout, default `30s`
- `TELESCOPE_OTEL_BATCH_SIZE`: embedded Collector send batch size, default `2000`
- `TELESCOPE_OTEL_BATCH_MAX_SIZE`: embedded Collector send batch max size, default `2000`
- `TELESCOPE_INTERNAL_METRICS_URL`: Collector internal Prometheus endpoint used by ingestion status, default `http://127.0.0.1:8888/metrics`
- `TELESCOPE_QUERY_TIMEOUT`: per-query timeout, default `15s`
- `TELESCOPE_INGESTION_CONFIG`: user-owned tables and mappings YAML file
- `TELESCOPE_INGESTION_PROFILE`: built-in ingestion profile; currently `starter`
- `TELESCOPE_COLLECTOR_CONFIG`: full Collector config URI or file path

The daemon requires one explicit ingestion choice: `--ingestion-config`, `--ingestion-profile starter`, or a full `--collector-config`. The corresponding environment variables are alternatives to the flags. The starter profile is a runnable example layout, not a universal telemetry schema.

Use `telescope ingestion check` for live destination column and type validation. Use `telescope ingestion test --signal <signal>` against a running daemon to confirm an exact synthetic record reached ScopeDB.

The daemon accepts telemetry for every deployment environment on the same OTLP listeners. Store an environment attribute only when the configured signal mapping selects it.

Example:

```bash
go run ./cmd/telescope daemon \
  --env-file ../../services/gateway/deploy/.env \
  --ingestion-config ../../services/gateway/deploy/ingestion.yaml
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
go run ./cmd/telescope mcp --env-file ../../services/gateway/deploy/.env
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
