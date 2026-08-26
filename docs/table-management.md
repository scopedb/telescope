# ScopeDB Mapping and Table Management

Telescope maps OpenTelemetry values into existing ScopeDB tables. It does not own a universal telemetry row or generate table DDL from the mapping.

The ownership boundary is:

| Concern | Owner |
| --- | --- |
| Signal-to-table routing and OTel source-to-column mapping | Telescope exporter config |
| Column types, nullability/defaults, partitioning, clustering, retention, and indexes | User-managed ScopeDB DDL |
| Renaming, normalization, derived values, filtering, and sampling | Upstream OpenTelemetry processors |
| Persistent queue and retry schedule | OpenTelemetry Collector `exporterhelper` |
| One append request and its `committed`/`rejected`/`unknown` result | ScopeDB Go SDK `AppendNDJSON` |

This separation is intentional. A mapping can identify the destination column name, but it cannot infer whether a customer wants a string or enum, a scalar or object, a nullable field or default, or a particular physical layout.

## Mapping Model

Each mapping is `destination column: OpenTelemetry source`:

```yaml
exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    tables:
      logs: telemetry_prod.otel.events
      traces: telemetry_prod.otel.spans
      metrics: telemetry_prod.otel.metric_points
    mappings:
      logs:
        event_time: log.timestamp
        app: resource.attributes["service.name"]
        environment: resource.attributes["deployment.environment.name"]
        level: log.severity_text
        body: log.body
        trace_id: log.trace_id
      traces:
        started_at: span.start_time
        app: resource.attributes["service.name"]
        trace_id: span.trace_id
        span_id: span.span_id
        operation: span.name
        duration_ns: span.duration_ns
        tags: span.attributes
      metrics:
        measured_at: datapoint.timestamp
        app: resource.attributes["service.name"]
        name: metric.name
        int_value: datapoint.int_value
        double_value: datapoint.double_value
        distribution: datapoint.distribution
        tags: datapoint.attributes
```

Only those destination columns appear in the NDJSON row. There is no implicit `record`, `env`, `schema_version`, or copy of every OpenTelemetry attribute. Map a whole attribute object only when the target table actually has an object column for it; otherwise select individual keys.

Missing and null source values are omitted. Selected empty strings and numeric zeroes are preserved. Any mapped source that can be absent therefore needs a compatible nullable column or table default.

The full source selector reference is in the [ScopeDB exporter README](../packages/scopedbexporter/README.md#mapping-sources).

## Starter Profile Tables

The explicitly selected `starter` profile uses three routes:

```yaml
tables:
  logs: scopedb.otel.logs
  traces: scopedb.otel.traces
  metrics: scopedb.otel.metrics
```

The corresponding starter mappings require these columns:

| Signal | Columns |
| --- | --- |
| Logs | `record_timestamp`, `observed_timestamp`, `trace_id`, `span_id`, `service`, `status`, `severity_number`, `message` |
| Traces | `start_timestamp`, `end_timestamp`, `trace_id`, `span_id`, `parent_span_id`, `service`, `span_name`, `span_kind`, `status_code`, `duration_ns` |
| Metrics | `record_timestamp`, `start_timestamp`, `service`, `metric_name`, `metric_type`, `temporality`, `unit`, `int_value`, `double_value`, `distribution` |

A minimal compatible layout is:

```sql
CREATE TABLE scopedb.otel.logs (
  record_timestamp timestamp,
  observed_timestamp timestamp,
  trace_id string,
  span_id string,
  service string,
  status string,
  severity_number int,
  message string
);

CREATE TABLE scopedb.otel.traces (
  start_timestamp timestamp,
  end_timestamp timestamp,
  trace_id string,
  span_id string,
  parent_span_id string,
  service string,
  span_name string,
  span_kind string,
  status_code string,
  duration_ns int
);

CREATE TABLE scopedb.otel.metrics (
  record_timestamp timestamp,
  start_timestamp timestamp,
  service string,
  metric_name string,
  metric_type string,
  temporality string,
  unit string,
  int_value int,
  double_value float,
  distribution object
);
```

Create the database and schema first if they do not already exist, and add the physical layout appropriate for the workload. The example deliberately does not prescribe partition, cluster, retention, or index settings.

The starter mapping is only a bootstrap convenience. Production configurations should name their own tables and mappings. Logs, traces, and metrics may share one physical table if all mapped columns are compatible; Telescope does not require distinct routes.

## Ingestion Configuration and Validation

For user-owned tables, put only the routes and mappings in an ingestion file; Collector receivers, batching, persistence, compression, and retry remain Telescope-owned:

```yaml
tables:
  logs: telemetry_prod.otel.events
  traces: telemetry_prod.otel.spans
  metrics: telemetry_prod.otel.metric_points
mappings:
  logs:
    event_time: log.timestamp
    message: log.message
  traces:
    started_at: span.start_time
    operation: span.name
  metrics:
    measured_at: datapoint.timestamp
    name: metric.name
    int_value: datapoint.int_value
    double_value: datapoint.double_value
```

Inspect the exact mapping and validate it against ScopeDB before accepting OTLP:

```bash
telescope ingestion check \
  --config ./ingestion.yaml \
  --scopedb-endpoint https://<region>.scopedb.cloud \
  --scopedb-api-key sk_...

telescope daemon \
  --ingestion-config ./ingestion.yaml \
  --scopedb-endpoint https://<region>.scopedb.cloud \
  --scopedb-api-key sk_...
```

Both the check command and daemon startup describe every destination. They report missing columns and statically known type mismatches, including the destination column, selector, produced type, and actual ScopeDB type. Attribute-key selectors and `log.body` are runtime-typed, so Telescope checks that their columns exist but does not guess a type. Telescope never modifies the table.

`create_tables_if_not_exist: true` is rejected. Remove it from older configs and provision the tables separately.

Full Collector configuration can still be checked without contacting ScopeDB:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://scopedb.invalid \
TELESCOPE_SCOPEDB_API_KEY=dummy \
make validate
```

Starting the pipeline performs the live table descriptions.

## Mapping Changes

Treat a mapping change like an application-to-database contract change:

1. Apply compatible DDL first.
2. Update the mapping.
3. Restart or roll out Telescope; startup validation confirms the columns.

For an incompatible layout, create a new table and switch the route. Telescope does not dual-write, backfill, or reconcile old and new tables.

Use ScopeDB workload measurements to decide promoted columns and indexes. Mapping every attribute into a top-level column or keeping a second full raw record by default adds cost without knowing the customer's access patterns.
