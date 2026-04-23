# Semantic Agent API Design

## Status

- status: draft
- scope: agent-facing observability tools over the current ScopeDB OTel landing schema
- audience: contributors working on the API service

## Problem

The repository currently writes OpenTelemetry logs, traces, and metrics into three landing tables:

- `scopedb.otel.logs`
- `scopedb.otel.traces`
- `scopedb.otel.metrics`

Those tables are good ingest targets, but they are not yet a good tool surface for a user-owned developer agent. The agent already has code, deploy, ownership, and product context. ScopeDB should provide a thin observability data plane rather than an investigation workflow API.

## Goals

- Keep the landing schema as the source of truth.
- Expose a small agent-first API surface.
- Support both detail lookup and trend or breakdown queries.
- Keep request shapes structured and machine-composable.
- Separate semantic query intent from storage and index optimizations.

## Non-goals

- A UI-facing troubleshooting API.
- Incident workflow modeling.
- A free-form string search DSL.
- A custom general-purpose query language replacing ScopeQL.
- Object-style fetch APIs in v1.

## Landing Schema

The landing schema remains the evidence layer.

- `logs` store discrete event evidence.
- `traces` store execution evidence.
- `metrics` store numeric evidence.
- `record` remains the full-fidelity source of truth.

Recent promoted fields include:

- `row_id`
- `service_name`
- `instance_id`
- `pod_name`
- `host_ip`
- `host_name`

### `row_id`

`row_id` is not an object identity. It is a stable tie-breaker for detail queries and pagination.

Current derivation:

- each ingest payload gets an ephemeral `ingest_id`
- each row gets a `row_ordinal`
- `row_id` encodes `ingest_id + row_ordinal` as a fixed 8-byte hex string

This keeps pagination stable while leaving room to change the encoding later.

## Semantic Relations

The semantic layer should expose three query-oriented relations:

- `events_v1`
  Source: `scopedb.otel.logs`
  Purpose: discrete event and debug evidence

- `executions_v1`
  Source: `scopedb.otel.traces`
  Purpose: execution-centric request debugging
  V1 approximation: root spans where `parent_span_id` is empty

- `measurements_v1`
  Source: `scopedb.otel.metrics`
  Purpose: numeric anomaly and regression investigation

## API Surface

The public API surface should be reduced to three primitives:

- `GET /v1/schema`
- `GET /v1/schema/guide.md`
- `POST /v1/search`
- `POST /v1/aggregate`

This API is for developer agents, not for UI workflows. Higher-level actions such as compare, summarize, investigate, and conclude should be handled by the agent itself.

## Schema

`GET /v1/schema` answers:

- which relations exist
- which fields each relation exposes
- which measures are supported
- which default sort and limit rules apply

Each relation should describe:

- `name`
- `kind`
- `time_field`
- `default_sort`
- `default_limit`
- `max_limit`
- `supports_search`
- `supports_aggregate`
- `advisory`
- `fields`
- `measures`

### Advisory

The canonical schema should stay machine-readable JSON. Advisory should be added to that JSON rather than replacing it with free-form Markdown.

Recommended relation-level advisory fields:

- `identity_fields`
- `anchor_fields`
- `default_project`
- `preferred_filters`
- `preferred_group_by`
- `preferred_measures`
- `common_patterns`
- `notes`

These are recommendations for agent planning, not execution constraints.

## Skill-Friendly Rendering

Idiomatic skill-style integrations should separate three layers:

- tools
  Structured JSON actions such as `schema`, `search`, and `aggregate`
- resources
  Human-readable guides or advisory content, often Markdown
- prompts
  Reusable recipes that teach an agent how to combine tools

For this service:

- `/v1/schema` remains the canonical JSON tool surface
- `/v1/schema/guide.md` is a Markdown guide rendered from that same JSON schema and advisory data
- common investigation playbooks should remain prompt or skill material, not new query endpoints

Each field should describe:

- `name`
- `type`
- `role`
- `filterable`
- `searchable`
- `patternable`
- `groupable`

Field capability flags are semantic. They should not expose physical index choices directly.

## Search

`POST /v1/search` returns detail rows.

Required request fields:

- `source`
- `time_range`

Optional request fields:

- `filter`
- `project`
- `sort`
- `limit`
- `cursor`
- `debug.scopeql`

