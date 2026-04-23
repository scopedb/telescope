# openapi

This directory stores the hand-written OpenAPI contract for the API service.

Current source of truth:

- `agent-openapi.yaml`: primary contract for the agent-facing observability tools API

Design notes:

- keep the HTTP layer agent-oriented
- keep the public surface small: `schema`, `schema/guide.md`, `search`, `aggregate`
- keep request and response shapes structured and machine-composable
- prefer a stable contract over premature code generation
