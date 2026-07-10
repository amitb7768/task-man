# taskman — Backend Architecture Review

Scope: the Go backend under `server/` (single `package main`). Report only — no code changed.
Reviewed against DESIGN.md (v1), DESIGN_V2_UI.md, DESIGN_V3_TEAMS.md, PROGRESS.md.
Driving question: how far is this code from cleanly accepting **basic auth + admin/member roles**,
and what minimal prep buys that without a rewrite.

---

## 1. Current State

Five files, one package, ~2765 lines. Clean separation by concern already exists:

```
                         HTTP (net/http, Go 1.22 method-pattern ServeMux, :8484)
   client / SPA ──▶  main.go            entry: env config, Mongo connect, EnsureIndexes,
                     │                   spaHandler static serve, ListenAndServe
                     ▼
                     handlers.go         type api{store}; routes(); one method per endpoint.
                     │                   decode JSON ▶ call store ▶ writeJSON / writeErr.
                     │                   NO middleware. NO auth. NO request logging.
                     ▼
                     store.go            type Store{client,db,tasks,teams,members}.
                     │  (1130 loc)       data model + validateTaskFields + ALL queries +
                     │                   Materialize + views + search + board + counts.
                     ├──▶ period.go       PURE: ISO week/month math, currentPeriod, between()
                     └──▶ recur.go        PURE: occurrence rule, anchor, missingPeriods
                     ▼
                  MongoDB (mongo:7 in docker-compose, db "taskman", 3 collections)
```

- **Request flow**: `ServeMux` (method+path patterns, `{id}` via `PathValue`) → handler on `*api`
  → `json.Decode` → `a.store.X(r.Context(), …)` → `writeJSON`. Uniform, no framework. Good.
- **Error handling**: `apiError{status,msg}` sentinel (store.go:32) + `badRequest/notFoundErr/
  conflictErr` constructors; `writeErr` (handlers.go:64) unwraps via `errors.As`, default 500 with
  a `log.Printf`. This taxonomy is clean and consistent — keep it.
- **Validation placement**: `validateTaskFields` (store.go:175) mixes structural checks (title,
  horizon, period format) with **referential** checks that hit Mongo (parent/team/assignee exist,
  assignee-in-team, cycle walk store.go:214). It lives in the store, runs on create and re-runs on
  patch (store.go:460). Correct for now but store-bound.
- **Store patterns**: one concrete `Store`, no interface. `toView` (store.go:359) issues **one
  `Find` per task** to compute child progress → N+1 on every list. `findPersonal` injects the
  `teamId $exists:false` personal filter (store.go:393).
- **Materialization**: `Materialize` (store.go:641) runs at the top of *every* read path (views,
  attention, search, board, team counts). It loads **all** series docs, finds latest per series in
  memory, spawns missing occurrences, dedup via the unique `{seriesId,period}` index.
- **Index management**: `EnsureIndexes` (store.go:143) at startup: tasks `{horizon,period}`,
  `{dueDate}`, `{parentId}`, `{teamId,assigneeId}`, unique-partial `{seriesId,period}`,
  text `{title,notes}`; teams unique `{name}`. No `members` indexes.
- **Test coverage shape**: pure functions only — `period_test.go` (12 tests) + `recur_test.go`
  (15 tests), table-driven, edge-covered (year rollover, interval, in-place-unmarshal aliasing).
  **Zero** tests touch `Store` or handlers (no `httptest`, no fake Mongo). The riskiest code —
  validation, cascade delete, patch-merge, materialization writes, board — is unexercised.
- **Config**: env only (`MONGO_URI`, `PORT`) via `envOr`. Fine.
- **Lifecycle**: `http.ListenAndServe` directly (main.go:42) — no `http.Server`, no signal handling;
  `defer store.Close` never runs.

---

## 2. What's Good (do NOT change)

- **Flat `package main` is correct for this size.** go.dev/doc/modules/layout says a single-binary
  project keeps code in one package until a second consumer appears; do not cargo-cult
  cmd/internal/hexagonal layers onto a 2.7k-line local tool.
- Pure period/recurrence math isolated and thoroughly tested — the hard logic is the tested logic.
- `apiError` + `writeErr` error taxonomy: one obvious place to add codes, already status-aware.
- Handlers are genuinely thin — decode, call, encode. This is exactly what makes the auth seam cheap.
- Store method signatures already take `ctx context.Context` first — the plumbing channel for a
  request-scoped user identity is **already present**; nothing to re-thread.
- `orEmpty` null-safety and the `"" vs null` clearing convention are consistent with the wire contract.

---

## 3. Gaps (ranked)

