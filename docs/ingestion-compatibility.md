# Telescope Ingestion Compatibility

Status: current `v1` source-mapping contract

This document defines what Telescope accepts through OTLP and makes available to user mappings. The executable source contract is the versioned corpus under `packages/scopedbexporter/testdata/golden/v1`.

This is not a mandatory ScopeDB row schema. Only sources selected in a configured signal's `mapping` are serialized into its destination columns. See [Mapping and Table Management](table-management.md).

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

Any object or array source can be followed by chained `["key"]` and `[index]` path segments. Mapping rules can select the first present source, apply a default when all sources are absent, emit a constant, and cast the output to `string`, `int`, `uint`, `float`, `boolean`, `timestamp`, `object`, `array`, or explicitly `any`. Structured casts validate the runtime shape, and `any` is never selected implicitly. These operations shape only the destination row; they do not mutate the OpenTelemetry input.

`telescope validate` checks every selected destination column before deployment. `telescope run` repeats the check when ScopeDB is reachable, while temporary destination failures do not block listeners or the persistent queue. Rules with a fixed output type, including explicit casts and constants, are checked against the ScopeDB catalog type; timestamps may target `timestamp` or `string`, and `any` accepts every fixed output type. A cast can still require a sample when input values determine whether conversion succeeds. Uncast individual attributes, nested paths, `log.body`, and other runtime-typed values are checked for column existence without guessing their type. `telescope preview` accepts repeatable `--sample signal=path` arguments, projects representative OTLP JSON or protobuf through the production mapper, reports per-column coverage, observed output types, and selected source/default counts, and compares them with the destination without appending rows. `--strict` turns incomplete or default-only sample coverage into a non-zero result. Use `telescope capture <signal>` to obtain a bounded sample from a running Telescope exporter input.

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

Current reasons are:

| Reason | Trigger |
| --- | --- |
| `unsupported_metric_type` | A metric has no supported OTLP data type. |
| `unsupported_number_value_type` | A gauge or sum data point has no integer or double value. |
| `mapping_cast_failed` | A present runtime value cannot be converted by the configured mapping cast. |
| `mapping_encoding_failed` | A projected row contains a value that cannot be represented as JSON. |
| `mapped_row_too_large` | One projected NDJSON row exceeds the ScopeDB append request limit. |

## Ingestion Status

`telescope capture --listen-http <address> <signal>` starts a temporary standalone OTLP/HTTP endpoint for cold-start sampling. It accepts JSON or protobuf, supports identity and gzip content encoding, limits each request to 20 MiB, and emits a bounded standard OTLP JSON sample. It does not load a mapping, contact ScopeDB, persist, queue, retry, or forward input.

`telescope inspect <signal> --sample <path>` decodes OTLP JSON or protobuf through the production mapper and reports exact mapping selectors, observed types, and the records where each selector has a populated value. Empty protocol defaults are omitted. Nested objects are expanded; arrays remain whole values. It neither displays sample values nor generates or persists a mapping.

`GET /v1/ingestion/capture?signal=<signal>&limit=<records>&timeout=<duration>` performs one on-demand capture at exporter input, after Collector batch processing and before the exporter sending queue, and returns an OTLP JSON export request. The native record unit is log records, spans, or metric points. It returns a partial sample when the timeout expires after receiving data and retains no background sample buffer. The default 45-second timeout exceeds the bundled 30-second batch timeout so low-volume input can reach the capture.

`GET /v1/ingestion/status` reports the current data path without querying ScopeDB telemetry tables. It lists only configured signals. For each one it includes:

- OTLP receiver accepted, failed, and refused records;
- successfully written and ultimately dropped records;
- drops split into exhausted retry, queue refusal, and permanent rejection counts;
- exporter queue enqueue failures, current size, capacity, and configured unit;
- filesystem blocks allocated to the persistent queue directory;
- the configured ScopeDB table;
- last write attempt, success, failure, duration, and error;
- whether the destination has been verified by a table check or successful append;
- the synthetic probe IDs in the last successfully appended batch and their confirmation time;
- records rejected by permanent mapper or ScopeDB errors observed by the exporter;
- invalid mapper items grouped by stable reason.

