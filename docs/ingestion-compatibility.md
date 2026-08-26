# Telescope Ingestion Compatibility

Status: current `v1` source-mapping contract

This document defines what Telescope accepts through OTLP and makes available to user mappings. The executable source contract is the versioned corpus under `packages/scopedbexporter/testdata/golden/v1`.

This is not a mandatory ScopeDB row schema. Only sources selected in `mappings.logs`, `mappings.traces`, or `mappings.metrics` are serialized into their configured destination columns. See [Mapping and Table Management](table-management.md).

## Protocol and Signal Support

| Input | Logs | Traces | Metrics |
| --- | --- | --- | --- |
| OTLP/gRPC | Supported | Supported | Supported |
| OTLP/HTTP protobuf | Supported | Supported | Supported |
| OTLP/HTTP JSON | Supported | Supported | Supported |

Telescope relies on the upstream OpenTelemetry OTLP receiver for wire decoding. The ScopeDB exporter receives OpenTelemetry `pdata`, so the mapping contract is the same for gRPC, HTTP protobuf, and HTTP JSON after decoding.

## Shared Context

The following shared context is available to every signal mapping:

| OpenTelemetry field | Mapping source |
| --- | --- |
| Resource attributes | `resource.attributes` or `resource.attributes["<key>"]` |
| Resource dropped attributes count | `resource.dropped_attributes_count` |
| Resource schema URL | `resource.schema_url` |
| Scope name and version | `scope.name`, `scope.version` |
| Scope attributes | `scope.attributes` or `scope.attributes["<key>"]` |
| Scope dropped attributes count | `scope.dropped_attributes_count` |
| Scope schema URL | `scope.schema_url` |

Selected attribute strings, integers, doubles, booleans, arrays, and nested key-value lists retain their JSON type and structure. Selected byte values are base64-encoded strings and do not yet retain an explicit byte type marker.

Before opening OTLP listeners, Telescope checks every selected destination column. Selectors with a fixed output type are checked against the ScopeDB catalog type; timestamps may target `timestamp` or `string`, and `any` accepts every fixed selector type. Individual attribute selectors, `log.body`, and other runtime-typed values are checked for column existence without guessing their type.

## Logs

Log mappings can select:

- event and observed timestamps;
- trace and span IDs;
- severity number and text;
- body and record attributes;
- event name, flags, and dropped attributes count;
- resource and scope context.

`log.message` is a string projection of the body. String bodies are copied directly; structured bodies are encoded as compact JSON. Select `log.body` instead when the target column should retain the body structure.

## Traces

Trace mappings can select:

- trace, span, and parent span IDs;
- trace state and flags;
- span name, kind, timestamps, duration, status, and attributes;
- dropped attribute, event, and link counts;
- events with timestamps, attributes, and dropped attribute counts;
- links with IDs, trace state, flags, attributes, and dropped attribute counts;
- resource and scope context.

## Metrics

| Metric type | Status | Available mapping data |
| --- | --- | --- |
| Gauge | Supported | Integer/double value, value type, timestamps, flags, attributes, exemplars |
| Sum | Supported | Gauge fields plus temporality and monotonicity |
| Histogram | Supported | Count, sum, min, max, bounds, bucket counts, flags, exemplars, temporality |
| Exponential histogram | Supported | Count, sum, min, max, scale, zero data, positive/negative buckets, flags, exemplars, temporality |
| Summary | Supported | Count, sum, quantiles, flags |

Metric name, description, unit, metadata, resource, and scope context are available for every data point. When selected, exemplar values retain an explicit `int` or `double` value type, filtered attributes, timestamp, trace ID, and span ID.

## Rejection Behavior

- Invalid OTLP wire data is rejected by the upstream OTLP receiver.
- A metric with no data type is rejected as a permanent mapping error instead of being skipped.
- A gauge or sum point with no numeric value is rejected as a permanent mapping error instead of being written with `null`.
- A future metric type unknown to the compiled exporter is rejected by the default mapping branch.

Mapping errors reject the Collector export batch containing the invalid metric. Telescope counts the item that triggered rejection under `invalid_items_by_reason`; it does not attempt per-record partial success.

Current reasons are:

| Reason | Trigger |
| --- | --- |
| `unsupported_metric_type` | A metric has no supported OTLP data type. |
| `unsupported_number_value_type` | A gauge or sum data point has no integer or double value. |

## Ingestion Status

`GET /v1/ingestion/status` reports the current data path without querying ScopeDB telemetry tables. For each signal it includes:

- OTLP receiver accepted, failed, and refused records;
- successfully written records and records involved in failed write attempts;
- exporter queue enqueue failures, current size, capacity, and configured unit;
- the configured ScopeDB table;
- last write attempt, success, failure, duration, and error;
- records rejected by permanent mapper or ScopeDB errors observed by the exporter.
- invalid mapper items grouped by stable reason.

Receiver and queue counts come directly from the OpenTelemetry Collector's internal metrics. `write_failed_attempt_records` counts records in attempts, so the same record can appear more than once when OTel retries it; it is not a loss or deduplication counter. Telescope does not add a second retry or queue implementation for this endpoint.

`invalid_items_by_reason` counts the malformed item that caused a batch rejection. `permanent_failed_records` counts the signal records affected by permanent mapper or ScopeDB errors. The two counters intentionally have different units; for example, a metric with no data type increments `unsupported_metric_type` but contains zero metric points.

When the embedded profile uses `unit: bytes`, queue size and capacity are logical serialized telemetry bytes reported by Collector. They are not the file-storage directory's exact disk usage.

The endpoint uses the Collector's local Prometheus endpoint at `http://127.0.0.1:8888/metrics` by default. Set `TELESCOPE_INTERNAL_METRICS_URL` when a custom Collector configuration moves that endpoint. If internal metrics cannot be read, the status response remains available but reports `internal_telemetry.available: false` and a `degraded` signal state.

## Known `v1` Gaps

- Byte-valued attributes and bodies are mapped as base64 strings without an explicit byte type marker.
- OpenTelemetry resource entity references are not mapped by the current exporter.
- Exemplar records with no value are retained without a `value` or `value_type`; they do not yet produce a reason-labelled diagnostic.
- Telescope does not normalize semantic-convention aliases. Map the desired attribute keys directly or normalize them with an upstream OpenTelemetry processor.

These gaps require an additive source-selector extension. They must be added to the golden corpus before their behavior changes.

## Upgrade Gate

Any OpenTelemetry Collector dependency update must pass:

```bash
go test ./packages/scopedbexporter -run TestGoldenMappingContractV1
```

A changed golden result is a source-mapping contract change. Update it only after deciding whether existing selectors keep their meaning and documenting any required mapping change.
