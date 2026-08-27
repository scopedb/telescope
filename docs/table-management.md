# ScopeDB Mapping and Table Management

Telescope maps OpenTelemetry values into user-owned ScopeDB tables. It does not own a universal telemetry row or execute table DDL. It can render an additive ScopeQL plan from mappings whose output types are explicit.

The ownership boundary is:

| Concern | Owner |
| --- | --- |
| Signal-to-table routing and destination projection (selection, fallback/default, constants, casts) | Telescope exporter config |
| Logical required column types and live catalog diff | `telescope plan` |
| DDL review/execution, nullability/defaults, partitioning, clustering, retention, and indexes | User-managed ScopeDB DDL and ScopeQL |
| Telemetry mutation, filtering, sampling, content-based routing, arithmetic, regex, and aggregation | Upstream OpenTelemetry processors |
| Persistent queue and retry schedule | OpenTelemetry Collector `exporterhelper` |
| One append request and its `committed`/`rejected`/`unknown` result | ScopeDB Go SDK `AppendNDJSON` |

This separation is intentional. A statically typed selector or explicit `cast` determines the logical append type. A representative sample only tests that decision; it cannot determine future shape or infer a physical layout.

Telescope's boundary ends at the append result. It does not query or interpret the stored columns.

## Mapping Model

Each mapping is keyed by destination column. A direct selector is the common shorthand:

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

Missing and null source values are omitted. Selected empty strings, `false`, and numeric zeroes are preserved. Without a mapping default, a source that can be absent therefore needs a compatible nullable destination column or table default.

Use an expanded rule when a destination column needs fallback, a Telescope-side default, a constant, or a fixed output type:

```yaml
signals:
  logs:
    table: telemetry_prod.otel.events
    mapping:
      event_time: log.timestamp
      service:
        sources:
          - resource.attributes["service.name"]
          - resource.attributes["service"]
        default: unknown
        cast: string
      request_id:
        source: log.body["request"]["id"]
        cast: string
      origin:
        value: otel
```

The rule contract is deliberately small:

- Use exactly one of `source`, ordered `sources`, or constant `value`.
- `default` is used only when every source is absent or null. Empty strings, `false`, and numeric zeroes are present values; `default` and `value` themselves cannot be null.
- `cast` fixes the output type to `string`, `int`, `uint`, `float`, `boolean`, `timestamp`, `object`, `array`, or explicitly `any`. Structured casts validate the runtime shape instead of serializing it; `any` is never selected implicitly.
- Object keys and array indexes can be chained with `["key"]` and `[index]`. Missing or mismatched path segments are absent and participate in fallback.

This projection runs only while building the ScopeDB row. It does not mutate the OpenTelemetry record seen by other pipelines, and it is not an event-processing language. Filtering, sampling, arbitrary expressions, regex, arithmetic, aggregation, and content-based routing remain upstream concerns.

The full source selector reference is in the [ScopeDB exporter README](../packages/scopedbexporter/README.md#mapping-sources).

## Plan and Apply Tables

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

Inspect a representative sample before planning when runtime values or casts are involved:

```bash
telescope preview \
  --offline \
  --strict \
  --sample traces=deploy/samples/traces.otlp.json \
  ./telescope.yaml
```

Then compare the logical write contract with the live catalog:

```bash
export SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
export SCOPEDB_API_KEY=sk_...

telescope plan \
  --sample traces=deploy/samples/traces.otlp.json \
  --out tables.scopeql \
  ./telescope.yaml
```

ScopeQL owns its connection configuration. If the intended connection is not already present and selected, initialize it before applying Telescope's generated DDL:

```bash
scopeql config set-connection production
scopeql config use-connection production
scopeql config get-connections
```

The setup command prompts for the endpoint and authentication fields supported by the installed ScopeQL version. Telescope's `--env-file`, `TELESCOPE_SCOPEDB_*`, and `SCOPEDB_*` inputs configure Telescope only; they do not create or migrate ScopeQL connections.

After reviewing the generated script and confirming the intended ScopeQL connection, apply it explicitly:

```bash
scopeql run -f tables.scopeql
```

The human plan groups every configured signal by destination table and classifies it as `create`, `alter`, `no-op`, or `blocked`. It shows the mapping behind a conflict, sample coverage and observed types, and an evidence-based `cast` edit when one sample type consistently resolves a dynamic mapping. `--out` atomically writes the ScopeQL from the same catalog read while retaining that feedback on stdout. The output file is left untouched when any table is blocked, so setup cannot silently apply a partial contract. `--format json` returns the same versioned generated plan for other tooling, and `--format scopeql` writes DDL to stdout for pipelines.

Generated ScopeQL contains only missing `CREATE DATABASE`, `CREATE SCHEMA`, `CREATE TABLE`, and `ALTER TABLE ... ADD COLUMN` statements, ordered by dependency and deduplicated across tables. It is never executed by Telescope. For Telescope, explicit connection flags override `TELESCOPE_SCOPEDB_*`, which override its `SCOPEDB_ENDPOINT` and `SCOPEDB_API_KEY` fallbacks. ScopeQL uses its independently selected connection.

A fixed selector type or explicit `cast` is authoritative. Observed sample types and coverage are included as evidence only. An uncast attribute, nested path, `log.body`, or `datapoint.value` therefore blocks table creation even when one sample happens to show a stable type. Add a cast such as `string`, `object`, `array`, or explicitly `any` to record the intended table type. Incompatible existing columns and conflicting requirements from signals sharing one table also block the plan.

Review the generated DDL before applying it. Telescope deliberately does not infer column defaults, partitioning, clustering, retention, distinct keys, or indexes from telemetry samples.

## Validate and Run

Validate the applied contract before accepting OTLP:

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

The validate command prints `signal -> table -> destination column -> mapping rule`, then describes every configured destination. It reports all invalid selectors and rules in one run, suggests close selector names, and reports missing columns or statically known output-type mismatches. An explicit cast supplies the output type for catalog validation. A cast from runtime data is still marked runtime-dependent when sample values determine whether conversion succeeds.

`telescope run` performs the same validation when ScopeDB is reachable. A deterministic table or mapping mismatch prevents startup. A temporary network, timeout, rate-limit, or server error leaves the destination unverified but does not prevent the OTLP listener and persistent queue from starting.

Full Collector configuration can still be checked without contacting ScopeDB:

```bash
make validate
```

For an existing deployment, obtain the sample from the actual exporter input instead of assembling one manually:

```bash
telescope capture --limit 100 traces |
  telescope preview --sample traces=- ./telescope.yaml
```

The capture is active only for this request and stops at the record limit or timeout. It observes exporter input after Collector batch processing and before the sending queue, so low-volume telemetry can take up to the batch timeout to appear while append retries cannot duplicate the sample. The default 45-second capture timeout exceeds the bundled 30-second batch timeout.

Use `telescope verify` after startup to send a minimal synthetic record for every enabled signal and wait for exact ScopeDB append acknowledgements. This confirms transport and commit acknowledgement. It neither exercises application-specific mapping values nor queries mapped columns; use `telescope preview` for the mapping itself.

## Mapping Changes

Treat a mapping change like an application-to-database contract change:

1. Update the candidate mapping and preview representative input.
2. Run `telescope plan`, review the generated additive ScopeQL, and apply it explicitly.
3. Run `telescope validate`, then restart or roll out Telescope.

For an incompatible layout, create a new table and switch the route. Telescope does not dual-write, backfill, or reconcile old and new tables.

Use ScopeDB workload measurements to decide promoted columns and indexes. Mapping every attribute into a top-level column or keeping a second full raw record by default adds cost without knowing the customer's access patterns.
