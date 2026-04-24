# scopedb/telescope

`scopedb/telescope` is an edge telemetry daemon for ScopeDB.
It has two jobs:

- ingest OpenTelemetry logs, traces, and metrics into ScopeDB through a custom OpenTelemetry Collector distribution
- expose a small agent-facing query surface over those landing tables through HTTP and MCP

The project is still early and intentionally compact. The main product direction is a local data-plane runtime that sits between application telemetry, ScopeDB, and developer agents.

## What This Is

- a deployable telemetry daemon for ScopeDB
- a ScopeDB-specific OTLP ingest path for logs, traces, and metrics
- an agent-oriented debug API over the ScopeDB OTel landing schema
- an MCP server for developer agents that need telemetry tools
- a semantic layer that keeps common debugging queries predictable and bounded

## What This Is Not

- not a fork of OpenTelemetry Collector
- not a runtime-loadable Collector plugin for stock Collector binaries
- not a managed observability control plane
- not a general arbitrary JSON-path query API over raw records

## Architecture

```text
apps / SDKs / upstream collectors
  -> OTLP gRPC or OTLP HTTP
  -> scopedb/telescope
  -> ScopeDB ingest tables
  -> semantic debug API / MCP tools
  -> developer agent / CLI / notebook
```

Telescope keeps the original mapped telemetry payload in `record` as evidence, but the query surface is based on promoted semantic fields such as `service_name`, `trace_id`, `message`, `status_code`, and `duration_ns`.
This is deliberate: ScopeDB can query arbitrary records, but the agent-facing API keeps default queries predictable and easier to optimize.

## Repository Layout

- `services/gateway/collector`: custom OTel Collector distribution, configs, and container build. The directory still uses the older `gateway` component name.
- `services/gateway/deploy`: Docker Compose and deployment environment examples for the collector runtime
- `services/api`: semantic debug API and MCP server over ScopeDB OTel tables
- `packages/vendordbexporter`: reusable Collector exporter module used by Telescope
- `docs`: design notes for the semantic debug API and query model

## Runtime Components

### Collector Runtime

The collector runtime accepts OTLP and writes to ScopeDB.

Default exposed ports:

- `4317`: OTLP gRPC
- `4318`: OTLP HTTP
- `13133`: Collector health check

Default ScopeDB tables:

- `scopedb.otel.logs`
- `scopedb.otel.traces`
- `scopedb.otel.metrics`

The production-style config is `services/gateway/collector/config/deploy.yaml`.
It enables `file_storage` for persistent queueing and can create the configured ScopeDB tables on startup when the API key has DDL permission.

### Debug API And MCP

The API service reads from the OTel landing tables and exposes a compact tool surface for developer agents.

HTTP routes:

- `GET /healthz`
- `GET /v1/schema`
- `GET /v1/schema/guide.md`
- `POST /v1/search`
- `POST /v1/aggregate`
- `POST /mcp`

MCP tools:

- `health`
- `schema`
- `schema_guide`
- `search`
- `aggregate`

MCP resources:

- `scopedb://telemetry/schema`
- `scopedb://telemetry/guide.md`

`/mcp` implements the Streamable HTTP shape for JSON-RPC requests. A stdio MCP binary is also provided for local agents that still prefer process-based MCP.

## Quick Start: Telescope Collector With Docker Compose

1. Copy the example environment file.

```bash
cp services/gateway/deploy/.env.example services/gateway/deploy/.env
```

2. Edit `services/gateway/deploy/.env`.

```bash
SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud
SCOPEDB_API_KEY=sk_...
```

3. Start the collector runtime.

```bash
docker compose -f services/gateway/deploy/docker-compose.yaml up -d --build
```

4. Send OTLP telemetry to Telescope.

- gRPC: `localhost:4317`
- HTTP: `localhost:4318`

## Quick Start: Debug API And MCP

The API service expects the same ScopeDB credentials:

```bash
cd services/api

SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud \
SCOPEDB_API_KEY=sk_... \
HTTP_ADDR=127.0.0.1:18080 \
go run ./cmd/scopedb-otel-debug-api
```

Check health:

```bash
curl -sS http://127.0.0.1:18080/healthz
```

Initialize MCP over HTTP:

```bash
curl -sS http://127.0.0.1:18080/mcp \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
```

Run the stdio MCP server:

```bash
cd services/api

SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud \
SCOPEDB_API_KEY=sk_... \
go run ./cmd/scopedb-otel-mcp
```

