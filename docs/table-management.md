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
signals:
  logs:
    table: telemetry_prod.otel.events
    mapping:
      event_time: log.timestamp
      app: resource.attributes["service.name"]
      environment: resource.attributes["deployment.environment.name"]
      level: log.severity_text
      body: log.body
      trace_id: log.trace_id
  traces:
    table: telemetry_prod.otel.spans
    mapping:
      started_at: span.start_time
      app: resource.attributes["service.name"]
      trace_id: span.trace_id
      span_id: span.span_id
      operation: span.name
      duration_ns: span.duration_ns
      tags: span.attributes
  metrics:
    table: telemetry_prod.otel.metric_points
    mapping:
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

## Ingestion Configuration and Validation

Put only the routes and mappings in `telescope.yaml`; Collector receivers, batching, persistence, compression, and retry remain Telescope-owned:

```yaml
signals:
  traces:
    table: telemetry_prod.otel.spans
    mapping:
      started_at: span.start_time
      operation: span.name
```

Only configured signals get Collector pipelines. The example accepts traces without requiring placeholder log and metric tables. Add independent `logs` or `metrics` blocks when needed.

Validate the mapping against the live destination before accepting OTLP:

```bash
telescope validate \
  --scopedb-endpoint https://<region>.scopedb.cloud \
  --scopedb-api-key sk_... \
  ./telescope.yaml

telescope run \
  --scopedb-endpoint https://<region>.scopedb.cloud \
  --scopedb-api-key sk_... \
  ./telescope.yaml
```

The check command prints `signal -> table -> destination column -> OTel source`, then describes every configured destination. It reports missing columns and statically known type mismatches, including the destination column, selector, produced type, and actual ScopeDB type. Attribute-key selectors and `log.body` are runtime-typed, so Telescope checks that their columns exist but does not guess a type. Telescope never modifies the table.

`telescope run` performs the same validation when ScopeDB is reachable. A deterministic table or mapping mismatch prevents startup. A temporary network, timeout, rate-limit, or server error leaves the destination unverified but does not prevent the OTLP listener and persistent queue from starting.

Full Collector configuration can still be checked without contacting ScopeDB:

```bash
make validate
```

Use `telescope verify` after startup to test every enabled signal and wait for exact ScopeDB append acknowledgements. The probes do not query mapped columns.

## Mapping Changes

Treat a mapping change like an application-to-database contract change:

1. Apply compatible DDL first.
2. Update the mapping.
3. Run `telescope validate`, then restart or roll out Telescope.

For an incompatible layout, create a new table and switch the route. Telescope does not dual-write, backfill, or reconcile old and new tables.

Use ScopeDB workload measurements to decide promoted columns and indexes. Mapping every attribute into a top-level column or keeping a second full raw record by default adds cost without knowing the customer's access patterns.
