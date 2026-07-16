# server/ — Go backend

Flat `package main`, stdlib `net/http` only, Mongo driver v2. Files:

- `main.go` — env config, startup, middleware chain, `EnsureIndexes`
- `handlers.go` — `routes()` + all HTTP handlers (thin JSON shells over Store)
- `middleware.go` — `requestLog → jsonGuard → sessionLoad → mustChangePasswordGate`;
  `requireAuth` / `requireAdmin` wrappers; `ctxUser` + `userFromContext`
- `auth.go` — login/logout/password endpoints, bcrypt, session issue
- `store.go` — ALL Mongo access + business rules (~1400 lines); `Task`/`Team`/`Member` structs at top
- `period.go` — pure date/period math (ISO weeks, `currentPeriod`, `datesInWeek`, `isoWeekMonday`)
- `recur.go` — recurrence math backing `Materialize`

## Patterns to copy (don't invent new ones)

- New endpoint: register in `routes()` with `requireAuth`/`requireAdmin`, thin
  handler, logic in a Store method.
- Team scoping inside Store methods (see `TeamBoard`):
  `if u := userFromContext(ctx); u != nil && u.SystemRole != RoleAdmin && !containsID(u.TeamIDs, teamID) { return forbiddenErr(...) }`
  Task-level scope: `canAccessTask` / `taskScopeFilter`.
- Reads go through `findViews(filter)` → `[]TaskView`; `toView` computes
  `progress` per task (known N+1, accepted).
- New indexes go in `EnsureIndexes`. Existing on tasks: `{horizon,period}`,
  `{dueDate}`, `{parentId}`, `{teamId,assigneeId}`, `{ownerId}`, unique partial
  `{seriesId,period}`, text `{title,notes}`.

## Invariants (violating these breaks things silently)

- `createdAt` is set once in `CreateTask` and never patchable.
- `completedAt` is set ONLY on the transition into `done` — cancelled tasks have
  no `completedAt`. Query "completed" as `status ∈ {done, cancelled}`.
- `ownerId` is system-managed: set iff `teamId` is nil. A non-ADMIN may never
  flip a task's `teamId` nil-ness.
- `Recurrence.Anchor` is server-managed; `PatchTask` re-anchors only on
  freq/interval change. A raw `UpdateOne` on `period` desyncs recurrence
  (`Reschedule` already has this wart — don't spread it). `Materialize` (runs at
  the top of every read path) ends a series ONLY when `recurrence == nil`;
  cancelling an instance does NOT stop the series.
- `dueDate`/`period` are zero-padded strings — string comparison is safe and is
  the existing idiom for range filters (see `ViewAttention`).
- Timezone: `time.Local` for all period math; Mongo stores UTC instants.
  Week = ISO-8601, Monday start.

## Tests

Real-Mongo harness in `testutil_test.go` (scratch DB per test, dropped in
cleanup); `testServer`/`jsonClient` wrap the real middleware chain. Pure math
covered in `period_test.go`/`recur_test.go`. `make test` needs Mongo up.