## Query Model

The semantic API is designed for agent-driven debugging.
It exposes two core query primitives:

- `search`: inspect detail rows and page through evidence with `next_cursor`
- `aggregate`: summarize trends and breakdowns with group-by, time buckets, and measures

Agents should bootstrap with:

1. call `schema` or read `scopedb://telemetry/schema`
2. use `schema_guide` for relation-specific hints and common patterns
3. use `search` for detail evidence
4. use `aggregate` for trend or breakdown checks
5. cite returned rows and `applied_query` when handing off findings

The API intentionally accepts promoted semantic fields only.
Arbitrary `record.*` paths are not accepted by `search` or `aggregate` filters.
If a raw attribute becomes important for repeated debugging, promote or materialize it first.

## Landing Tables

Telescope can create the default tables automatically with:

```yaml
create_tables_if_not_exist: true
```

If you prefer to manage schema manually, use equivalent tables:

```sql
CREATE TABLE IF NOT EXISTS scopedb.otel.logs (
  ingest_ts timestamp,
  record_timestamp timestamp,
  observed_timestamp timestamp,
  schema_version string,
  dataset string,
  service_name string,
  trace_id string,
  span_id string,
  severity_text string,
  message string,
  record object
);

CREATE TABLE IF NOT EXISTS scopedb.otel.traces (
  ingest_ts timestamp,
  start_timestamp timestamp,
  end_timestamp timestamp,
  duration_ns int,
  schema_version string,
  dataset string,
  service_name string,
  trace_id string,
  span_id string,
  parent_span_id string,
  span_name string,
  span_kind string,
  status_code string,
  record object
);

CREATE TABLE IF NOT EXISTS scopedb.otel.metrics (
  ingest_ts timestamp,
  record_timestamp timestamp,
  start_timestamp timestamp,
  schema_version string,
  dataset string,
  service_name string,
  metric_name string,
  metric_type string,
  temporality string,
  unit string,
  number_value float,
  distribution object,
  record object
);
```

## Build And Validate

Build the custom Collector:

```bash
make build-ocb
make build
```

Validate Collector configs:

```bash
SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud \
SCOPEDB_API_KEY=sk_... \
make validate

SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud \
SCOPEDB_API_KEY=sk_... \
make validate-deploy
```

Run Go tests for the API service:

```bash
cd services/api
go test ./...
```

Run exporter tests:

```bash
make test
```

The workspace pins Go `1.25` through the Makefile with `GOTOOLCHAIN=go1.25.3`.

## Send Test Telemetry

Examples with `telemetrygen`:

```bash
GOTOOLCHAIN=go1.25.3 go run github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen@v0.150.0 traces \
  --otlp-endpoint localhost:4317 \
  --otlp-insecure \
  --traces 3
```

```bash
GOTOOLCHAIN=go1.25.3 go run github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen@v0.150.0 logs \
  --otlp-endpoint localhost:4317 \
  --otlp-insecure \
  --logs 3
```

```bash
GOTOOLCHAIN=go1.25.3 go run github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen@v0.150.0 metrics \
  --otlp-endpoint localhost:4317 \
  --otlp-insecure \
  --metrics 3
```

## Reliability Semantics

The collector runtime uses standard Collector exporter helper behavior.

- delivery is at-least-once under retryable failures
- duplicates are possible
- queue overflow can drop data
- persistent queue survives Collector restart, but not disk failure
- if queue/storage cannot accept new items, drops can happen before retry logic runs

## Main Exporter Configuration

The `vendordb` exporter supports:

- `endpoint`: ScopeDB base URL
- `path`: ingest path, default `/v1/ingest`
- `api_key`: bearer token for ScopeDB
- `dataset`: logical dataset name
- `tables.logs`, `tables.traces`, `tables.metrics`: per-signal table routes
- `create_tables_if_not_exist`: create configured routes on startup
- `schema_version`: payload schema version
- `compression`: `zstd`, `gzip`, or `none`
- `timeout`: per-attempt exporter timeout
- `retry_on_failure`: exporterhelper retry policy
- `sending_queue`: exporterhelper queue policy, including optional `storage: file_storage`

## Contributing

Contribution rules live in `CONTRIBUTING.md`.
Pull requests use semantic titles such as `feat: add persistent queue deployment defaults` or `fix(exporter): classify 401 as permanent`.
