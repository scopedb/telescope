# Services

Deployable services live under this directory.

Current layout:

- `gateway/collector`: the custom OTel Collector distribution used by the Telescope collector runtime
- `gateway/deploy`: deployment assets for the collector runtime
- `api`: Telescope CLI, embedded Collector runtime, and operational ingestion endpoints

When adding a new service later, prefer a sibling directory such as `services/<service-name>/`.
Keep reusable libraries in `packages/` and local development helpers in `tools/`.