### Default behavior

If `sort` is omitted, search defaults to:

- `ts DESC`
- `row_id DESC`

If `limit` is omitted, the relation default applies.

Pagination must use opaque cursors, not offsets.

Current boundary:

- cursors are currently guaranteed only for default detail ordering
- default detail ordering means `ts DESC, row_id DESC`
- callers should not combine cursor pagination with custom sort yet

### Response shape

Search returns:

- `rows`
- `page.limit`
- `page.has_more`
- `page.next_cursor`
- `meta.applied_query`
- `meta.warnings`
- `meta.debug.generated_scopeql` when `debug.scopeql` is `true`

`generated_scopeql` is intentionally opt-in. General developer agents should rely on `applied_query`, rows, pagination, and evidence fields instead of coupling to ScopeQL execution details.

`meta.applied_query` summarizes the effective API-level query:

- `source`
- `time_range`
- `filter`
- `project`
- `sort`
- `limit`
- `has_cursor`

## Aggregate

`POST /v1/aggregate` returns grouped or bucketed results.

Required request fields:

- `source`
- `time_range`

Optional request fields:

- `filter`
- `group_by`
- `measures`
- `sort`
- `limit`
- `debug.scopeql`

This single primitive covers:

- breakdowns by field
- top-N contributor views
- trends by time bucket

There is no separate `series` endpoint in v1.

Aggregate responses use the same `meta.applied_query` envelope. For aggregate requests it may include:

- `source`
- `time_range`
- `filter`
- `group_by`
- `measures`
- `sort`
- `limit`

### Grouping

`group_by` supports two forms:

- field grouping
- time bucket grouping

Examples:

- `{"field":"service_name"}`
- `{"time_bucket":{"field":"ts","interval":"5m"}}`

`time_bucket.interval` should be a duration string such as:

- `30s`
- `5m`
- `15m`
- `2h`
- `24h`
- `1d`

The API should accept arbitrary duration strings and compile them to the closest exact ScopeQL `floor(timestamp, n => ..., unit => ...)` form.

### Measures

Initial supported measure ops:

- `count`
- `count_distinct`
- `sum`
- `avg`
- `min`
- `max`
- `p50`
- `p95`
- `p99`

## Filter Model

Both `search` and `aggregate` share the same structured `FilterExpr`.

Supported logical operators:

- `and`
- `or`
- `not`

Supported scalar predicates:

- `eq`
- `in`
- `gt`
- `gte`
- `lt`
- `lte`
- `exists`

Supported string-match predicates:

- `search`
- `contains`
- `regexp_like`

### `search`

`search` is a filter predicate backed by ScopeDB search semantics. It is not a free-form query language and does not imply relevance scoring.

Observed behavior is closer to token or phrase matching than to a UI-style search DSL. It should be modeled as:

- `{"search":["message","payment timeout"]}`

It may be accelerated by ScopeDB search indexes, but that is an execution concern.

### `contains`

`contains` is a substring predicate:

- `{"contains":["message","timeout"]}`

It may be accelerated by pattern indexes or ngram-style optimizations, but that is also an execution concern.

### `regexp_like`

`regexp_like` is a regex predicate:

```json
{
  "regexp_like": {
    "field": "message",
    "pattern": "timeout|deadline",
    "flags": "i"
  }
}
```

It is more expressive and potentially more expensive than `contains`.

## Execution and Optimization

Semantic API requests should compile to ScopeQL.

Physical optimization remains below the API boundary:

- search indexes may accelerate `search`
- pattern or ngram indexes may accelerate `contains` and `regexp_like`
- materialized indexes may accelerate promoted fields

The API should express filter semantics, not index strategy.

## Registry

The registry source of truth should remain in Go under:

- `services/api/internal/semantic`

The registry should stay thin:

- fields
- relations
- supported measures
- simple expression AST

It should not become a second query language.

## Current Implementation Gap

What this design assumes:

- the semantic registry remains the source of truth
- `row_id` is available on landing rows
- `search` and `aggregate` are the primary public tools

What still needs implementation work:

- refine cursor pagination for non-default sorts if that becomes necessary
- add guardrails for excessively fine-grained buckets and very high point counts
- tighten validation and explain metadata as the agent tool surface stabilizes
