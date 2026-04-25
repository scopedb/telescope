# Table Management

Telescope writes OpenTelemetry logs, traces, and metrics into three ScopeDB tables. The recommended path is to let Telescope create and maintain the initial table layout, then only customize table routing when you need environment isolation, migration control, or a non-default database/schema.

This document is the operational guide for table creation and routing. If you just want a working local daemon, the embedded defaults are enough.

## Default Model

The default table routes are:

```yaml
tables:
  logs: scopedb.otel.logs
  traces: scopedb.otel.traces
  metrics: scopedb.otel.metrics
```

Each route may be written as `table`, `schema.table`, or `database.schema.table`. A three-part route such as `telemetry_prod.otel.logs` means database `telemetry_prod`, schema `otel`, table `logs`.

Telescope keeps each signal in a separate table because the promoted columns differ by signal:

| Signal | Default table | Purpose |
| --- | --- | --- |
| Logs | `scopedb.otel.logs` | Log events, exceptions, messages, severity, trace/span correlation. |
| Traces | `scopedb.otel.traces` | Span executions, timing, duration, status, parent/child correlation. |
| Metrics | `scopedb.otel.metrics` | Metric points, metric identity, numeric values, distributions. |

All tables include shared columns such as `ingest_ts`, `schema_version`, `env`, `row_id`, `service`, `version`, `instance_id`, `k8s_pod`, `k8s_namespace`, `k8s_cluster`, `container_name`, `host_ip`, `host`, and `record`. The `record` column stores the full mapped OpenTelemetry payload as evidence; promoted columns are the intended fast query surface.

The important split is:

- `env` is a logical label stored in every row.
- `tables.*` is physical storage routing.

Prefer changing `env` first. Change table routes only when storage topology, retention, indexing, access policy, or migration control needs to differ.

## Default Physical Layout

Telescope creates ScopeDB-native physical layouts for the default tables:

| Signal | Partition key | Cluster key |
| --- | --- | --- |
| Logs | `floor(record_timestamp, 24, 'hour')` | `env`, `service`, `severity_number`, `record_timestamp` |
| Traces | `floor(start_timestamp, 24, 'hour')` | `env`, `service`, `status_code`, `start_timestamp` |
| Metrics | `floor(record_timestamp, 24, 'hour')` | `env`, `service`, `metric_name`, `record_timestamp` |

The `floor(..., 24, 'hour')` expression is the default UTC day bucket. Partitions stay time-bounded only; service remains the primary clustering dimension, followed by stable low-cardinality status fields. Logs use OTel `severity_number` for physical clustering while preserving string `status` for agent-facing queries.

Telescope also creates default indexes:

| Signal | Indexes |
| --- | --- |
| Logs | range on `record_timestamp`, `severity_number`; point on `trace_id`, `span_id`, `service`, `version`, `k8s_namespace`, `k8s_cluster`, `source`, `status`, `severity_number`, `exception_type`; pattern on `service`, `version`, `instance_id`, `k8s_pod`, `k8s_namespace`, `k8s_cluster`, `container_name`, `host_ip`, `host`, `source`, `exception_type`; search and pattern on `message`, `exception_message` |
| Traces | range on `start_timestamp`, `duration_ns`; point on `trace_id`, `span_id`, `parent_span_id`, `service`, `version`, `k8s_namespace`, `k8s_cluster`, `status_code`, `http_status_code`, `url_path`, `http_route`, `peer_service`, `error_type`; pattern on `service`, `version`, `instance_id`, `k8s_pod`, `k8s_namespace`, `k8s_cluster`, `container_name`, `host_ip`, `host`, `span_name`, `url_path`, `http_route`, `peer_service`, `rpc_method`, `error_type` |
| Metrics | range on `record_timestamp`; point on `metric_name`, `service`, `version`, `k8s_namespace`, `k8s_cluster`; pattern on `metric_name`, `service`, `version`, `instance_id`, `k8s_pod`, `k8s_namespace`, `k8s_cluster`, `container_name`, `host_ip`, `host` |

## How Tables Are Created

Table creation is controlled by the ScopeDB exporter setting:

```yaml
exporters:
  scopedb:
    create_tables_if_not_exist: true
```

When enabled, the exporter does this during startup:

1. Parse `tables.logs`, `tables.traces`, and `tables.metrics`.
2. Create the database if the route uses `database.schema.table`.
3. Create the schema if the route uses `schema.table` or `database.schema.table`.
4. Create each signal table with Telescope's built-in schema.
5. Cache successful initialization per endpoint, signal, and table route for the process lifetime.

Creation uses `CREATE ... IF NOT EXISTS`, so repeated daemon starts are expected and safe. If startup cannot create or verify the table, the exporter fails fast instead of silently dropping telemetry.

## Recommended Pattern

Use the embedded daemon defaults for local bootstrap:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud \
TELESCOPE_SCOPEDB_API_KEY=sk_... \
telescope daemon
```

This uses the embedded Collector config and creates the default tables automatically.

Docker uses the same embedded Collector config. Docker Compose sets `TELESCOPE_QUEUE_DIR=/var/lib/telescope/queue`, so the persistent queue is stored in the `scopedb-telescope-queue` volume.

Keep one logical telemetry environment per `env` value unless you have a strong reason to split physical tables. `env` is stored as a column and is cheaper to change than table topology.

## Choosing Env vs Tables

Prefer changing `env` when you want to distinguish:

- local, staging, and production telemetry in the same physical tables
- temporary test traffic from normal traffic
- multiple apps that can share retention and access policy

Prefer changing `tables.*` when you need:

- different retention, indexing, or access policy per environment
- migration testing for a new table schema
- hard physical isolation between tenants or deployments
- a non-default ScopeDB database/schema layout

Avoid routing multiple signals into the same table. The exporter rejects duplicate `tables.logs`, `tables.traces`, and `tables.metrics` routes because the signal schemas are intentionally different.

## Configuration Reference

Minimal ScopeDB exporter configuration:

```yaml
exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    path: /v1/ingest
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    env: default
    create_tables_if_not_exist: true
    schema_version: v1
