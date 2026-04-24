# ScopeDB Exporter

`scopedbexporter` is an independent OpenTelemetry Collector exporter module named `scopedb`.

It accepts logs, traces, and metrics in Collector pdata form, maps them into JSON records, and writes them through ScopeDB's public `/v1/ingest` API with a generated `INSERT` statement.

## Config

```yaml
exporters:
  scopedb:
    endpoint: https://<region>.scopedb.cloud
    path: /v1/ingest
    api_key: ${env:SCOPEDB_API_KEY}
    dataset: default
    tables:
      logs: scopedb.otel.logs
      traces: scopedb.otel.traces
      metrics: scopedb.otel.metrics
    create_tables_if_not_exist: false
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

Notes:

- `api_key` uses `configopaque.String`, so it is redacted when config values are logged
- the exporter always sends `Authorization: Bearer <api_key>`
- built-in defaults route signals to `scopedb.otel.logs`, `scopedb.otel.traces`, and `scopedb.otel.metrics`
- table routes accept `table`, `schema.table`, or `database.schema.table`
- `tables.logs`, `tables.traces`, and `tables.metrics` are required and must point to distinct tables
- `create_tables_if_not_exist` ensures the configured database, schema, and table exist for every configured route during exporter startup
- `zstd` is the default POST compression; use `gzip` only when talking to older ScopeDB deployments
- startup table creation uses the official ScopeDB Go SDK `v0.5.0`
- the deployment config enables table creation automatically, while the local demo configs leave it off

## Ingest Request Shape

The exporter sends a ScopeDB ingest request like:

```json
{
  "type": "committed",
  "data": {
    "format": "json",
    "rows": "{\"signal\":\"logs\",...}\n{\"signal\":\"logs\",...}"
  },
  "statement": "SELECT ... INSERT INTO scopedb.otel.logs (...)"
}
```

Each JSON row includes shared ingest columns plus the full original mapped record:

- `ingest_ts`
- `signal`
- `schema_version`
- `dataset`
- `row_id`
- `record`

The exporter also promotes signal-specific fields into top-level row columns so each table can use its own schema:

- shared resource columns: `service_name`, `instance_id`, `pod_name`, `host_ip`, `host_name`
- logs: `record_timestamp`, `observed_timestamp`, `trace_id`, `span_id`, `severity_text`, `message`
- traces: `start_timestamp`, `end_timestamp`, `duration_ns`, `trace_id`, `span_id`, `parent_span_id`, `span_name`, `span_kind`, `status_code`
- metrics: `record_timestamp`, `start_timestamp`, `metric_name`, `metric_type`, `temporality`, `unit`, `number_value`, `distribution`

The `record` object keeps the signal-specific body. Log records include fields such as:

- `timestamp_unix_nano`
- `observed_timestamp_unix_nano`
- `trace_id`
- `span_id`
- `severity_text`
- `severity_number`
- `body`
- `resource`
- `scope`
- `attributes`

Each span record includes fields such as:

- `trace_id`
- `span_id`
- `parent_span_id`
- `name`
- `kind`
- `start_time_unix_nano`
- `end_time_unix_nano`
- `status_code`
- `status_message`
- `events`
- `links`
- `resource`
- `scope`
- `attributes`

Each metric record includes fields such as:

- `metric_name`
- `description`
- `unit`
- `type`
- `temporality`
- `is_monotonic`
- `timestamp_unix_nano`
- `start_timestamp_unix_nano`
- `value`
- `histogram`
- `summary`
- `exemplars`
- `resource`
- `scope`
- `attributes`

## Suggested Table Schemas

```sql
CREATE TABLE IF NOT EXISTS scopedb.otel.logs (
  ingest_ts timestamp,
  record_timestamp timestamp,
  observed_timestamp timestamp,
  schema_version string,
  dataset string,
  row_id string,
  service_name string,
  instance_id string,
  pod_name string,
  host_ip string,
  host_name string,
  trace_id string,
  span_id string,
  severity_text string,
  message string,
  record object
)

CREATE TABLE IF NOT EXISTS scopedb.otel.traces (
  ingest_ts timestamp,
  start_timestamp timestamp,
  end_timestamp timestamp,
  duration_ns int,
  schema_version string,
  dataset string,
  row_id string,
  service_name string,
  instance_id string,
  pod_name string,
  host_ip string,
  host_name string,
  trace_id string,
  span_id string,
  parent_span_id string,
  span_name string,
  span_kind string,
  status_code string,
  record object
)

CREATE TABLE IF NOT EXISTS scopedb.otel.metrics (
  ingest_ts timestamp,
  record_timestamp timestamp,
  start_timestamp timestamp,
  schema_version string,
  dataset string,
  row_id string,
  service_name string,
  instance_id string,
  pod_name string,
  host_ip string,
  host_name string,
  metric_name string,
  metric_type string,
  temporality string,
  unit string,
  number_value float,
  distribution object,
  record object
)
```

## Error Semantics

- `400`, `401`, `403`, `404`, `422` are treated as permanent errors
- `408`, `409`, `425`, `429`, and `5xx` are retryable
- network and timeout failures are retryable
- context cancellation is returned as-is
