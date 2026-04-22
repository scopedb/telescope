# vendordb Exporter

`vendordbexporter` is an independent OpenTelemetry Collector exporter module named `vendordb`.

It accepts logs, traces, and metrics in Collector pdata form, maps them into JSON records, and writes them through ScopeDB's public `/v1/ingest` API with a generated `INSERT` statement.

## Config

```yaml
exporters:
  vendordb:
    endpoint: http://localhost:8080
    path: /v1/ingest
    api_key: ${env:VENDOR_API_KEY}
    dataset: default
    table: public.vendor_otel_raw
    create_table_if_not_exists: false
    schema_version: v1
    compression: gzip
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
- `table` controls the ScopeDB table name used by the generated ingest statement
- `create_table_if_not_exists` runs `CREATE TABLE IF NOT EXISTS` during exporter startup
- startup table creation uses the official ScopeDB Go SDK `v0.4.0`
- the deployment config enables table creation automatically, while mock/demo configs leave it off

## Ingest Request Shape

The exporter sends a ScopeDB ingest request like:

```json
{
  "type": "committed",
  "data": {
    "format": "json",
    "rows": "{\"signal\":\"logs\",...}\n{\"signal\":\"logs\",...}"
  },
  "statement": "SELECT ... INSERT INTO public.vendor_otel_raw (...)"
}
```

Each JSON row includes top-level ingest columns plus the full original mapped record:

- `ingest_ts`
- `signal`
- `schema_version`
- `dataset`
- `trace_id`
- `span_id`
- `parent_span_id`
- `service_name`
- `metric_name`
- `severity_text`
- `record`

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

## Suggested Table

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

## Error Semantics

- `400`, `401`, `403`, `404`, `422` are treated as permanent errors
- `408`, `409`, `425`, `429`, and `5xx` are retryable
- network and timeout failures are retryable
- context cancellation is returned as-is