```

Full table routing example:

```yaml
exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    path: /v1/ingest
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    env: production
    tables:
      logs: telemetry_prod.otel.logs
      traces: telemetry_prod.otel.traces
      metrics: telemetry_prod.otel.metrics
    create_tables_if_not_exist: true
    schema_version: v1
    compression: zstd
    timeout: 10s
    retry_on_failure:
      enabled: true
      initial_interval: 1s
      max_interval: 30s
      max_elapsed_time: 0s
    sending_queue:
      enabled: true
      queue_size: 10000
      num_consumers: 4
      storage: file_storage
```

In this example, `env: production` is still a row value. The three `tables.*` routes choose physical tables in database `telemetry_prod` and schema `otel`.

Important fields:

| Field | Default | Notes |
| --- | --- | --- |
| `endpoint` | none | Required ScopeDB physical region endpoint. |
| `path` | `/v1/ingest` | ScopeDB ingest API path. |
| `api_key` | none | Required; sent as `Authorization: Bearer <api_key>`. |
| `env` | `default` | Stored in every row; preferred first-level environment separator. |
| `tables.logs` | `scopedb.otel.logs` | Log table route. |
| `tables.traces` | `scopedb.otel.traces` | Trace/span table route. |
| `tables.metrics` | `scopedb.otel.metrics` | Metric table route. |
| `create_tables_if_not_exist` | `false` in exporter defaults, `true` in Telescope daemon config | Enables startup database/schema/table creation. |
| `schema_version` | `v1` | Stored in every row for future migrations. |
| `compression` | `zstd` | Use `none`, `gzip`, or `zstd`. |
| `timeout` | `10s` | Also bounds startup table creation unless unset. |

## Embedded Defaults

The embedded `telescope daemon` config is used by both the local binary and the Docker image:

| Setting | Embedded daemon default |
| --- | --- |
| HTTP API | `:8080` |
| OTLP gRPC | `0.0.0.0:4317` |
| OTLP HTTP | `0.0.0.0:4318` |
| health | `0.0.0.0:13133` |
| queue dir | `$HOME/.telescope/queue` |
| batch timeout | `30s` |
| batch send size | `2000` |
| batch max size | `2000` |
| queue size | `5000` |
| queue consumers | `1` |
| retry initial interval | `5s` |
| retry max interval | `60s` |
| retry max elapsed | `10m` |

Override the queue directory with `TELESCOPE_QUEUE_DIR` when running in a container or another environment that needs a specific writable volume.

Use `TELESCOPE_COLLECTOR_CONFIG` or `telescope daemon --collector-config` only when you need to replace the embedded Collector config with a custom config URI or file path.

## Validating Configuration

Validate the embedded config:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://scopedb.invalid \
TELESCOPE_SCOPEDB_API_KEY=dummy \
make validate
```

The validation path checks Collector config shape and exporter config validation. It does not contact ScopeDB unless you actually start a pipeline.

## Manual Table Control

If your production environment manages DDL separately, set:

```yaml
exporters:
  scopedb:
    create_tables_if_not_exist: false
```

In that mode, create the tables ahead of time using the same schema as `packages/scopedbexporter/table_schema.go`. This is useful when database/schema creation requires elevated privileges or when schema changes must go through change management.

For most early Telescope deployments, keep `create_tables_if_not_exist: true`. It reduces bootstrap friction and keeps the table layout aligned with the exporter version.

## Schema Evolution

Telescope's initial table schema is intentionally append-friendly:

- stable promoted columns are first-class table columns
- `record` keeps the full mapped OpenTelemetry payload
- `schema_version` allows future readers to distinguish layouts
- `env` allows logical separation without multiplying table topology

When a raw OpenTelemetry attribute becomes important for repeated queries, prefer promoting it in the exporter/schema and semantic layer rather than relying on arbitrary `record` filters. This keeps agent queries predictable and lets ScopeDB index/materialize the field intentionally.

For a breaking schema migration, prefer creating new tables or a new env first, running both paths briefly, and then switching readers once the new tables have enough coverage.

## Troubleshooting

`endpoint is required` or `api_key is required`:

Set `TELESCOPE_SCOPEDB_ENDPOINT` and `TELESCOPE_SCOPEDB_API_KEY`.

`table route must be table, schema.table, or database.schema.table`:

Use only identifier parts made of letters, numbers, and underscores, starting with a letter or underscore.

`tables.logs and tables.traces must point to different tables`:

Give each signal a distinct route.

Startup fails while ensuring tables:

Confirm the API key has permission to create the target database/schema/table, or pre-create tables and set `create_tables_if_not_exist: false`.

Telemetry buffers but does not appear in ScopeDB:

Check the persistent queue settings, ScopeDB endpoint, API key, and exporter retry logs. In Docker, the queue is stored in the `scopedb-telescope-queue` volume mounted at `/var/lib/telescope/queue`.
