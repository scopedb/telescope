# Telescope

Telescope is an OpenTelemetry Collector distribution that receives OTLP telemetry and appends it to ScopeDB. It is the data plane between OpenTelemetry producers and user-managed ScopeDB tables.

It provides:

- OTLP/gRPC and OTLP/HTTP receivers for logs, traces, and metrics
- user-owned signal-to-table mappings
- a ScopeDB exporter built on the Go SDK append API
- batching, retry, memory limiting, and a persistent sending queue
- sample field inspection, mapping preview, additive ScopeDB table planning, preflight validation, live OTLP capture, and exact delivery probes
- a narrow ScopeQL query bridge for inspecting stored results
- operational health, readiness, and ingestion status endpoints

`telescope plan` never applies its generated DDL. You choose the signals, destination tables, and fields to append. The command can compare that logical write contract with the live catalog and render reviewable, additive ScopeQL; table creation, physical design, and DDL execution remain explicit and user-owned. At runtime Telescope's data-plane responsibility ends when ScopeDB reports the append result. The operator-invoked `telescope query` command is a narrow bridge for inspecting stored data while the standalone ScopeQL CLI is under development; it is not part of ingestion.

## Requirements

- Docker with Docker Compose for the recommended deployment path
- a reachable ScopeDB endpoint and API key
- pre-existing destination tables, or the ScopeQL CLI with a configured connection to apply a generated table plan
- OpenTelemetry clients, SDKs, or Collectors that can export OTLP

## Quick Start

Create the local configuration files:

```bash
cp deploy/.env.example deploy/.env
cp deploy/telescope.example.yaml deploy/telescope.yaml
```

Set the ScopeDB credentials in `deploy/.env`:

```bash
TELESCOPE_SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
TELESCOPE_SCOPEDB_API_KEY=sk_...
```

Then edit `deploy/telescope.yaml`. Only configured signals are accepted and started:

```yaml
signals:
  traces:
    table: scopedb.otel.traces
    mapping:
      timestamp: span.start_time
      trace_id: span.trace_id
      span_id: span.span_id
      service:
        sources:
          - resource.attributes["service.name"]
          - resource.attributes["service"]
        default: unknown
        cast: string
      name: span.name
      duration_ns: span.duration_ns
      status_code: span.status.code
```

A mapping value can stay as a source-selector shorthand, or use an expanded rule for ordered fallback, a missing-value default, a constant, and an explicit output cast. Object and array sources support chained access such as `log.body["request"]["id"]`. See [ScopeDB Mapping and Table Management](docs/table-management.md) for the complete contract.

For runtime-typed selectors such as attributes and `log.body`, and for casts whose input values vary at runtime, preview a representative OTLP JSON or protobuf payload before deployment:

```bash
docker run --rm \
  -v "$PWD/deploy/telescope.yaml:/etc/telescope/telescope.yaml:ro" \
  -v "$PWD/deploy/samples:/samples:ro" \
  ghcr.io/scopedb/telescope:latest \
  preview --offline \
    --strict \
    --sample traces=/samples/traces.otlp.json \
    /etc/telescope/telescope.yaml
```

The preview shows destination-column coverage, observed output types, and which ordered source or default supplied each value, then prints projected NDJSON without writing to ScopeDB. Mapping failures are collected across the sample and identify the record, column, and selected source. `--strict` also fails on unobserved, partial, or default-only columns. Omit `--offline` to include destination column types and detect sample/type mismatches.