| # | Gap | Evidence | Severity | Blocks auth? |
|---|-----|----------|----------|--------------|
| G1 | **No middleware seam.** `routes()` returns a bare mux; handlers registered directly, no wrap point. | handlers.go:17-46 | High (for auth) | Yes |
| G2 | **No user/identity model.** members are records with no credentials; tasks have no `ownerId`; personal views assume one operator (`teamId:null` = "Amit's"). | store.go:51-100, findPersonal store.go:393 | High (for auth) | Yes |
| G3 | **Store + handlers untested.** Only pure funcs tested; validation/cascade/patch/materialize have no harness. Adding auth without a test seam compounds this. | no `Store`/`httptest` refs in `*_test.go` | High | Partly |
| G4 | **N+1 progress reads.** `toView` runs a `Find` per task; a 50-task view = 51 queries + a full-series materialize scan. | store.go:337-365, 641 | Med (scale) | No |
| G5 | **Missing indexes for v3 query patterns.** `members.teamIds` (used by `memberCount` count + `ListMembers`) and tasks `{teamId,status,dueDate}` (open/overdue counts) unindexed → collection scans, N-teams × scan in the landing page. | store.go:913-930, 1016; EnsureIndexes 143 | Med | No |
| G6 | **No graceful shutdown.** `ListenAndServe` blocks; SIGTERM kills abruptly; `store.Close` deferred but unreachable. | main.go:42 | Low | No |
| G7 | **No observability.** No request logging/metrics; only 500s log. Once auth exists you'll want auth-failure/audit logs. | writeErr handlers.go:70 | Low | No |
| G8 | **`restore` bypasses all validation by design.** Accepts arbitrary client JSON as `Task` with client-chosen `_id`. Safe at localhost-single-op; under multi-user it is a write-anything injection vector. | store.go:582, DESIGN_V2 §3 | Low now / High post-auth | No (flag) |
| G9 | **PATCH is read-modify-`ReplaceOne`, last-write-wins.** No version/optimistic check. | store.go:428-490 | Low (single-op) | No |
| G10 | **Multi-doc ops non-transactional** (cascade-delete read→DeleteMany; restore InsertMany). Standalone Mongo has no txns anyway (needs replica set). | store.go:531-608 | Low at this scale | No |

---

## 4. Auth & Permissions Design Sketch

**Verdict: the code is close — a seam-add, not a rewrite.** Handlers are thin, `ctx` already flows
to the store, and the error taxonomy already returns 4xx cleanly. Two real prerequisites: a
middleware seam (G1) and a user model (G2). Password hashing needs **no new dependency** —
`golang.org/x/crypto` v0.33.0 is already in the tree (bcrypt lives there).

### Data model
Add a `users` collection; keep `members` as the domain/assignment entity (task.assigneeId → member
stays untouched). **Link, don't merge** — merging would destabilize the assignee foreign key.

```
users:  _id, username (unique), passwordHash (bcrypt), role "admin"|"member",
        memberId ObjectId? (links to members._id for a logged-in member), createdAt
sessions: _id (random 32B token), userId, expiresAt   // TTL index {expiresAt:1} expireAfterSeconds:0
```
- **Session over JWT.** For a localhost/LAN single-binary tool JWT buys nothing (no stateless scale,
  no third party) and costs key mgmt + revocation pain. Use an opaque random token in an
  **HttpOnly, SameSite=Lax cookie**, session doc in Mongo with a TTL index. Logout = delete the doc.
- **`ownerId` on tasks** is the one data-model addition needed for *personal* per-user views. Near
  term you can defer it (members log in to see **team** tasks scoped by membership; personal
  Day/Week/Month stay admin-only). Add `ownerId` when members need their own personal planning.
- Seed one admin (Amit) from env (`ADMIN_USER`, `ADMIN_PASSWORD`) on first boot.

### Middleware
```
requestLogger → sessionAuth → (per-route) requireAdmin / requireMember
```
`sessionAuth` reads the cookie, loads the session+user, stores the user in `r.Context()` (new
`ctxUser` key). Wrap the whole mux for auth/logging; gate individual routes with a tiny
`a.admin(handler)` wrapper that 403s non-admins. Store methods that must scope results read the user
from ctx — the ctx channel already exists.

### Endpoint × role matrix  (✓ allowed · ▲ scoped to caller · — denied)
| Endpoint | unauth | member | admin |
|---|---|---|---|
| POST /api/auth/login | ✓ | ✓ | ✓ |
| POST /api/auth/logout · GET /api/auth/me | — | ✓ | ✓ |
| POST /api/teams | — | — | ✓ |
| GET /api/teams | — | ▲ own teams | ✓ all |
| PATCH /api/teams/{id}  (rename) | — | — | ✓ |
| DELETE /api/teams/{id} | — | — | ✓ |
| GET /api/teams/{id}/board | — | ▲ if in team | ✓ |
| POST /api/members | — | — | ✓ |
| GET /api/members | — | ▲ own teams | ✓ |
| PATCH /api/members/{id} | — | ▲ self only | ✓ |
| DELETE /api/members/{id} | — | — | ✓ |
| POST /api/tasks | — | ▲ own/team | ✓ |
| GET/PATCH /api/tasks/{id} | — | ▲ owner/assignee/team | ✓ |
| DELETE /api/tasks/{id} | — | ▲ owner | ✓ |
| POST /api/tasks/restore | — | ▲ owner (revalidate!) | ✓ |
| POST /api/tasks/reschedule | — | ▲ own | ✓ |
| GET /api/views/day·week·month | — | ▲ own (needs ownerId) | ✓ |
| GET /api/views/attention · GET /api/search | — | ▲ own+team | ✓ |

