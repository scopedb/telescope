# Send your first trace

This walkthrough uses the binary built in the [README](../README.md), a ScopeDB workspace, and its endpoint and API key. Run the commands from the repository root. In an extracted v0.3 release archive, use `./telescope` in place of `./bin/telescope`.

## 1. Choose the destination

```bash
cp deploy/.env.example deploy/.env
cp deploy/telescope.example.yaml deploy/telescope.yaml
```

Fill in `TELESCOPE_SCOPEDB_ENDPOINT` and `TELESCOPE_SCOPEDB_API_KEY` in `deploy/.env`. The example sends traces to `scopedb.otel.traces`; change `signals.traces.table` in `deploy/telescope.yaml` if that name is already used for a different schema.

The mapping selects trace IDs, timestamps, service, operation, and duration. Logs and metrics stay disabled until you add their own table and mapping. You can [change the columns](table-management.md) later.

## 2. Prepare the table

```bash
./bin/telescope plan --env-file deploy/.env \
  --out tables.scopeql deploy/telescope.yaml
```

Review `tables.scopeql`, then run its statements in the SQL editor for the same ScopeDB workspace. The plan creates missing namespaces and columns; it leaves retention, clustering, and indexes for you to choose. If the plan is blocked, fix the reported mapping or schema conflict and rerun it. Telescope never applies this script automatically.

Already using the ScopeQL CLI? You can also apply the file with `scopeql run -f tables.scopeql` after checking its separately configured connection with `scopeql config get-connections`.

## 3. Start Telescope

```bash
./bin/telescope validate --env-file deploy/.env deploy/telescope.yaml
./bin/telescope run --env-file deploy/.env deploy/telescope.yaml
```

Keep this terminal running. Once `telescope ready` appears, the OTLP receivers are listening on ports **4317** (gRPC) and **4318** (HTTP). Readiness means Telescope can receive data; the next step checks delivery to ScopeDB.

## 4. Write and verify

In a second terminal, from the same directory:

```bash
./bin/telescope verify traces
```

This sends a synthetic trace through OTLP and waits for its ScopeDB append acknowledgement. With the default batch timeout it can take about 30 seconds. Success ends with:

```text
traces: ScopeDB append committed synthetic probe (probe-...)
```

Read stored rows using the same connection:

```bash
./bin/telescope query --env-file deploy/.env \
  "FROM scopedb.otel.traces
   WHERE service = 'telescope-ingestion-probe'
   SELECT start_timestamp, service, span_name, trace_id LIMIT 5"
```

If verification fails, run `./bin/telescope status`: `last_error` identifies destination failures, while a growing queue means delivery is waiting or retrying. See [operations](operations.md) for the counters and troubleshooting steps.

You now have a working OTLP-to-ScopeDB path. [Connect your application or Collector](integrations.md), or use the [deployment guide](deployment.md) to run Telescope as a service. The synthetic trace remains in the table as ordinary telemetry.
