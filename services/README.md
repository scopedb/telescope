# Services

Deployable services live under this directory.

Current layout:

- `gateway/collector`: the custom OTel Collector distribution that serves as the current gateway runtime
- `gateway/deploy`: deployment assets for the gateway service

When adding a new service later, prefer a sibling directory such as `services/<service-name>/`.
Keep reusable libraries in `packages/` and local development helpers in `tools/`.
