# Deploy Telescope

Prepare and validate the ScopeDB tables with the [quickstart](quickstart.md) before routing application traffic. Pin the binary or image to a reviewed commit while v0.3 is unreleased, and to an immutable release tag once published.

## Native process

```bash
telescope run --env-file deploy/.env deploy/telescope.yaml
```

Run under your service manager and send SIGTERM for a graceful stop. Each instance needs its own persistent `TELESCOPE_QUEUE_DIR`; the native default is `~/.telescope/queue`. Keep the directory across binary upgrades and follow the [drain procedure](operations.md#drain-and-upgrade) before changing mappings.

## Docker Compose

With `deploy/.env` and `deploy/telescope.yaml` prepared:

```bash
make docker-build
IMAGE=scopedb-telescope:ci docker compose --env-file deploy/.env \
  -f deploy/docker-compose.yaml up -d
```

Compose publishes OTLP on ports 4317/4318 and operational HTTP on `127.0.0.1:8080`. Its named volume preserves the queue across container replacement. Use `docker compose --env-file deploy/.env -f deploy/docker-compose.yaml exec telescope telescope status` to inspect delivery. For a published release image, set `IMAGE=ghcr.io/scopedb/telescope:<version>` and omit the build step.

## Kubernetes

Use the [StatefulSet guide](../deploy/kubernetes/README.md). Each replica gets an independent queue volume; the Service exposes OTLP. The example overlay is the starting point for your image tag, mapping, storage class, and tuning.

## Configuration and capacity

Connection settings resolve in this order: command flags, `TELESCOPE_SCOPEDB_*`, then `SCOPEDB_ENDPOINT` / `SCOPEDB_API_KEY`. An `--env-file` fills unset values using literal `KEY=VALUE` lines and optional matching quotes; it performs no shell expansion and does not support inline comments.

[The environment example](../deploy/.env.example) lists batching, append limits, timeouts, and queue controls. Compose forwards all of them. In Kubernetes, set tuning through an environment patch in your overlay and keep credentials in the Secret.

`TELESCOPE_QUEUE_MAX_BYTES` is a **per-signal** logical capacity, 512 MiB by default. Size the volume above the sum for enabled signals, plus queue database overhead. Never share a queue directory between processes. On network storage, validate locking, sync behavior, and startup latency in the actual deployment.

Use [health, readiness, and metrics](operations.md#status-and-metrics-reference) to distinguish a running process from a destination that is accepting writes.
