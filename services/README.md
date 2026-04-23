# Services

Deployable services live under this directory.

Current layout:

- `gateway/collector`: the custom OTel Collector distribution that serves as the current gateway runtime
- `gateway/deploy`: deployment assets for the gateway service
- `api`: planned developer-facing debug API service built on top of the ScopeDB OTel landing schema

When adding a new service later, prefer a sibling directory such as `services/<service-name>/`.
Keep reusable libraries in `packages/` and local development helpers in `tools/`.
