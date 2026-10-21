# Migrate a v0.2 exporter to Append

The v0.2.0 release posts a data-and-statement envelope to `/v1/ingest`. The v0.3 implementation on `main` uses the ScopeDB Go SDK's `Table.AppendNDJSON` and `POST /v1/databases/{database}/schemas/{schema}/tables/{table}/rows`. It needs ScopeDB 0.5.6 or later. Changing only the old `path` setting cannot migrate a v0.2 binary: Append expects mapped NDJSON rows, not the legacy envelope or a ScopeQL transformation.

Until a v0.3 image is published, build the public source revision you have reviewed with `make docker-build VERSION=<revision>` and run that local image. Do not pair the v0.2.0 image with a v0.3 configuration or treat `latest` as evidence of Append support.

## Preserve the analysis contract explicitly

The [checkout mapping](../deploy/otel-checkout.yaml) selects the fields needed to join failed payment spans to their logs and group attempts by business order. It enables traces and logs only; no metrics table is required. Business keys such as `order.id` remain literal keys in the `attributes` object, while `resource` preserves service and environment context.

For a test workspace, create the following schema and tables by executing each block separately in ScopeDB Console. The new schema keeps the old v0.2 tables available for comparison; this example does not backfill or delete them.

```scopeql
CREATE SCHEMA IF NOT EXISTS scopedb.otel_checkout_append;
```

```scopeql
CREATE TABLE IF NOT EXISTS scopedb.otel_checkout_append.traces (
    start_timestamp timestamp,
    duration_ns int,
    trace_id string,
    span_id string,
    parent_span_id string,
    span_name string,
    status_code string,
    error_type string,
    attributes object,
    resource object
);
```

```scopeql
CREATE TABLE IF NOT EXISTS scopedb.otel_checkout_append.logs (
    record_timestamp timestamp,
    trace_id string,
    span_id string,
    message string,
    attributes object,
    resource object
);
```

`IF NOT EXISTS` does not repair an incompatible existing table. Review the mapping and validate its destination before starting the new instance:

```bash
export SCOPEDB_ENDPOINT="https://<your-workspace-api-address>"
export SCOPEDB_API_KEY="<your-api-key>"
./bin/telescope validate deploy/otel-checkout.yaml
./bin/telescope run deploy/otel-checkout.yaml
```

For another schema, edit the mapping, preview representative samples, and use `telescope plan --format scopeql <config>` to generate additive DDL for review. Telescope never applies DDL itself.

| v0.2 behavior                                        | Append migration                                                                                                                  |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `path: /v1/ingest` and an implicit ScopeQL transform | Remove `path`; declare `signals.<signal>.table` and `mapping`.                                                                    |
| `create_tables_if_not_exist` and `schema_version`    | Remove both; create and validate the user-owned table schema.                                                                     |
| `record['attributes']['order.id']`                   | With this mapping, query `attributes['order.id']`.                                                                                |
| Generated `row_id` and `ingest_ts`                   | No implicit columns; neither was a business idempotency key or a database commit timestamp.                                       |
| All three table routes required                      | Configure only the signals you actually export.                                                                                   |
| `collector --config=...`                             | Prefer `run <telescope.yaml>`; custom Collector distributions use `advanced collector --config=...` with `tables` and `mappings`. |

Append does not execute arbitrary ScopeQL. Map fields and casts in Telescope; use upstream OTel processors for filtering or routing. Transformations requiring database functions or joins need a separate query over a staging table. That query and the Append request commit independently.

## Cut over without reinterpreting queued telemetry

1. Stop sending new OTLP to the v0.2 instance and drain its queue using its original binary and configuration.
2. Deploy the Append instance with the reviewed mapping and a new queue volume. Keep the old tables and queue until the migration is verified.
3. Send a trace and correlated log, wait for the Append acknowledgement, and query both tables. A successful OTLP response only confirms collector acceptance.
4. Route new traffic to the Append instance. Update queries and dashboards to the selected columns; compare business conclusions before retiring the old deployment.

The writer caps each request at 8 MiB of uncompressed NDJSON and 200,000 rows. Collector owns retries and the persistent queue. The default embedded profile retries transient failures without an elapsed-time cutoff, subject to its bounded queue; permanent failures are dropped. A confirmed chunk is excluded from a later retry, but an `unknown` outcome may already have committed and can be duplicated. Keep trace/span IDs and business identifiers, and account for duplicate delivery in queries. See the [exporter's retry contract](../packages/scopedbexporter/README.md#append-and-retry-semantics).
