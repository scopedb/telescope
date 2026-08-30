# ScopeDB Exporter for OpenTelemetry Collector

`scopedbexporter` is the ScopeDB exporter used by Telescope and is available to custom OpenTelemetry Collector distributions through `scopedbexporter.NewFactory()`. The component is named `scopedb`. It converts Collector `pdata` into user-selected ScopeDB columns and writes NDJSON with the ScopeDB Go SDK `Table.AppendNDJSON` API.

The exporter does not impose a fixed telemetry schema. Each mapping entry projects one destination column from OpenTelemetry input, and only mapped columns are serialized and stored.

## Configuration

```yaml
exporters:
  scopedb:
    endpoint: ${env:TELESCOPE_SCOPEDB_ENDPOINT}
    api_key: ${env:TELESCOPE_SCOPEDB_API_KEY}
    tables:
      logs: observability.events
      traces: observability.spans
      metrics: observability.metric_points
    mappings:
      logs:
        ts: log.timestamp
        service_name:
          sources:
            - resource.attributes["service.name"]
            - resource.attributes["service"]
          default: unknown
          cast: string
        level: log.severity_text
        message: log.message
        labels: log.attributes
      traces:
        ts: span.start_time
        trace_id: span.trace_id
        span_id: span.span_id
        service_name: resource.attributes["service.name"]
        operation: span.name
        duration_ns: span.duration_ns
        labels: span.attributes
      metrics:
        ts: datapoint.timestamp
        service_name: resource.attributes["service.name"]
        metric: metric.name
        int_value: datapoint.int_value
        double_value: datapoint.double_value
        distribution: datapoint.distribution
        labels: datapoint.attributes
    compression: zstd
    timeout: 10s
    retry_on_failure:
      enabled: true
      initial_interval: 5s
      max_interval: 60s
      max_elapsed_time: 0s
    sending_queue:
      enabled: true
      storage: file_storage
      sizer: bytes
      queue_size: 536870912
      num_consumers: 1
```

`mappings.<signal>` is keyed by destination column. Destination columns must be unquoted ScopeDB identifiers. The value may be a source-selector shorthand or an expanded rule:

```yaml
mapping:
  message: log.message
  service:
    sources:
      - resource.attributes["service.name"]
      - resource.attributes["service"]
    default: unknown
    cast: string
  origin:
    value: otel
```

An expanded rule accepts exactly one of `source`, `sources`, or `value`. `sources` is an ordered fallback list and selects the first present value. `default` applies only when every source is absent or null. `value` emits a constant and cannot be combined with a source or default. Neither `default` nor `value` may be null. Selected empty strings, `false`, and numeric zeroes are present; they do not trigger fallback. Without a default, an absent result is omitted from that NDJSON row.

`cast` may be `string`, `int`, `uint`, `float`, `boolean`, `timestamp`, `object`, `array`, or explicitly `any`. Integer casts reject fractional or out-of-range values; casting a large integer to float can lose precision. A timestamp accepts RFC 3339 text or Unix nanoseconds and emits UTC RFC 3339. Structured casts validate the runtime shape without serializing it, and `any` is never selected implicitly. Known invalid constants and defaults fail configuration validation. A bad runtime value rejects only that record as permanent `mapping_cast_failed`; valid records in the same batch continue to ScopeDB. Use `telescope preview` with representative input before rollout.

Tables and mappings are always explicit: a signal is enabled only when both entries are configured.

The target tables are user-managed and must exist before the exporter starts. Startup calls `Describe` for each route and fails with the exact missing columns or statically known output-type mismatches. Runtime-typed values such as uncast individual attributes are not assigned a guessed type. The Telescope CLI can project representative OTLP JSON or protobuf with `preview --sample signal=path` to expose coverage, observed output types, and selected source/default counts without appending rows. Add `--strict` when unobserved, partial, or default-only sample coverage should fail. Signal routes may point to the same table when their mappings target a compatible schema.

## Mapping Sources

Sources shared by all signals:

- `resource.attributes`, `resource.attributes["<key>"]`
- `resource.schema_url`, `resource.dropped_attributes_count`
- `scope.name`, `scope.version`, `scope.attributes`, `scope.attributes["<key>"]`
- `scope.schema_url`, `scope.dropped_attributes_count`

Object and array sources support chained bracket access. Use a JSON-quoted string for an object key and a non-negative integer for an array index, for example `log.body["request"]["id"]`, `log.body["tags"][0]`, or `span.events[0]["name"]`. A missing key, null value, container mismatch, or out-of-range index is treated as absent, so the next `sources` entry or `default` can apply.

Log sources:

- `log.timestamp`, `log.observed_timestamp` as RFC 3339 timestamps
- `log.timestamp_unix_nano`, `log.observed_timestamp_unix_nano`
- `log.trace_id`, `log.span_id`, `log.event_name`
- `log.severity_text`, `log.severity_number`, `log.flags`
- `log.body`, `log.message`, `log.attributes`, `log.attributes["<key>"]`
- `log.dropped_attributes_count`

Trace sources:

- `span.trace_id`, `span.span_id`, `span.parent_span_id`, `span.trace_state`, `span.flags`
- `span.name`, `span.kind`
- `span.start_time`, `span.end_time` as RFC 3339 timestamps
- `span.start_time_unix_nano`, `span.end_time_unix_nano`, `span.duration_ns`
- `span.status.code`, `span.status.message`
- `span.attributes`, `span.attributes["<key>"]`, `span.dropped_attributes_count`
- `span.events`, `span.links`, `span.dropped_events_count`, `span.dropped_links_count`

Metric sources:

- `metric.name`, `metric.description`, `metric.unit`, `metric.type`
- `metric.metadata`, `metric.metadata["<key>"]`
- `metric.temporality`, `metric.is_monotonic`
- `datapoint.timestamp`, `datapoint.start_time` as RFC 3339 timestamps
- `datapoint.timestamp_unix_nano`, `datapoint.start_time_unix_nano`, `datapoint.flags`
- `datapoint.attributes`, `datapoint.attributes["<key>"]`
- `datapoint.value` with its native integer or double type, and `datapoint.value_type`
- `datapoint.int_value` and `datapoint.double_value`, each present only for its matching point type
- `datapoint.number_value`, an explicit lossy projection that coerces integer points to double
- `datapoint.distribution`, `datapoint.exemplars`

Whole attribute maps, bodies, events, links, distributions, and exemplars retain their JSON structure. Map individual attributes when only a few dimensions deserve physical columns. Telescope's projection is limited to destination shaping: selection, nested access, fallback/default, constants, and casts. Use upstream OpenTelemetry processors for mutating telemetry, filtering, sampling, content-based routing, arithmetic, regex processing, or aggregation.

## Append and Retry Semantics

The writer uses synchronous `Table.AppendNDJSON`, one ScopeDB request per chunk. Each request is capped at 8 MiB of uncompressed NDJSON and 200,000 rows.

A chunk succeeds only when ScopeDB reports `committed` and the inserted row count matches. Projection, JSON encoding, and row-size failures are isolated to the offending records. When ScopeDB returns a non-retryable `rejected` result with a complete, non-truncated row-error list, Telescope removes those rows and makes one append attempt for the remainder. It never bisects or repeatedly probes a rejected chunk. Rejections without a complete row list remain permanent for the whole uncommitted chunk. Retryable rejection, throttling, transport errors, and `unknown` outcomes are returned as retryable. When a later chunk fails, Collector retries only uncommitted records, not an already confirmed prefix or an isolated bad record.

The OpenTelemetry Collector `exporterhelper` sending queue is the only persistent queue and retry owner. Retryable requests have no elapsed-time expiry by default; the byte-sized queue bounds retained data, and a full queue reports enqueue failures instead of growing without limit. Unisolated permanent errors stop immediately. `AppendNDJSON` is not wrapped in a second SDK stream, queue, or reconciliation loop. Delivery is at least once: retrying an `unknown` outcome can create duplicates.

`compression` accepts `zstd` (default) or `gzip` and is passed into `scopedb.Config`. ScopeDB Go SDK `v0.6.3` applies that setting to direct `AppendNDJSON` requests.
