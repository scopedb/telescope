# Map telemetry to tables

A Telescope configuration chooses a destination table and a column mapping for each enabled signal. Start with the [quickstart](quickstart.md) for a working trace pipeline; use this guide to adapt it to your data.

## Mapping model

Each mapping key is a destination column. Its value selects an OpenTelemetry field:

```yaml
signals:
  traces:
    table: scopedb.otel.traces
    mapping:
      started_at: span.start_time
      trace_id: span.trace_id
      operation: span.name
      duration_ns: span.duration_ns
      tags: span.attributes
      service:
        sources:
          - resource.attributes["service.name"]
          - resource.attributes["service"]
        default: unknown
        cast: string
```

Only mapped columns are written. Add independent `logs` or `metrics` blocks to enable those pipelines; there are no implicit columns or placeholder tables. Use `telescope inspect <signal> --sample <file>` to discover selectors in your own data, or consult the [source reference](ingestion-compatibility.md).

| Rule | Meaning |
| --- | --- |
| `column: span.name` | Select one source. |
| `source: resource.attributes["service.name"]` | Expanded form of a single source. |
| `sources: [source_a, source_b]` | Select the first present, non-null source. |
| `default: unknown` | Use this value when every source is absent or null. |
| `value: production` | Write a constant instead of reading a source. |
| `cast: string` | Fix the output type and convert or validate the selected value. |

Use exactly one of `source`, `sources`, or `value`. Missing and null inputs are omitted unless a default applies; empty strings, `false`, and zero are present values. `default` and `value` cannot themselves be null. An optional source needs a nullable destination column or a table default.

Object keys and array indexes can be chained, for example `log.body["request"]["id"]` or `resource.attributes["host.ip"][0]`. A missing or mismatched segment is absent and participates in fallback.

Casts accept `string`, `int`, `uint`, `float`, `boolean`, `timestamp`, `object`, `array`, or explicitly `any`. Object and array casts validate shape rather than serializing it. `any` is never chosen implicitly. An invalid cast rejects that record, so preview application samples before rollout.

## Preview before writing

```bash
telescope inspect traces --sample traces.otlp.json
telescope preview --offline --strict \
  --sample traces=traces.otlp.json telescope.yaml
```

Preview reports output types, column coverage, selected sources/defaults, mapping errors, and projected rows without appending. `--strict` fails for unobserved, partial, or default-only columns. Repeat `--sample` for multiple signals; use `signal=-` for stdin. See [capture and preview](operations.md#capture-and-preview) to obtain a sample from real traffic.

A sample tests the mapping; it does not define future column types. Fixed selectors and explicit casts determine the table contract. Uncast attributes, nested paths, `log.body`, and `datapoint.value` need an explicit cast before a new table can be planned, even if one sample has a consistent type.

## Plan and apply tables

```bash
telescope plan --env-file deploy/.env \
  --sample traces=traces.otlp.json --out tables.scopeql telescope.yaml
```

The plan compares the mapping with the live catalog:

| Result | Next step |
| --- | --- |
| `create` | Review and create the missing table and namespaces. |
| `alter` | Review the additive columns. |
| `no-op` | Validate the configuration and start Telescope. |
| `blocked` | Resolve conflicting types, sample errors, or incompatible existing columns. |

The output includes sample evidence and suggests a cast when it can do so unambiguously. It never edits the mapping. `--out` writes atomically from the same catalog read and leaves the file untouched if any table is blocked. `--format json` exposes the versioned plan; `--format scopeql` writes DDL to stdout.

Review and apply the script in the intended ScopeDB workspace. It contains only missing `CREATE DATABASE`, `CREATE SCHEMA`, `CREATE TABLE`, and `ALTER TABLE ... ADD COLUMN` statements, ordered by dependency. Telescope does not execute the plan or infer retention, clustering, distinct keys, indexes, nullability, or column defaults.

<details>
<summary>Apply with the ScopeQL CLI</summary>

ScopeQL uses its own connection settings. Telescope's environment file does not configure it:

```bash
scopeql config set-connection telescope-target
scopeql config use-connection telescope-target
scopeql config get-connections
scopeql run -f tables.scopeql
```

</details>

Validate the applied table contract before starting or deploying:

```bash
telescope validate --env-file deploy/.env telescope.yaml
```

Validation checks column existence and known type compatibility, reporting all invalid selectors and rules. Runtime-dependent casts still need representative samples. `telescope run` checks configuration locally without a catalog request so a ScopeDB outage does not block receivers or queue recovery; the first confirmed append verifies the destination.

## Change an existing mapping

1. Preview the candidate mapping with representative traffic.
2. Plan, review, and apply its additive DDL.
3. Stop new traffic to the old instance; wait for its queue and accepted-without-final-outcome count to reach zero.
4. Validate and restart with the new mapping.

The queue stores OTLP before mapping. A non-empty queue must drain under its original configuration; otherwise older telemetry would be projected through the new rules. A binary-only upgrade can reuse the queue when the config digest is unchanged. To avoid an ingestion pause, use a separate deployment and queue volume while the old one drains. See [v0.2 migration](migration-v0.2.md) for the version boundary.

For an incompatible layout, create a new table and switch the route. Telescope does not dual-write, backfill, or reconcile schemas. Use upstream Collector processors for filtering, sampling, arithmetic, regex, aggregation, or content-based routing; this mapping changes only the destination row.
