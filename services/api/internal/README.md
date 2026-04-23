# internal

This directory will hold the implementation details for the API service, including:

- ScopeQL query builder
- semantic field registry
- semantic relation definitions
- Go registry definitions and validation
- ScopeQL query compilation
- Echo HTTP handlers
- ScopeDB SDK-backed query execution

The current registry source of truth should live in this package tree rather than in external config files.
