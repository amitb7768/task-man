# internal/httpapi — HTTP layer

Stdlib `net/http` only. No business rules here: handlers decode, call one
`*service.Service` method (aliased `Store` in `types.go`), encode.

- `Handler(store, static)` — the one entry point `cmd/taskman` uses: builds
  the `api`, mounts `static` (the SPA) on `/` unwrapped, and applies the
  global chain `requestLog → jsonGuard → sessionLoad → mustChangePasswordGate`
  around the mux. Tests' `testServer` builds the same chain.
- `handlers.go` — `routes()` (every endpoint, each wrapped `requireAuth` /
  `requireAdmin` per `docs/AUTH_FEATURES.md`) + all handlers. Ids are opaque
  strings; an unknown id is the service's 404, never a handler 400.
  List endpoints (views day/week/month/attention, search, backlog, team
  board/history) take a repeatable `?tag=` AND-filter parsed by `queryTags`
  (normalised; invalid → 400). `GET /api/tags?teamId=&status=open|closed` (requireAuth;
  status defaults to open, other values 400) → `{"tags":[{tag,count}]}` —
  the filter bar's known-tag source (`docs/DESIGN_V10_TAGS.md`).
- `middleware.go` — the chain + `requireAuth`/`requireAdmin`; the caller is a
  `*model.CtxUser` stored via `model.WithUser` (the key the service reads).
- `auth.go` — login/logout/me/change-password, session cookie, login backoff
  (`loginLimiter`). Sessions/bcrypt live in the service.
- `types.go` — aliases onto `internal/model`, handler-level `apiError`
  (bad JSON, auth gates, content-type), `writeErr` mapping `apiError` and
  `*service.APIError` to status + `{"error": msg}`; anything else → 500.

New endpoint: register in `routes()` with its auth wrapper, thin handler,
logic in a Service method.

Tests: end-to-end over the real chain against a scratch Postgres schema
(`testutil_test.go`: `newTestAPI`, `testServer`, `jsonClient`,
`direct{Member,Team,Task}` row seeders). Skip without `TASKMAN_TEST_PG_DSN`.
