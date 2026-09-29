# FSM Library (copied from pbh_ipd_service)

**Provenance:** copied from `pbh_ipd_service` `internal/fsm` @ `main`
commit `b6f94dbb5` on 2026-09-29 (wave 2.1 of the taskman Mongo→Postgres
migration, `docs/DESIGN_PG_FSM_MIGRATION.md`). Source module: `pb_hospitals`.

This is a **copy, not a dependency**. Bugs found here get fixed here,
independently of ipd's copy — there is no shared module or vendoring
relationship. If ipd fixes a bug in its FSM engine, someone has to notice
and port the fix by hand.

## Decouplings from ipd's copy

Only two code changes were made beyond `pb_hospitals/internal/fsm/... ->
taskman/internal/fsm/...` import-path rewrites (verified diff: import
paths + exactly these two, nothing else):

1. `core/interfaces.go` — `ErrPermanent` was `= bgtask.ErrPermanent`
   (aliased to ipd's `internal/bgtask` sentinel). taskman has no bgtask
   package, so it is now a local sentinel: `errors.New("fsm: permanent
   failure")`.
2. `core/manager.go` — `bitbucket.org/pbhealthjira/go/log` (zerolog-backed,
   pbh-internal) was replaced with stdlib `log/slog` at all 3 call sites
   (`slog.ErrorContext` / `slog.DebugContext`), since taskman has no
   dependency on pbh's internal log wrapper.

## Dropped packages

- `outbox/` — Kafka ADT-event publishing. taskman has no Kafka.
- `postcommit/` — durable background-task bridge (`bgtask`). taskman has no
  bgtask/background-task-table equivalent.
- ipd's own `README.md`, `admission_transitions_test.go`,
  `m_bed_transitions_test.go` — these validate ipd's own YAMLs
  (`admission.yaml`, `bed.yaml`, `discharge.yaml`, `m_bed.yaml`,
  `transfer.yaml`), which are not part of this copy.

The `Manager` struct still *declares* `PostCommitRegistry`,
`PostCommitRepo`, `OutboxNotifier`, and `AggregateType` fields (interfaces
defined in `core/interfaces.go` itself, not in the dropped packages), and
`ExecuteTransition` still calls `persistPostCommitActions` and fires
`NotifyOutbox()`. None of that is a problem: those fields are optional and
left `nil` by any taskman caller, so the corresponding code paths are
no-ops. There is simply no `postcommit`/`outbox` implementation in this
tree to plug into them.

## `state_history` — present but deliberately unwired

`core/state_history.go` and `core/state_history_repository.go` were
copied verbatim (they have no ipd-specific coupling — plain gorm model +
repository). They are **not wired into any taskman DB or DI** by this
wave. Per `docs/DESIGN_PG_FSM_MIGRATION.md`: taskman's `task_activity`
table is the single audit sink for status changes (`WriteAudit` hook, wave
2.2) — ipd's separate `state_history` table (used there for skip-detection
against a happy-path timeline) is intentionally **not** created as a
second log. `Manager.StateHistoryRepo` stays `nil`; `writeSkippedStates`
is a no-op without a timeline (`GetHappyPathTemplate` returns nil for
`task.yaml`, which defines no `timeline:` section) and without a repo.

If a future wave wants skip-tracking, wiring it means: add a
`state_history` table + Flyway-equivalent migration, construct a
`NewStateHistoryRepository(db)`, and set `Manager.StateHistoryRepo`. Until
then this code is inert dead weight kept for parity with ipd, not a stub
that other tests should exercise.

## What wave 2.2 needs to know before wiring hooks

- **Nothing here assumes a specific `Status`/`Substatus` vocabulary** —
  `core` is generic string types. `transitions/task.yaml` (already
  authored, not part of this copy's diff) defines taskman's own 4 states
  (`todo`, `in_progress`, `done`, `cancelled`) and 12 permissive pairs —
  see that file's header comment for the full event map.
- `loader.NewFromFS("task.yaml")` is the only loader entry point this copy
  needs; `admission.yaml` / `bed.yaml` / `discharge.yaml` / `m_bed.yaml` /
  `transfer.yaml` do not exist in this tree — only `task.yaml` is embedded
  (`transitions/embed.go`'s `//go:embed *.yaml` picks up whatever `.yaml`
  files live in `transitions/`, which is just this one).
- `guards.Evaluator` (`guards/guards.go`) is a plain registry — currently
  nothing registers a guard, and `task.yaml`'s `guard: ""` on every row
  means `Manager.Guards` can stay `nil` at launch (locked decision #1,
  permissive launch).
- `statestore.InMemoryStore` (`statestore/mem_store.go`) is test/dev-only.
  Wave 2.2's real `StateStore` will need to read the task's current
  `status` from Postgres — likely backed by the same `*gorm.DB`/tx the
  patch transaction already holds (see `Manager.getState`'s `txStateStore`
  optional-interface pattern for read-your-own-writes inside a tx).
- `ExecuteTransition` runs entirely inside one `db.Transaction(...)` call;
  if wave 2.2 calls it from inside an already-open tx (savepoint), the
  `NotifyOutbox()`/outbox-wake path is skipped automatically
  (`inExistingTx`) — irrelevant here since `OutboxNotifier` is unset, but
  worth knowing if that ever changes.
- `ErrPermanent` (`core/interfaces.go`) is now a plain local sentinel with
  no retry-queue semantics attached (no bgtask worker exists to dead-letter
  against) — it is unused connective tissue until/unless a retry queue is
  ever built for taskman.
