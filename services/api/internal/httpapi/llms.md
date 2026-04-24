# Telescope

> Telescope is a local telemetry runtime for developer agents. It receives OpenTelemetry logs, traces, and metrics, stores them in ScopeDB, and exposes a small HTTP/MCP debugging surface for schema discovery, detail search, and aggregate analysis.

Use this file as the high-level map for agents connecting to a running Telescope daemon. The daemon is an early prototype focused on the agent-facing observability loop rather than dashboards or a managed control plane.

Important notes:

- The HTTP API and MCP server are served by the telescope daemon.
- Agents should inspect the schema before composing search or aggregate requests.
- Query filters use promoted semantic field names only. The record field remains full-fidelity evidence, but arbitrary record paths are not accepted by the default search or aggregate filters.
- Telemetry is stored in separate ScopeDB tables for logs, traces, and metrics.

## Runtime Endpoints

- [Health](/healthz): Service status for the running daemon.
- [Semantic schema](/v1/schema): Machine-readable relation, field, measure, and advisory metadata.
- [Schema guide](/v1/schema/guide.md): Markdown guide generated from the canonical schema for agent planning.
- [Search](/v1/search): POST endpoint for detail telemetry rows.
- [Aggregate](/v1/aggregate): POST endpoint for grouped, bucketed, or summarized telemetry rows.
- [MCP](/mcp): Streamable HTTP MCP endpoint exposing health, schema, schema_guide, search, and aggregate tools.

## Optional

- [ScopeDB documentation](https://docs.scopedb.io/): Reference material for ScopeQL, data types, ingest, and product concepts.
- [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/): Upstream collector documentation relevant to the exporter and collector runtime.
- [Model Context Protocol](https://modelcontextprotocol.io/): Protocol used by Telescope's MCP tool surface.
