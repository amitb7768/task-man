# Design: Mongo → Postgres migration + FSM + backend layering

**Status:** approved plan, pre-build (2026-09-28). Decisions grilled and locked with Amit.
**Scope:** replace the Mongo repo layer with Postgres, route all task status changes through the ipd FSM engine (copied), restructure the backend into layered packages. UI contract unchanged.

## Locked decisions

| # | Decision | Choice |
|---|---|---|
| 1 | FSM policy | **Permissive at launch**: all 12 status pairs legal under semantic events — zero UI behavior change (undo toasts, done↔todo toggles keep working). Tightening later = YAML + guard edit. |
| 2 | Refactor depth | **Middle path**: `cmd/taskman` + `internal/{model,repo,service,httpapi,fsm}`. Authz moves repo→service; domain errors mapped to HTTP in handlers; typed `TaskFilter` replaces inline bson filters; graceful shutdown; N+1 fixes. No wire/protos/tenantdb/Flyway — pbh layering essence, not ceremony. |
| 3 | Integrity | **FKs enforced; restore validated.** `ON DELETE SET NULL` (assignee), `RESTRICT` (team w/ tasks, parent), `CASCADE` (activity, sessions). POST /api/tasks/restore rejects dangling refs with a clear error (closes the unvalidated-raw-JSON admin hole). |
| 4 | Cutover | No scheduling constraints; short stop-the-server window (prod = the LAN Mac, ~137 tasks). |

## Schema (Postgres 16; gorm v1.30.0 + driver v1.6.0, pbh pins)

