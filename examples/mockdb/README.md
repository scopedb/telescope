# Mock Vendor Backend

This mock server accepts ScopeDB-style ingest requests on `POST /v1/ingest`.
It mirrors the production auth shape and only accepts Bearer auth.

Behavior:

- listens on `:8080`
- accepts `Authorization: Bearer demo-key`
- supports zstd, gzip, and plain JSON request bodies
- stores all received ingest requests and decoded rows in memory
- exposes `GET /debug/payloads` for inspection

Failure injection:

- `MOCKDB_FAIL_COUNT=3` returns `500` for the first three requests, then succeeds
- `MOCKDB_FORCE_STATUS=401` always returns the chosen status code

Run:

```bash
go run .
```

Optional:

- `MOCKDB_LISTEN_ADDR=:18080` to listen on a different port
