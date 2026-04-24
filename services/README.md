# Services

Deployable services live under this directory.

Current layout:

- `gateway/collector`: the custom OTel Collector distribution used by the Telescope collector runtime
- `gateway/deploy`: deployment assets for the collector runtime
- `api`: developer-facing debug API and MCP service built on top of the ScopeDB OTel landing schema

When adding a new service later, prefer a sibling directory such as `services/<service-name>/`.
Keep reusable libraries in `packages/` and local development helpers in `tools/`.
