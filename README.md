# ScopeDB OTel Collector Gateway

`scopedb-otel` is a deployable custom OpenTelemetry Collector daemon for ScopeDB.
It accepts OTLP logs, traces, and metrics over gRPC or HTTP, then writes them into ScopeDB through the public `/v1/ingest` API.

This repository is organized around the current gateway service, while leaving room for additional services later:

- `services/gateway`: the deployable OTel gateway service
- `packages/vendordbexporter`: the reusable Collector exporter module used by the gateway

Contribution rules live in [CONTRIBUTING.md](/Users/leiysky/work/scopedb-workspace/scopedb-otel/CONTRIBUTING.md:1).
Pull requests use semantic PR titles such as `feat: ...` or `fix: ...`.

## What This Is

- a standalone Collector gateway daemon you can run as a container or service
- a ScopeDB-specific OTLP ingress path
- a custom Collector distribution that already includes the exporter
- a persistent-queue-capable gateway using `file_storage`

## What This Is Not

- not a fork of OpenTelemetry Collector
- not an upstream contrib component
- not a runtime-loadable Collector plugin
- not a general-purpose observability control plane

## Deployment Model

This project is meant to run as its own Collector process:

```text
apps / agents / upstream collectors
  -> OTLP gRPC or OTLP HTTP
  -> scopedb-otel-gateway
  -> ScopeDB /v1/ingest
  -> target ScopeDB table
```

By default the deployed gateway exposes:

- `4317` for OTLP gRPC
- `4318` for OTLP HTTP
- `13133` for health checks

The production-style config is [services/gateway/collector/config/deploy.yaml](/Users/leiysky/work/scopedb-workspace/scopedb-otel/services/gateway/collector/config/deploy.yaml:1).
It enables `file_storage` and stores the persistent queue under `/var/lib/vendor-otelcol/queue`.
It also enables `create_tables_if_not_exist: true`, so the deployed gateway will create every configured target table on startup when the API key has DDL permission.

## How It Integrates With OpenTelemetry Collector

There are three common integration patterns.

### 1. Use It As Your Gateway Collector

Point your SDKs, agents, or sidecars directly at this daemon.
This is the simplest model and the one this repo is optimized for.

### 2. Put It Behind An Existing Collector

If you already run a standard OTel Collector, keep that layer for routing, sampling, or transforms, then export OTLP to this gateway.

Example upstream Collector snippet:

```yaml
exporters:
  otlp/scopedb_gateway:
    endpoint: scopedb-otel-gateway:4317
    tls:
      insecure: true

service:
  pipelines:
    logs:
      exporters: [otlp/scopedb_gateway]
    traces:
      exporters: [otlp/scopedb_gateway]
    metrics:
      exporters: [otlp/scopedb_gateway]
```

### 3. Rebuild It Into Your Own Custom Distribution

If you already maintain your own OCB build, you can import the exporter module from `packages/vendordbexporter` into your own `builder-config.yaml`.

Important: this exporter is linked at build time.
You cannot take a stock Collector binary and add `vendordb` at runtime through config alone.

## Quick Start With Docker Compose

1. Copy the example environment file.

```bash
cp services/gateway/deploy/.env.example services/gateway/deploy/.env
```

2. Edit `services/gateway/deploy/.env` and set:

- `SCOPEDB_ENDPOINT`
- `SCOPEDB_API_KEY`

By default, the gateway writes to:

- `scopedb.otel.logs`
- `scopedb.otel.traces`
- `scopedb.otel.metrics`

3. Start the gateway.

```bash
docker compose -f services/gateway/deploy/docker-compose.yaml up -d --build
```

4. Send OTLP data to:

- `localhost:4317` for gRPC
- `localhost:4318` for HTTP

The Compose definition lives at [services/gateway/deploy/docker-compose.yaml](/Users/leiysky/work/scopedb-workspace/scopedb-otel/services/gateway/deploy/docker-compose.yaml:1).

## Run The Container Directly

Build the image:

```bash
make docker-build
```

Run it:

```bash
docker run --rm \
  -p 4317:4317 \
  -p 4318:4318 \
  -p 13133:13133 \
  -e SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud \
  -e SCOPEDB_API_KEY=sk_... \
  -v scopedb-otel-queue:/var/lib/vendor-otelcol/queue \
  scopedb-otel-gateway:0.1.0
```

The image entrypoint and default config are defined in [services/gateway/collector/Dockerfile](/Users/leiysky/work/scopedb-workspace/scopedb-otel/services/gateway/collector/Dockerfile:1).

## Environment Variables

The deployment-facing config is environment-driven:

- `SCOPEDB_ENDPOINT`: ScopeDB base URL
- `SCOPEDB_API_KEY`: ScopeDB API key

Auth is sent as `Authorization: Bearer <token>`, which matches the current ScopeDB client behavior.
Tenant selection is handled by the ScopeDB SaaS auth gateway, so there is no separate tenant config.
The deployment config creates the built-in OTel tables automatically on startup.

## Table Initialization

The deployment config auto-creates the target table on startup with:

```yaml
create_tables_if_not_exist: true
```

That startup DDL path uses the official ScopeDB Go SDK `github.com/scopedb/scopedb-sdk/go v0.5.0`.

By default the gateway routes signals to:

- `scopedb.otel.logs`
- `scopedb.otel.traces`
- `scopedb.otel.metrics`

If you prefer to manage schema yourself or your API key does not have DDL permission, disable that flag in a custom Collector config and create the table manually with:

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
)
```

## Local Development

Build and validate the custom Collector locally:

```bash
make build-ocb
make build
SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud \
SCOPEDB_API_KEY=sk_... \
make validate

SCOPEDB_ENDPOINT=https://your-workspace.scopedb.cloud \
SCOPEDB_API_KEY=sk_... \
make validate-deploy
```

The workspace pins Go `1.25` via `toolchain go1.25.3`.
If your default Go is older, `GOTOOLCHAIN=go1.25.3` is already wired into the Makefile.

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

## Repository Layout

- `services/gateway/collector`: OCB distribution, configs, and container build for the gateway service
- `services/gateway/deploy`: Docker Compose and environment examples for the gateway service
- `packages/vendordbexporter`: standalone exporter Go module that can also be reused by other Collector builds

## Reliability Semantics

- at-least-once delivery under retryable failures
- duplicates are possible
- queue overflow can still drop data
- persistent queue survives Collector restart, but not disk failure
- if the queue or storage layer cannot accept new items, drops can happen before retry logic runs

## Main Configuration Surface

The `vendordb` exporter supports these main fields:

- `endpoint`: base URL for the ingest backend
- `path`: ingest path, default `/v1/ingest`
- `api_key`: secret used for backend authentication
- `dataset`: logical dataset name
- built-in defaults: `scopedb.otel.logs`, `scopedb.otel.traces`, `scopedb.otel.metrics`
- table routes accept `table`, `schema.table`, or `database.schema.table`
- `tables.logs`, `tables.traces`, `tables.metrics`: required per-signal table routes, each pointing to a distinct table
- `create_tables_if_not_exist`: automatically ensures the configured database, schema, and table exist for all configured routes on startup
- `schema_version`: payload schema version
- `compression`: `zstd`, `gzip`, or `none` (`zstd` is the default)
- `timeout`: per-attempt exporter timeout
- `retry_on_failure`: exporterhelper retry policy
- `sending_queue`: exporterhelper queue policy, including optional `storage: file_storage`
