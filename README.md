# ScopeDB OTel Collector Gateway

`scopedb-otel` is a deployable custom OpenTelemetry Collector daemon for ScopeDB.
It accepts OTLP logs, traces, and metrics over gRPC or HTTP, then writes them into ScopeDB through the public `/v1/ingest` API.

This repository contains two deliverables:

- `vendor-otelcol`: a custom Collector distribution built with OCB
- `vendordbexporter`: a standalone Collector exporter module under `exporter/vendordbexporter`

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

The production-style config is [otelcol/config/deploy.yaml](/Users/leiysky/work/scopedb-workspace/scopedb-otel/otelcol/config/deploy.yaml:1).
It enables `file_storage` and stores the persistent queue under `/var/lib/vendor-otelcol/queue`.
It also enables `create_table_if_not_exists: true`, so the deployed gateway will create the target table on startup when the API key has DDL permission.

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

If you already maintain your own OCB build, you can import the exporter module from `exporter/vendordbexporter` into your own `builder-config.yaml`.

Important: this exporter is linked at build time.
You cannot take a stock Collector binary and add `vendordb` at runtime through config alone.

## Quick Start With Docker Compose

1. Copy the example environment file.

```bash
cp deploy/.env.example deploy/.env
```

2. Edit `deploy/.env` and set:

- `SCOPEDB_ENDPOINT`
- `SCOPEDB_API_KEY`
- `SCOPEDB_TABLE`

3. Start the gateway.

```bash
docker compose -f deploy/docker-compose.yaml up -d --build
```

4. Send OTLP data to:

- `localhost:4317` for gRPC
- `localhost:4318` for HTTP

The Compose definition lives at [deploy/docker-compose.yaml](/Users/leiysky/work/scopedb-workspace/scopedb-otel/deploy/docker-compose.yaml:1).

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
  -e SCOPEDB_TABLE=public.vendor_otel_raw \
  -v scopedb-otel-queue:/var/lib/vendor-otelcol/queue \
  scopedb-otel-gateway:0.1.0
```

The image entrypoint and default config are defined in [otelcol/Dockerfile](/Users/leiysky/work/scopedb-workspace/scopedb-otel/otelcol/Dockerfile:1).

## Environment Variables

The deployment-facing config is environment-driven:

- `SCOPEDB_ENDPOINT`: ScopeDB base URL
- `SCOPEDB_API_KEY`: ScopeDB API key
- `SCOPEDB_TABLE`: target ScopeDB table, for example `public.vendor_otel_raw`

Auth is sent as `Authorization: Bearer <token>`, which matches the current ScopeDB client behavior.
Tenant selection is handled by the ScopeDB SaaS auth gateway, so there is no separate tenant config.
The deployment config creates the table automatically on startup.

## Table Initialization

The deployment config auto-creates the target table on startup with:

```yaml
create_table_if_not_exists: true
```

That startup DDL path uses the official ScopeDB Go SDK `github.com/scopedb/scopedb-sdk/go v0.4.0`.

If you prefer to manage schema yourself or your API key does not have DDL permission, disable that flag in a custom Collector config and create the table manually with:

```sql
CREATE TABLE IF NOT EXISTS public.vendor_otel_raw (
  ingest_ts timestamp,
  signal string,
  schema_version string,
  dataset string,
  trace_id string,
  span_id string,
  parent_span_id string,
  service_name string,
  metric_name string,
  severity_text string,
  record object
)
```

## Local Development

Build and validate the custom Collector locally:

```bash
make build-ocb
make build
make validate
make validate-deploy
```

The workspace pins Go `1.25` via `toolchain go1.25.3`.
If your default Go is older, `GOTOOLCHAIN=go1.25.3` is already wired into the Makefile.

## Run With The Mock Backend

Start the mock backend:

```bash
export VENDOR_API_KEY=demo-key
make mockdb
```

Then start the local demo Collector:

```bash
export VENDOR_DB_ENDPOINT=http://localhost:8080
export VENDOR_API_KEY=demo-key
./otelcol/_build/vendor-otelcol --config otelcol/config/demo.yaml
```

The mock backend accepts `Authorization: Bearer demo-key`.
If `:8080` is busy, run it with `MOCKDB_LISTEN_ADDR=:18080 make mockdb`.

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

When using the mock backend, inspect received payloads with:

```bash
curl http://localhost:8080/debug/payloads
```

## Repository Layout

- `exporter/vendordbexporter`: standalone exporter Go module
- `otelcol`: OCB distribution, configs, and container build
- `deploy`: deployment examples
- `examples/mockdb`: mock ingest backend for local testing

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
- `table`: ScopeDB table inserted into by the generated ingest statement
- `create_table_if_not_exists`: automatically runs `CREATE TABLE IF NOT EXISTS` on startup
- `schema_version`: payload schema version
- `compression`: `gzip` or `none`
- `timeout`: per-attempt exporter timeout
- `retry_on_failure`: exporterhelper retry policy
- `sending_queue`: exporterhelper queue policy, including optional `storage: file_storage`
