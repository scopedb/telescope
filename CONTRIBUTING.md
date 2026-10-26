# Contributing

Use [GitHub Issues](https://github.com/scopedb/telescope/issues) for bugs and design proposals. Changes to the mapping contract or persistent queue format need an explicit migration decision and regression coverage.

## Repository boundaries

This repository is one Go module, `github.com/scopedb/telescope`.

| Path | Responsibility |
| --- | --- |
| `cmd/telescope/` | Process entrypoint, CLI parsing and presentation, runtime lifecycle, and operational HTTP client |
| `internal/collector/` | Embedded Collector profile, environment defaults, component factories, and persistent queue integration |
| `internal/status/` | Health, readiness, captures, status, and Prometheus HTTP handlers; combines exporter observations with Collector metrics |
| `internal/httpclient/` | Owned SDK HTTP transports and redirect policy |
| `scopedbexporter/` | Public Collector factory and configuration, shared mapping contract and diagnostics; private append and partial-delivery implementation |
| `deploy/` | User-editable deployment examples, OTLP samples, and Prometheus rules |
| `docs/` | Mapping, compatibility, exporter, operations, migration, and release documentation |
| `mise.toml` | Development tools and task entrypoints |
| `scripts/` | Deployment and distribution checks invoked by mise |
| `.github/workflows/` | Pull request checks and release build/publish orchestration |

The command composes Collector and status; both depend on the exporter. The exporter does not depend on the command, Collector assembly, or operational HTTP server. Mapping inspection, preview, destination validation, and table planning stay alongside the production mapper so all callers use the same rules. Intermediate records and append outcomes are private; custom distributions consume telemetry through the Collector factory.

Tests live beside the behavior they exercise. `scopedbexporter/testdata/golden/` freezes normalized records and v0.2-compatible rows; `internal/collector/testdata/queue/v1/` freezes the persistent queue format. These are compatibility inputs, not generated output. Do not replace them to make an incompatible change pass.

Keep each regression at the boundary that owns it: mapping fixtures for record shape, exporter HTTP requests and final counters for delivery, Collector restarts for persistence, and CLI tests for configuration and operational wiring. Reuse these tests instead of repeating each outcome at every layer. Prefer `testify` assertions and small setup helpers; avoid tests of constructor fields, private forwarding helpers, and complete help or metrics text when observable behavior is already covered.

`bin/` and `dist/` are ignored build outputs. A release archive contains the binary and operator documentation/examples, not source, tests, development scripts, or private deployment files. The container contains the runtime binary, license, and CA certificates; configuration and queue storage are supplied at deployment.

## Local checks

Install [mise](https://mise.jdx.dev/); `mise.toml` pins the Go and HawkEye versions. Tasks use Bash. From the repository root, run:

```bash
mise install
mise run fix
mise run check
mise run test:race
mise run ci-runtime
```

`check` verifies license headers, formatting, and modules, runs vet and staticcheck, scans reachable vulnerabilities, and compiles integration tests. `ci-runtime` also needs Docker Compose, kubectl, and Python 3.12 or later; it validates configuration, deployment wiring, container health/status/metrics, Kubernetes rendering, and release archives. It does not deploy anything. Use `mise tasks` to list the available tasks.

Use `mise run test` for a quick unit run. Integration-tagged files are compiled and checked by the standard checks; live ScopeDB tests require an explicit target:

```bash
export TELESCOPE_SCOPEDB_INTEGRATION_ENDPOINT=https://example.scopedb.cloud
export TELESCOPE_SCOPEDB_INTEGRATION_API_KEY=...
mise run test-integration
```

Use a non-production workspace. The tests create uniquely named temporary tables and delete them through cleanup hooks. `TELESCOPE_SCOPEDB_INTEGRATION_TENANT_ID` is available for a test server that requires tenant routing. Tests verify HTTP and gRPC ingestion, typed Append readback, row isolation, and v0.2-compatible schemas. The explicit integration target fails when credentials are absent instead of reporting a skipped run as success.

## Build and release

`mise run build` produces `bin/telescope`. `VERSION=v0.3.0 mise run artifacts` builds macOS arm64 and Linux amd64/arm64 archives plus `dist/SHA256SUMS`; it does not publish them. Task settings are environment variables: use `PLATFORMS` to restrict a local build and `DIST_DIR` to choose its output directory. Without `VERSION`, builds use `git describe --tags --always`.

Pull requests run Go and runtime checks. Changes to release inputs also run the multi-platform image dry run. The release workflow publishes only on version tags; manual dispatch validates builds without publishing.

Record user-visible changes in [CHANGELOG.md](CHANGELOG.md), compared with the latest release. Keep `Unreleased` first, followed by `Breaking changes`, `New features`, `Bug fixes`, and `Improvements`, omitting empty categories. Fold corrections to unreleased features into their existing entries. Before tagging, rename the section to `vX.Y.Z (YYYY-MM-DD)` and prepend a new `Unreleased` section. The release workflow uses the matching version section as its notes and fails if that section is missing.