- **tasks**: `id TEXT PK` (existing ObjectID hex migrates as-is; new ids = UUID — API treats ids as opaque). NULL encodes Mongo-absent: `due_date, week_of, team_id, owner_id, assignee_id, parent_id, series_id, completed_at, recurrence JSONB, notes`. `period TEXT NOT NULL DEFAULT ''` (backlog), `priority TEXT NOT NULL DEFAULT ''`, `horizon, status, title NOT NULL`, `created_at/updated_at TIMESTAMPTZ`. Partial unique `(series_id, period) WHERE series_id IS NOT NULL` + `ON CONFLICT DO NOTHING` = materialization idempotency, verbatim semantics.
- **task_activity**: embedded array → child table (`id TEXT PK, task_id FK CASCADE, kind, date, at, by_id, by_name, text, from_status, to_status, edited_at`). **Also the FSM audit sink** — `WriteAudit` writes status rows here in-tx. ipd's `state_history` table is deliberately NOT created (one log, not two); the copied history files stay unwired.
- **teams** (`name` unique), **members** (partial unique `lower(email) WHERE password_hash IS NOT NULL` — matches Mongo's login-enabled-only uniqueness), **member_teams** join (replaces `teamIds` array), **sessions** (code-side expiry check stays; opportunistic `DELETE expired` on login replaces the TTL index).
- Indexes: mirror Mongo's set + the two the arch review flagged missing (`member_teams(team_id)`, `tasks(team_id,status,due_date)`).
- Search: `ILIKE` over title/notes (137 rows; `$text` stemming semantics change accepted; tsvector is a later upgrade if felt).
- Period strings compare with `COLLATE "C"` where ranged.
- **Concurrency**: `patchTaskOnce`'s updatedAt-CAS + 8-retry loop (and its ms-truncation hazard) is replaced by `SELECT … FOR UPDATE` in one transaction. Status + activity append + derived fields commit atomically — the property Mongo gave via single-document writes, now via tx.
- Migrations: embedded SQL run at boot (golang-migrate as a library). Deviation from pbh Flyway is deliberate: no Java/Jenkins here, env-only config stays.

## FSM (copied from ipd `internal/fsm` @ feat/bed-occupied-at)

- Copy `core/, loader/, guards/, statestore/` (~1,100 LOC, 12 files) → `internal/fsm/`, import paths rewritten. Two decouplings: `ErrPermanent` → local sentinel (drops bgtask import); `go/log` → `slog` (3 call sites). Drop `postcommit/` and `outbox/` entirely. Provenance noted in the package README.
- `internal/fsm/transitions/task.yaml`: 4 states, all 12 pairs, semantic events (`start, complete, cancel, reopen, resume, uncancel, …`) + creation transitions unused for now. PATCH handler maps `(current→requested)` → event.
- Hooks (per machine, m_bed template): `ApplyStateChange` = status UPDATE (row already locked FOR UPDATE); `UpdateOtherFields` = today's completedAt/weekOf derivations moved verbatim; `WriteAudit` = task_activity status row; Validate/guards = nil at launch. Post-commit, outbox, state-history seams stay nil.
- Deliberately outside the FSM (today's no-log behavior preserved): create-with-status, materialized instances (spawn `todo`), restore replay.
- `ExecuteTransition` runs inside the patch tx (savepoint semantics built into the engine).

## Package layout

```
cmd/taskman/            main: config, migrate-at-boot, seed, http.Server + signal shutdown
internal/model/         Task, Member, Team, Session, ActivityEntry, Recurrence + status/horizon enums
internal/repo/          gorm PG repo behind interfaces; typed TaskFilter; scratch-schema test harness
internal/service/       domain logic from store.go (validation, derivation, scoping/authz, materialize, summary)
internal/httpapi/       handlers + middleware; domain-error → HTTP mapping; JSON patch merge (EqualFold weekOf quirk kept)
internal/fsm/...        the copy
cmd/migrate-mongo/      one-shot data migration (only place mongo-driver survives until retirement)
```

`period.go`/`recur.go` pure logic → `internal/model` (or `internal/period`); their 34 pure tests move untouched. 36 Mongo-backed tests → same-philosophy PG harness: unique scratch schema per test on the compose Postgres, dropped on cleanup.

## Data migration + cutover (P4)

`mongodump` archive → stop binary → `cmd/migrate-mongo` (4 collections; ids preserved as hex text; activity → rows; teamIds → join rows; sessions skipped — team re-logs-in; dangling refs reported and fixed/dropped by hand, expected ≈0) → start on PG → soak 1 week with Mongo volume untouched as rollback → retire container + driver dep. docker-compose: `mongo:7` → `postgres:16-alpine`.

## Behavior deltas (accepted, everything else byte-compatible)

1. Restore validates refs (was: raw unvalidated insert). 2. Search is ILIKE, not stemmed $text. 3. Deleting a member SET-NULLs assignments in-DB (same net effect). 4. Timestamps keep microseconds (CAS that depended on ms truncation is gone). 5. Concurrent patch conflicts wait on a row lock instead of 409-after-8-retries. 6. Ids are opaque strings end-to-end (P1 wave 1.2): a malformed path id is the repo's 404 (Mongo-era handlers 400'd on bad hex); garbage filter ids match nothing (200 + empty); reschedule silently skips unknown ids. 7. Email uniqueness among login-enabled members is now case-insensitive (lower(email) partial unique) — same-email-different-case create/enable 409s where Mongo allowed it. 8. Team deletion detaches its members' join rows (Mongo left dangling teamIds).

## Phases (each shippable; plan doc = this file)

1. **P1** schema + migrations + PG repo behind interfaces + test harness (behavior-identical; Mongo still live).
2. **P2** FSM copy + status changes routed through it (permissive YAML, zero behavior change).
3. **P3** package split, service/authz extraction, graceful shutdown, N+1 fixes (progress + team counts → GROUP BY).
4. **P4** migrate data, cut over, soak, retire Mongo.

Suggested alongside: nightly `pg_dump` cron (backup is currently a manual one-liner), session-expiry sweep on login.

## Execution plan (2026-09-29, orchestrated build)

Fable orchestrates; opus/sonnet subagents implement; Fable personally owns schema DDL, `task.yaml`, wave briefs, and the review gate after every wave. Prod (`:8484` binary + `taskman-mongo-1`) is untouched until P4 — all work is code + a new `taskman-postgres` compose service.

| Wave | What | Who | Gate (Fable) |
|---|---|---|---|
| 1.0 | Migration SQL (`internal/repo/migrations/0001_init.sql`) + compose `postgres:16-alpine` + golang-migrate boot wiring | **Fable writes DDL**, sonnet wires boot/compose | DDL applies clean on scratch PG; indexes/partial uniques verified via `\d` |
| 1.1a | Task repo: models, `TaskFilter`, CRUD, `patchTaskOnce`→`FOR UPDATE` tx, materialize `ON CONFLICT DO NOTHING`, summary | **opus** | Semantics diff vs store.go (absent↔NULL, idempotency, atomicity); contrast tests present |
| 1.1b | Member/Team/Session repos + authz-relevant queries + seed + restore validation | **opus** (parallel with 1.1a) | FK actions match locked decision #3; restore rejects dangling refs |
| 1.2 | 36 Mongo tests → scratch-schema-per-test PG harness; 34 pure tests moved untouched | sonnet | Full suite green `-race`; test intent preserved (spot-read 8–10 ports) |
| 2.1 | FSM copy: 12 files, import rewrite, ErrPermanent→local, go/log→slog, drop postcommit/outbox, provenance README | sonnet | Byte-diff vs ipd source = only the 2 decouplings + paths |
| 2.2 | **Fable writes `task.yaml`** + event map; opus wires hooks (ApplyStateChange/UpdateOtherFields/WriteAudit→task_activity) into patch tx | **opus** | All 12 pairs transition; activity rows identical to today's; savepoint path exercised |
| 3.1 | Package split `cmd/taskman` + `internal/*`, authz→service, error mapping, graceful shutdown, N+1→GROUP BY | **opus** | Build+suite green; no logic edits smuggled into the move; endpoint smoke on scratch PG |
| 4.1 | `cmd/migrate-mongo` + dry-run report against a `mongodump` copy (read-only vs prod) | **opus** | Row counts + ref-integrity report; ids preserved as hex |
| 4.2 | Cutover: stop → migrate → start on PG → soak 1wk → retire mongo | **user-gated, Fable drives live** | Rollback = untouched mongo volume |

Review gate = read the load-bearing files, run `go build ./... && go test ./... -race`, and check the wave's golden semantics before the next wave launches. Waves 1.1a/1.1b run in parallel; everything else is sequential.

**P1 completed 2026-09-29** — commits 087d57e (1.0), 599637a (1.1), ae1f31c (1.2): server fully on Postgres, Mongo store deleted, 35/36 server tests re-homed bodies-unchanged, mongo-driver out of go.mod. FSM source correction: ipd checkout moved to `main` — the copy takes `internal/fsm` from there (provenance recorded in the copied README).

**P2 completed 2026-09-29** — commits 657a186 (2.1 copy @ b6f94dbb5; NB: ipd internal/fsm is hand-rolled, looplab was never a dep) and the 2.2 wiring commit: every status change runs ExecuteTransition in the patch tx, task_activity is the WriteAudit sink, teeth test proves enforcement (restricted YAML → 409, no trace), zero API-visible change.
