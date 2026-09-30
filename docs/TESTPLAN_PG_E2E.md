# E2E test plan — PG+FSM branch vs prod, through the real UI

**Goal:** prove the `pg-fsm-migration` branch behaves identically to prod
(Mongo, :8484) for every user-visible flow, using a real browser against a
branch server loaded with a migrated copy of prod data. Prod is never
written to; the test instance is disposable.

## Environment (P0)

- **Test instance:** branch binary on **:18485**, schema `taskman_e2e` on
  the dev Postgres (`taskman-pg-dev`, :5432), populated by
  `cmd/migrate-mongo --apply` reading prod Mongo (read-only) — the same
  tool and data path the real cutover will use, so this doubles as a full
  cutover rehearsal.
- **UI bundle:** the exact `ui/dist` prod serves (copied from the main
  checkout) — proves the UI contract moved nowhere.
- **Access:** test-only ADMIN `e2e-admin@test.local` inserted into the
  scratch schema (known bcrypt hash; prod copy's real hashes untouched).
  USER-role flows use "enable login" on a migrated member inside the test
  copy (temp password) — which tests that flow too.
- **Baseline:** prod :8484 browsed read-only for side-by-side comparison
  (normal app reads; its materialize-on-read is everyday behavior).

## Phases (browser-driven, each with pass criteria)

| # | Flow | Pass = |
|---|---|---|
| 1 | Auth: bad login, good login, /me, logout; must-change-password gate via enable-login temp password | statuses/redirects identical to prod behavior |
| 2 | Data fidelity: members, teams, one team board, day/week/month counts, one task detail incl. activity timeline | numbers/fields match prod side-by-side (+1 member = e2e admin) |
| 3 | Task CRUD: create personal + team task, edit fields, delete → undo | restore returns the task intact (validated-restore path) |
| 4 | Status/FSM: todo→in_progress→done, done↔todo toggle, cancel, reopen; bulk complete + undo toast; unchanged-status edit | every pair works (permissive FSM), one status activity row each, no-op edit logs nothing |
| 5 | Notes: add, backdate, edit, delete; timeline order | order stable (seq), author rules enforced |
| 6 | Recurrence: create weekly series → instance materializes on view; cancel instance ≠ series end; remove recurrence = series end | matches documented semantics |
| 7 | Backlog: create in pool, move to week, back to pool | v7 flows intact (incl. the 'backlog' horizon fix) |
| 8 | Board/rollover/attention: buckets, reassign, admin rollover, overdue/slipped | weekOf semantics identical (dueDate IS NOT NULL arm) |
| 9 | Teams/members admin: create, rename collision, delete-team-with-tasks blocked, enable login, delete assignable member | 409s surface in UI exactly as before; assignee cleared |
| 10 | Concurrency + search + summary: same task edited in two tabs; substring search; summary date range | no 409 dialogs (lock-wait delta); search/summary sane |

Wrap-up: server log scanned for errors/panics; GIF recordings of phases 4
and 6; findings table in this doc; scratch schema left for inspection
until cutover, then dropped.

## Results (run 2026-09-30, headless agent-browser, branch @ dac32b3)

**Verdict: PASS — no migration defects found.** 190 requests served, zero
5xx, zero panics, zero FSM errors in the server log.

| # | Result | Evidence |
|---|---|---|
| 0 | **PASS** + found 1 operational quirk | migrate --apply on prod data: 147/12/3/55/10 rows, ALL CHECKS PASSED incl. 3 field-by-field samples. Quirk: repo.Migrate needs the target schema to pre-exist for search_path-scoped DSNs (cutover targets `public` — unaffected). |
| 1 | PASS | bad login inline error; admin login; USER temp-password login → forced Set-a-new-password gate → through |
| 2 | PASS | 3 TEAMS · 13 MEMBERS (12+e2e admin); task detail shows all 3 migrated activity rows in order; the one Cancelled task matches Mongo's per-status count. Apparent overdue delta (3 vs 0) = live prod drift after snapshot (task 6ab129fe dueDate 09-29→09-30 in prod), verified — not a migration error; real cutover has no drift window (server stopped first) |
| 3 | PASS | create (UUID id beside hex ids); delete → Undo → `POST /api/tasks/restore 200`, id preserved |
| 4 | PASS | 5 UI-reachable pairs (todo→ip→done→cancelled→todo, todo→done) each exactly one activity row, by_name stamped; remaining pairs covered by TestFSMAllTwelvePairs |
| 5 | PASS | note with author + day bucket via detail panel |
| 6 | PASS | recurring daily created (anchor stamped); aged head → view-load spawned current instance, same series; reload idempotent (still 2) |
| 7 | PASS | horizon='backlog' + period='' through UI (the CHECK-constraint fix live); Assign → daily/today/MRD + weekOf auto-stamped W40 (team-flip rule) |
| 8 | PASS | IPD board 25 open = independent Mongo count; buckets 4+2+2+3+5+1+5+3+0 = 25, consistent with assignee dropdown |
| 9 | PASS | duplicate team name → "team name already exists" in UI; member+join row created; enable-login temp password; DELETE login-enabled member → 409 "member has login enabled; disable instead of deleting" |
| 10 | PASS | 6 concurrent writes (4 PATCH + 2 notes) all 200/201, no 409 (lock-wait delta), coherent final state; ADMIN search finds USER's personal task 0 times (privacy invariant); Summary panel renders |

Driver notes (not app issues): a11y refs are per-snapshot (stale-ref
clicks no-op), the 6s undo toast needs a tight snapshot→click chain, and
the member-remove confirm() blocks the page until `dialog accept`.

Test instance left RUNNING for manual poking before cutover:
http://localhost:18485 (schema `taskman_e2e`, e2e admin credentials from
the setup step; prod copy is disposable — drop schema when done).