The repository includes minimal OTLP payloads for all three signals under `deploy/samples/`; replace them with captured application traffic before finalizing a production mapping. Telescope can collect that first sample without a configuration file or ScopeDB destination, as described under [Capture Before Configuration](#capture-before-configuration).

Plan missing tables or columns, review the generated ScopeQL, and apply it explicitly:

```bash
docker run --rm \
  --env-file deploy/.env \
  -v "$PWD/deploy/telescope.yaml:/etc/telescope/telescope.yaml:ro" \
  ghcr.io/scopedb/telescope:latest \
  plan --format scopeql /etc/telescope/telescope.yaml > tables.scopeql
```

ScopeQL owns its connection and authentication configuration independently of Telescope. Before the first DDL apply on a host, configure and select the intended connection, then verify it before running the generated script:

```bash
scopeql config set-connection production
scopeql config use-connection production
scopeql config get-connections
scopeql run -f tables.scopeql
```

`scopeql config set-connection` prompts for the endpoint and authentication fields required by the installed ScopeQL version. Telescope's `--env-file` and `SCOPEDB_*` fallbacks do not create or migrate that ScopeQL connection.

`plan` generates only the missing `CREATE DATABASE`, `CREATE SCHEMA`, `CREATE TABLE`, and `ALTER TABLE ... ADD COLUMN` statements, in dependency order. It blocks on runtime-dependent output types, sample conversion failures, shared-column type disagreements, and incompatible existing columns. A sample can provide evidence, but it never chooses a table type; add an explicit mapping `cast` when the source type is dynamic. Telescope does not infer retention, clustering, distinct keys, or indexes, so review and extend the ScopeQL before applying it.

Validate the applied table contract before deployment:

```bash
docker run --rm \
  --env-file deploy/.env \
  -v "$PWD/deploy/telescope.yaml:/etc/telescope/telescope.yaml:ro" \
  ghcr.io/scopedb/telescope:latest \
  validate /etc/telescope/telescope.yaml
```

Start Telescope:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml up -d
```

For a source build, run `make docker-build` and set `IMAGE=scopedb-telescope:ci` when invoking Docker Compose.

### Kubernetes

The Kubernetes baseline uses a StatefulSet so each replica keeps a stable, independent persistent queue. Copy the example overlay, edit its mapping and image tag, then create the ScopeDB connection and apply it:

```bash
cp -R deploy/kubernetes/example deploy/kubernetes/local
# Edit deploy/kubernetes/local/telescope.yaml and pin newTag in kustomization.yaml.

kubectl create namespace telescope --dry-run=client -o yaml | kubectl apply -f -
kubectl -n telescope create secret generic telescope-scopedb \
  --from-literal=endpoint=https://<region>.scopedb.cloud \
  --from-literal=api-key=sk_... \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -k deploy/kubernetes/local
```

Applications inside the cluster can export to `telescope.telescope.svc.cluster.local:4317` or `http://telescope.telescope.svc.cluster.local:4318`. Inspect the first replica and verify its end-to-end delivery directly:

```bash
kubectl -n telescope exec telescope-0 -- telescope status
kubectl -n telescope exec telescope-0 -- telescope verify
```

The baseline starts one replica with a 2 GiB queue volume. Every additional StatefulSet ordinal receives its own volume; drain an ordinal before scaling it down. Kustomize gives the generated config a content hash and rolls the StatefulSet when the mapping changes. Apply such a change in place only after the existing queues and accepted-without-final-outcome counts reach zero. Otherwise deploy a second instance with distinct names, selectors, and volumes, route new OTLP to it, and let the old instance drain under its original config.

## Send Telemetry

The default listeners are:

- `localhost:4317` for OTLP/gRPC
- `localhost:4318` for OTLP/HTTP

For an OpenTelemetry SDK using OTLP/HTTP:

```bash
export OTEL_SERVICE_NAME=my-service
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=development
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
```

If another OpenTelemetry Collector already receives the telemetry, add Telescope as an OTLP exporter and include it in the existing pipelines:

```yaml
exporters:
  otlp/telescope:
    endpoint: telescope:4317
    tls:
      insecure: true

service:
  pipelines:
    traces:
      exporters: [otlp/telescope]
    metrics:
      exporters: [otlp/telescope]
    logs:
      exporters: [otlp/telescope]
```

## Operate Telescope

### Capture Before Configuration

Start a temporary OTLP/HTTP listener when representative application data is needed before the mapping and destination table exist:

```bash
telescope capture \
  --listen-http 127.0.0.1:14318 \
  --limit 100 \
  --timeout 2m \
  traces > traces.otlp.json
```

Point an application or a temporary second exporter in an existing Collector at `http://127.0.0.1:14318`. The command accepts standard OTLP/HTTP JSON or protobuf, including gzip requests, on `/v1/traces` and writes a standard OTLP JSON export request to stdout. It exits when the record limit is reached, returns a partial sample when the timeout expires after receiving data, and fails when no data arrives.

This mode starts only the selected OTLP/HTTP endpoint. It does not load `telescope.yaml`, connect to ScopeDB, map, persist, queue, retry, or forward telemetry. Only the bounded sample is retained, so use a second exporter when the original telemetry must continue to its existing destination. A single request is limited to 20 MiB.

Inspect the sample before writing a mapping:

```bash
telescope inspect traces --sample traces.otlp.json
```

`inspect` uses the production mapper to list exact source selectors, observed output types, and the records where each selector is populated, grouped by the OTLP resource, scope, and signal layers. It omits empty protocol defaults, marks partial and mixed-type selectors, expands nested objects into copyable selectors, and keeps arrays as whole values rather than suggesting brittle numeric indexes. It does not require a Telescope configuration or ScopeDB connection and never prints sample values. Use `--format json` for structured output.

Capture, retain, and inspect the first sample in one pipeline:

```bash
telescope capture --listen-http 127.0.0.1:14318 traces |
  tee traces.otlp.json |
  telescope inspect traces --sample -
```

With Docker, publish a temporary host port and keep stdout redirected to the host:

```bash
docker run --rm \
  -p 127.0.0.1:14318:4318 \
  ghcr.io/scopedb/telescope:latest \
  capture --listen-http 0.0.0.0:4318 \
    --limit 100 --timeout 2m traces > traces.otlp.json
```

Then feed the result directly into the normal setup sequence:

```bash
telescope preview --offline --strict \
  --sample traces=traces.otlp.json \
  deploy/telescope.yaml
```

### Preview a Mapping with Live Data

Capture a bounded sample at the running exporter's input and pipe it directly through a candidate mapping:

```bash
telescope capture \
  --endpoint http://127.0.0.1:8080 \
  --limit 100 \
  --timeout 45s \
  traces |
  telescope preview \
    --sample traces=- \
    deploy/telescope.yaml
```

`capture` waits until it collects the requested number of log records, spans, or metric points, or until the timeout returns a partial sample. It retains nothing when no capture is active. Capture observes exporter input after Collector batch processing and before the sending queue. The 45-second default exceeds the bundled 30-second batch timeout, while retries cannot duplicate the sample. `preview` uses the production mapper and does not append the sample. Redirect `capture` to a file when the same input should be replayed later.

### Inspect Delivery Status

Telescope exposes its operational HTTP surface on `127.0.0.1:8080` in the Docker deployment:

```bash
curl -sS http://127.0.0.1:8080/healthz
curl -sS http://127.0.0.1:8080/readyz
curl -sS http://127.0.0.1:8080/v1/ingestion/status
curl -sS http://127.0.0.1:8080/metrics
```

The ingestion status reports only configured signals, including received, ScopeDB-confirmed written, and dropped counts; exhausted retries, isolated or batch-level permanent rejections, and queue refusals; queue utilization and allocated queue storage; table routes; destination validation; and the latest write result. These are local data-plane facts; Telescope does not query destination tables. The exporter queue does not include telemetry still waiting in the Collector batch processor or an in-flight export; human-readable `telescope status` calls out accepted items that do not yet have a final outcome when the queue is empty. Collector owns retries. Retryable requests have no elapsed-time expiry by default and remain in the bounded persistent queue across restarts. A locally identifiable bad record is dropped without blocking valid neighbors; an unisolated permanent chunk failure stops immediately.

`/metrics` is the stable Prometheus surface for Telescope delivery and queue alerts. The bundled [Prometheus rules](deploy/prometheus-rules.yaml) cover queue saturation, final drops, stalled delivery, and unverified destinations, and record the queue directory's 24-hour disk high-water mark. Prometheus should also alert on its standard `up` metric: `/metrics` returns `503` instead of publishing false zeroes when Collector's internal telemetry is unavailable.

For a human-readable summary:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml \
  exec telescope telescope status
```

### Verify Delivery

To send a synthetic signal and wait for the exact ScopeDB append acknowledgement:

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml \
  exec telescope telescope verify
```

Expected output:

```text
traces: OTLP accepted synthetic probe (probe-...)
traces: ScopeDB append committed synthetic probe (probe-...)
```

### Query Stored Results

Use the temporary query bridge to inspect the rows produced by a mapping with the same ScopeDB connection already available to Telescope:

```bash
telescope query \
  "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' SELECT start_timestamp, trace_id, service LIMIT 1"

telescope query --format json \
  "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' LIMIT 1"

kubectl -n telescope exec telescope-0 -- \
  telescope query --format jsonl \
  "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' LIMIT 1"
```

It submits exactly one ScopeQL statement read from the argument, stdin, or `--file`, and supports human-readable `table` plus scriptable `json` and `jsonl` output. It reuses the normal `--scopedb-endpoint`, `--scopedb-api-key`, `--env-file`, and ScopeDB environment precedence. `--timeout` sets the server execution limit; interrupting the command also requests cancellation of the submitted statement.

Use a selective trace ID or time predicate when inspecting telemetry. `LIMIT` bounds returned rows, but it should not be treated as a scan budget.

This is intentionally not a second ScopeQL CLI: it has no REPL, connection profiles, history, or client-side multi-statement script handling. Those concerns belong to the standalone CLI.

### Upgrade or Change a Mapping

`telescope status` reports the running version and a normalized config digest. A binary-only upgrade may reuse the persistent queue when that digest is unchanged; Telescope's test suite verifies that the current binary can drain the frozen `v1` queue format for logs, traces, and metrics.

The queue retains OTLP before destination mapping. Do not start a changed table or mapping contract against a non-empty queue: older telemetry would be projected by the new mapping. Remove the old instance from its OTLP upstream, then wait until every queue is empty and `telescope status` reports no accepted items without a final outcome before changing the config. For a zero-downtime change, send new traffic to a separate deployment and queue volume while the old deployment drains with its original config.

## Local Binary

Build and run the embedded Collector:

```bash
make build

export SCOPEDB_ENDPOINT=https://<region>.scopedb.cloud
export SCOPEDB_API_KEY=sk_...

./bin/telescope preview --offline --sample traces=deploy/samples/traces.otlp.json deploy/telescope.yaml
./bin/telescope plan --out tables.scopeql deploy/telescope.yaml
./bin/telescope validate deploy/telescope.yaml
./bin/telescope query "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' LIMIT 1"
./bin/telescope run deploy/telescope.yaml
```

The local Telescope CLI accepts `SCOPEDB_ENDPOINT` and `SCOPEDB_API_KEY` as fallbacks. Command-line connection flags take precedence over `TELESCOPE_SCOPEDB_*`, which take precedence over those fallback variables. ScopeQL uses its own selected connection; Telescope environment files do not configure it. The Docker Compose example keeps using its explicit `TELESCOPE_SCOPEDB_*` deployment variables.

Commands:

- Setup:
  - `telescope inspect`: discover mapping selectors and observed types in an OTLP sample
  - `telescope preview`: project OTLP samples through a candidate mapping without appending
  - `telescope plan`: compare mappings with live tables and render additive ScopeQL
  - `telescope validate`: validate mapping rules and destination tables
  - `telescope run`: run the OTLP-to-ScopeDB data plane from the same configuration
- Operations:
  - `telescope status`: report local receiver, queue, and ScopeDB delivery state
- Diagnostics:
  - `telescope capture`: capture bounded OTLP from a temporary listener or a running instance
  - `telescope verify`: send synthetic OTLP and wait for confirmed ScopeDB appends
  - `telescope query`: execute one ScopeQL statement and render its result
- `telescope version`: print the build version

`preview`, `plan`, `validate`, and `run` use the same `telescope.yaml` contract. `plan --out tables.scopeql` keeps the actionable human plan on stdout and atomically writes the executable DDL without a second catalog read; a blocked plan leaves any existing output file untouched. `plan --format json` exposes the versioned generated plan for tooling, while `plan --format scopeql` remains available for stdout pipelines. `inspect` and standalone `capture` need no configuration or ScopeDB connection. Running-instance `capture`, `status`, and `verify` use Telescope's operational endpoint. `query` uses the ScopeDB connection directly and does not load `telescope.yaml`. `preview --offline` projects a file or stdin sample without connecting to ScopeDB. `verify` uses a minimal synthetic record to confirm transport and append acknowledgement; it does not prove that application-specific fields are populated. The upstream Collector command remains available as the advanced escape hatch `telescope advanced collector --config <collector.yaml>`.

For the mapping contract and table ownership model, see [Mapping and Table Management](docs/table-management.md). For supported source selectors, see [Ingestion Compatibility](docs/ingestion-compatibility.md).

## Development

```bash
make fmt
make check
make build
```

`make check` verifies formatting and module integrity, runs `go vet`, and executes the unit tests. `make test-race` adds the race detector; `make ci-runtime` validates the container, Kubernetes manifests, and release artifacts.

Project layout:

- `cmd/telescope`: Telescope CLI and runtime entrypoint
- `internal/collector`: embedded Collector configuration and component factories
- `internal/status`: operational health, readiness, and ingestion status endpoints
- `deploy`: Docker Compose deployment assets
- `packages/scopedbexporter`: ScopeDB OpenTelemetry Collector exporter
- `docs`: ingestion and table mapping documentation

Telescope is licensed under the [Apache License, Version 2.0](LICENSE).
