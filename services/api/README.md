# Telescope Runtime

This directory contains the `telescope` binary, an OpenTelemetry Collector distribution that receives OTLP telemetry and exports configured signals to ScopeDB.

## Layout

- `cmd/telescope`: CLI entrypoint
- `internal/collector`: embedded Collector factories and configuration generation
- `internal/httpapi`: health, readiness, and ingestion status handlers

## Runtime

Required environment variables:

- `TELESCOPE_SCOPEDB_ENDPOINT`
- `TELESCOPE_SCOPEDB_API_KEY`

Common optional environment variables:

- `TELESCOPE_HTTP_ADDR`: operational HTTP address, default `:8080`
- `TELESCOPE_OTLP_GRPC_ADDR`: OTLP/gRPC address, default `0.0.0.0:4317`
- `TELESCOPE_OTLP_HTTP_ADDR`: OTLP/HTTP address, default `0.0.0.0:4318`
- `TELESCOPE_HEALTH_ADDR`: Collector health extension address, default `0.0.0.0:13133`
- `TELESCOPE_QUEUE_DIR`: persistent queue directory, default `$HOME/.telescope/queue`
- `TELESCOPE_QUEUE_MAX_BYTES`: logical queue capacity, default `536870912`
- `TELESCOPE_OTEL_BATCH_TIMEOUT`: batch timeout, default `30s`
- `TELESCOPE_OTEL_BATCH_SIZE`: preferred batch size, default `2000`
- `TELESCOPE_OTEL_BATCH_MAX_SIZE`: maximum batch size, default `2000`
- `TELESCOPE_INTERNAL_METRICS_URL`: Collector metrics URL used by ingestion status, default `http://127.0.0.1:8888/metrics`
- `TELESCOPE_INGESTION_CONFIG`: tables and mappings YAML file
- `TELESCOPE_INGESTION_PROFILE`: built-in profile; currently `starter`
- `TELESCOPE_COLLECTOR_CONFIG`: full Collector config URI or file path

Run locally:

```bash
go run ./cmd/telescope daemon \
  --env-file ../../services/gateway/deploy/.env \
  --ingestion-config ../../services/gateway/deploy/ingestion.yaml
```

Operational endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /v1/ingestion/status`