This endpoint reports data-plane delivery state only. It does not report table queryability.

Receiver, final exporter failure, and queue counts come from the OpenTelemetry Collector's internal metrics. `written` instead counts native signal items only when ScopeDB confirms a chunk as `committed` with the expected inserted-row count, so a committed prefix remains visible even if a later chunk fails. The exporter outcome metric wraps Collector's retry sender, so intermediate attempts are not counted as drops. `dropped` combines final exporter failures, queue enqueue failures, invalid items isolated locally, and complete ScopeDB row rejections. `retry_exhausted` is the retryable part of final exporter failures. The embedded profile has no elapsed-time retry cutoff, so this normally remains zero; it can increase when retry cannot continue during shutdown or an advanced Collector configuration sets a finite horizon. `permanent_rejected` includes permanent mapping and ScopeDB rejections. Telescope does not add a second retry or queue implementation.

The reported exporter queue excludes telemetry held by the Collector batch processor and an export currently in flight. Therefore a transient `received > written + dropped` with an empty exporter queue does not imply data loss. Human-readable `telescope status` calls this out as accepted items without a final outcome; it does not publish a synthetic durable counter for that transient state.

`invalid_items_by_reason` counts locally identifiable mapping failures, while the delivery counters use each signal's native unit: log records, spans, or metric points. Human-readable `telescope status` prints the non-zero reason counts for each signal. A metric with no data type therefore increments `unsupported_metric_type` but contributes zero metric points to `permanent_rejected` and `dropped`.

When the embedded profile uses `unit: bytes`, queue size and capacity are logical serialized telemetry bytes reported by Collector. `queue_storage.allocated_bytes` separately reports filesystem blocks allocated to files in `TELESCOPE_QUEUE_DIR`; it does not treat the queue database's logical file length as current disk use.

`GET /metrics` exposes the same delivery facts as stable Prometheus metrics. This includes accepted, written, and finally dropped items; logical queue size and capacity; last write success; destination verification; and allocated queue storage. Load [`deploy/prometheus-rules.yaml`](../deploy/prometheus-rules.yaml) for the baseline alerts. Disk high-water history belongs in Prometheus and is calculated with `max_over_time`; Telescope does not retain another history database. The endpoint returns `503` rather than publishing false zeroes when Collector metrics are unavailable.

The status and metrics endpoints read Collector's private Prometheus endpoint at `http://127.0.0.1:8888/metrics` by default. Set `TELESCOPE_INTERNAL_METRICS_URL` when a custom Collector configuration moves it. Scrape Telescope's operational `/metrics` endpoint, not port `8888`; the latter is an internal implementation detail. If internal metrics cannot be read, the status response remains available but reports `internal_telemetry.available: false` and a `degraded` signal state. State is limited to component health (`starting`, `ready`, `degraded`, or `refusing`); counters, queue size, and timestamps describe data movement without inferring flow from an old success.

## Known `v1` Gaps

- Byte-valued attributes and bodies are mapped as base64 strings without an explicit byte type marker.
- OpenTelemetry resource entity references are not mapped by the current exporter.
- Exemplar records with no value are retained without a `value` or `value_type`; they do not yet produce a reason-labelled diagnostic.
- Telescope does not normalize semantic-convention aliases globally. Use an ordered `sources` rule when aliases should feed one destination column, or normalize them upstream when other consumers also need the canonical field.

These gaps require an additive source-selector extension. They must be added to the golden corpus before their behavior changes.

## Upgrade Gate

Any OpenTelemetry Collector dependency update must pass:

```bash
go test ./packages/scopedbexporter -run TestGoldenMappingContractV1
```

A changed golden result is a source-mapping contract change. Update it only after deciding whether existing selectors keep their meaning and documenting any required mapping change.