Minimal `/api/auth/*`: **login / logout / me**. Nothing more for MVP.

---

## 5. Phased Fix Plan

Effort S/M/L · Risk-if-skipped · Blocks-auth.

### Phase 0 — prep NOW (zero-cost habits during in-flight Composer/UI work)
- **P0.1** Keep handlers thin; route all new work (Composer) through existing `POST /api/tasks` — no
  new endpoints that would later need re-gating. *(S · low · —)* — already the plan per PROGRESS.
- **P0.2** When adding any create/write path, keep the `apiError` constructors; never write raw
  `http.Error`. *(S · low · —)*
- **P0.3** Mentally reserve `ownerId` on tasks — don't build UI that assumes "all personal tasks are
  everyone's". *(S · med · helps auth)*

### Phase 1 — structural prep (do before auth; small)
- **P1.1** Add `middleware.go` + a `chain`/wrap in `routes()`; ship a **requestLogger** now to prove
  the seam (also fixes G7). *(S · — · **unblocks auth**)*
- **P1.2** Introduce a narrow `store` **interface** consumed by `api` (methods it already calls), so
  handlers + future middleware get a fake for tests (fixes G3 enabler). *(M · high · enables auth tests)*
- **P1.3** Add first `httptest` handler tests against the fake store (create/patch/delete happy+400).
  *(M · high · —)*
- **P1.4** Graceful shutdown: `http.Server` + `signal.NotifyContext` + `Shutdown` (fixes G6). *(S · low · —)*
- **P1.5** Add missing indexes: `members{teamIds:1}`, tasks `{teamId,status,dueDate}` (fixes G5). *(S · med · —)*

Optional/skip: relocating `server/` → `cmd/taskman/` is pure convention and buys nothing here — skip.

### Phase 2 — auth + roles
- **P2.1** `users` + `sessions` collections, TTL index; bcrypt (already-present dep); env-seeded admin. *(M · — · —)*
- **P2.2** `auth.go`: `sessionAuth` middleware (cookie→session→user→ctx) + `requireAdmin`/`requireMember`
  wrappers; `POST /api/auth/login|logout`, `GET /api/auth/me`. *(M · — · —)*
- **P2.3** Gate admin endpoints per the matrix (teams create/rename/delete, members create/delete). *(S · — · —)*
- **P2.4** Scope reads to the caller: `GET /api/teams` and board/search/attention filter by
  membership; store methods read user from ctx. *(M · — · —)*
- **P2.5** **Revalidate `restore`** and reject client-supplied ids not owned by the caller (closes G8). *(S · high post-auth · —)*
- **P2.6** UI gating points: hide team create/rename/delete + member add/delete for members; add a
  login screen + `me`-driven role check; 401 → redirect to login. *(M · — · —)*
- **P2.7** *(when members need personal planning)* add `ownerId` to tasks + set on create + filter
  personal views by owner. *(M · med · —)* — defer if members only need team visibility first.

### Phase 3 — hardening (worth it at this scale, not before)
- **P3.1** Kill N+1 progress: one grouped count over `parentId $in [ids]` per list instead of per-task
  Find (G4). *(M · low · —)*
- **P3.2** Gate materialization behind a per-horizon "last materialized" marker or a background ticker
  so every read doesn't rescan+write. *(M · low · —)*
- **P3.3** Optimistic concurrency on PATCH (`updatedAt` precondition) — only once >1 user writes (G9). *(S · low · —)*

---

## 6. Explicit Non-Goals (over-engineering traps for this scale — do NOT do)

- **No hexagonal / clean-architecture layering, no `internal/` domain packages.** One binary, one
  operator (soon a handful of members). File-level separation in `package main` is the ceiling.
- **No JWT / OAuth / external IdP.** Opaque cookie sessions in Mongo are simpler and revocable.
- **No RBAC framework / policy engine.** Two roles = two `if` wrappers. A permission matrix in code,
  not a rules DSL.
- **No Mongo transactions / replica-set conversion** to make cascade-delete atomic — the failure
  window is theoretical at single-operator concurrency; restore already covers the undo case.
- **No ORM, no service/repository/DTO ceremony, no DI container.** The concrete `Store` (behind one
  test interface) is enough.
- **No microservices / message queue / event sourcing / rate limiting / API versioning.** LAN tool.
- **No premature `ownerId` rollout** if the first auth milestone is only "members see their team's
  tasks" — add it when personal per-user planning is actually needed (P2.7).
