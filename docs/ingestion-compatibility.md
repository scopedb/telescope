# Telescope Ingestion Compatibility

Compatibility contract: `v1` source mapping

This reference documents the OTLP data Telescope accepts and the fields available to mappings. The executable source contract is the versioned corpus under `scopedbexporter/testdata/golden/v1`.

Only fields selected in a signal's [mapping](table-management.md) become ScopeDB columns.

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
| Full normalized record | `record` |
| Deterministic content identifier | `record.id` |

Selected attribute strings, integers, doubles, booleans, arrays, and nested key-value lists retain their JSON type and structure. Selected byte values are base64-encoded strings and do not yet retain an explicit byte type marker.

Use the [mapping guide](table-management.md#mapping-model) for paths, fallback, casts, and validation. `telescope inspect <signal> --sample <file>` lists the exact selectors populated in your input.

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

Telescope removes an invalid metric before the exporter queue and continues with the valid metrics in the same Collector batch. The invalid metric is dropped as a unit: if one gauge or sum point has no value, the other points belonging to that metric are not written. Telescope does not attempt per-data-point repair.

After dequeue, destination projection treats each log record, span, or metric point independently. A cast, JSON encoding, or row-size failure drops only that item and does not prevent valid items in the same batch from being appended. If ScopeDB rejects a chunk and reports a complete, non-truncated set of row errors, Telescope removes those rows and attempts the remaining rows once. It does not bisect a chunk or repeat this isolation pass; incomplete rejection details fail the whole uncommitted chunk.

Reported reasons are:

| Reason | Trigger |
| --- | --- |
| `unsupported_metric_type` | A metric has no supported OTLP data type. |
| `unsupported_number_value_type` | A gauge or sum data point has no integer or double value. |
| `mapping_cast_failed` | A present runtime value cannot be converted by the configured mapping cast. |
| `mapping_encoding_failed` | A projected row contains a value that cannot be represented as JSON. |
| `mapped_row_too_large` | One projected NDJSON row exceeds the ScopeDB append request limit. |

## Delivery and status

See the [append and retry contract](exporter.md#append-and-retry-semantics) for partial failures and queue replay, and [operational status](operations.md#status-and-metrics-reference) for counters, endpoints, and internal metrics forwarding.

## Current `v1` Limitations

- Byte-valued attributes and bodies are mapped as base64 strings without an explicit byte type marker.
- OpenTelemetry resource entity references are not mapped by the `v1` exporter contract.
- Exemplar records with no value are retained without a `value` or `value_type`; they do not yet produce a reason-labelled diagnostic.
- Telescope does not normalize semantic-convention aliases globally. Use an ordered `sources` rule when aliases should feed one destination column, or normalize them upstream when other consumers also need the canonical field.

For dependency upgrade checks and fixture policy, see [Contributing](../CONTRIBUTING.md).
