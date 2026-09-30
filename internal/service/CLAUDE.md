# internal/service — business flows

`Service` (`service.go`) owns every business method: validation, derivation,
scoping/authz, transactions, materialization, summary. Business errors are
`*APIError{Status, Msg}` (`badRequest`/`notFoundErr`/`forbiddenErr`/…);
httpapi maps them. `internal/repo` is infra only (Open/Migrate/NewID + pure
query helpers such as `LoadMemberTeamIDs`, `LookupNames`) — anything with a
business rule or a business error stays here.

Files: `tasks.go` (create/patch/delete/restore/reschedule, loaders, scope,
progress), `task_views.go` (Materialize, day/week/month, attention, search,
team board/rollover/history), `task_notes.go`, `task_summary.go`,
`fsm.go` (status changes → `internal/fsm`), `teams.go`, `members.go`,
`sessions.go`, `auth.go` + `auth_password.go`, `seed_admin.go`.

## Patterns

- Authz checks that read a locked row stay INSIDE the tx, after the
  `FOR UPDATE` read (moving them out is a TOCTOU bug). Team scope:
  `forbidNonMember`; task scope: `canAccessTask` / `taskScope`.
- List reads: `listTasks` → `toViews` (progress = ONE grouped query per call,
  `progressFor`); never N+1 per row.
- `listTasks` Tasks carry no activity — never write one back.

## Invariants (violating these breaks things silently)

- `createdAt` is set once in `CreateTask`; `patchTaskTx` restores it.
- `completedAt` is set ONLY on the transition into `done` (cleared on leaving
  it) — cancelled tasks have none. "Completed" = `status ∈ {done, cancelled}`.
  Transition into terminal bumps a team task's `weekOf`.
- `ownerId` is system-managed: set iff `teamId` is nil. A non-ADMIN may never
  flip a task's `teamId` nil-ness.
- `Recurrence.Anchor` is server-managed; `patchTaskTx` re-anchors only on
  freq/interval change. `Reschedule` writes `period` raw without re-anchoring
  (known wart — don't spread it). `Materialize` (top of every read path) ends
  a series ONLY when the latest instance's `recurrence == nil`; cancelling an
  instance does not stop it. Idempotency = partial unique `(series_id, period)`
  + `ON CONFLICT DO NOTHING`.
- `dueDate`/`period` are zero-padded strings — string comparison is the range
  idiom (`COLLATE "C"` where ranged).
- Time: `time.Local` for period math; timestamps read back in UTC.

## Activity log (`task_activity` table)

- System-managed: never client-settable. `CreateTask` nils it; `patchTaskTx`
  nils it before `json.Unmarshal` and restores `orig.Activity` after.
- Status auto-log: a status change runs `transitionTaskStatus` (FSM) inside the
  patch tx after all validation; `WriteAudit` inserts the `kind:"status"` row,
  so a rejected patch leaves no trace. Create/Materialize/restore/Reschedule
  never log.
- Notes (`task_notes.go`): row insert/update/delete + `touchTask` bump of
  `updated_at`, in one tx; status entries are immutable; own-or-ADMIN edits.

## Tests

`testutil_test.go`: `newTestStore` = scratch schema per test on
`TASKMAN_TEST_PG_DSN` (skips when unset), `asUser` ctx. `newWithManager` is
the FSM test seam (restricted transition table). Fixtures: `tk*`
(task domain), `mt*` (member/team/session).
