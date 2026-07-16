# v6 — Week-scoped team board, rollover & history (contract)

Decided 2026-07-16. Scope: **team views only** — personal Day/Week/Month, All
Tasks, Attention, and Search are untouched.

User decisions (locked):
1. Week membership is an explicit server-managed **`weekOf`** field, not a
   computed createdAt/dueDate rule.
2. Open tasks **with a dueDate stay visible every week** until closed — the
   week filter only expires *undated* open tasks and completed ones.
3. Rollover is **ADMIN-only**, manual (banner entry point; no cron, no
   auto-prompt).
4. History is **per-team**, newest-first by `createdAt`, "Load more"
   (offset/limit) pagination.

---

## Data model

New Task field: `weekOf` (`bson/json "weekOf,omitempty"`, string `"YYYY-Www"`,
ISO-8601 week, machine-local TZ like all period math). Present **iff the task
is a team task** (`teamId` set). Rules:

- **Create** (team task): `weekOf = isoWeek(now)`.
- **Transition into terminal** (`done`/`cancelled`, create or patch):
  `weekOf = isoWeek(now)`, unconditionally. Semantics: *weekOf is the week the
  task last mattered* — a task finished this week shows in this week's
  completed fold even if it was created earlier. (Transitions back out of
  terminal leave `weekOf` as-is.)
- **PATCH `weekOf`**: ADMIN-only (403 otherwise), value must be a valid ISO
  week key. This is the rollover "move" primitive and its undo.
- **Personal→team flip** (ADMIN patch): set `weekOf = isoWeek(now)`, mirroring
  the existing `ownerId` re-derivation. Team→personal: unset it.
- **Materialize** (recurring spawns): new instances get `weekOf = isoWeek(now)`
  when `teamId` is set.
- **Backfill** (startup, idempotent, alongside `EnsureIndexes`): where
  `teamId` exists and `weekOf` is missing →
  `weekOf = isoWeek(completedAt ?? createdAt)`.

New index: `{teamId: 1, weekOf: 1, status: 1}`.

## Board visibility (`GET /api/teams/{id}/board` — modified)

Let `W = currentWeek()`.

| Task state | Visible on board? |
|---|---|
| Open, has `dueDate` (past, this week, or future) | **Always** (Overdue section when past — unchanged) |
| Open, no `dueDate` | Only if `weekOf == W` |
| Terminal (`done`/`cancelled`) | Only if `weekOf == W` (inside the completed fold) |

Response additions: `week: "YYYY-Www"` and `staleOpen: <n>` (count of
open ∧ undated ∧ `weekOf < W`; one extra `CountDocuments`). The UI shows the
rollover banner from `staleOpen` only to ADMINs.

Accepted consequences (deliberate, don't "fix"):
- Parent `progress` counts still cover **all** children, including ones
  filtered off the board (existing unscoped `progress()` behavior).
- Subtasks stay independent rows — a visible child's parent may be hidden.
- Hidden stale tasks remain discoverable via Search / All Tasks / Attention
  (their scoping is untouched) — that's the escape hatch, not a leak.

## Rollover (ADMIN-only)

- **Entry**: board banner "N tasks from past weeks — Review" when
  `staleOpen > 0`, rendered only for ADMIN.
- **`GET /api/teams/{id}/rollover`** — `requireAdmin` (no team-membership
  check needed beyond that; admins see all teams). Returns
  `{ week, tasks: [TaskView] }` = open ∧ undated ∧ `weekOf < W`, sorted
  `weekOf` asc, then `createdAt` asc.
- **Actions** — client-side `Promise.all` of per-task PATCH (existing bulk
  convention), each with snapshot-undo toast:
  - *Move to this week* → `PATCH {weekOf: W}` — **skips recurring instances**
    (`recurrence != nil || seriesId != nil`); toast reports "moved N, skipped
    M recurring". Their period is recurrence-owned; moving desyncs the anchor.
  - *Mark done* → `PATCH {status: "done"}`
  - *Cancel* → `PATCH {status: "cancelled"}` — terminalizing a recurring
    instance does NOT end its series (existing app-wide semantics).
- Recurring instances are badged in the list so the skip is legible.

## History (per-team, paginated — first pagination in the codebase)

- **`GET /api/teams/{id}/history?offset=0&limit=50`** — `requireAuth` + the
  same team scoping as the board (ADMIN any team, USER own teams). Filter:
  `teamId` ∧ `status ∈ {done, cancelled}` ∧ `weekOf < W`. Sort `createdAt`
  **desc**. `limit` default 50, max 200. Response `{ tasks, hasMore }`
  (fetch `limit+1`, return `limit`).
- **Entry**: "View older →" footer link on the team board's completed fold.
- Current-week completions live in the fold; history is strictly past weeks.

## Endpoint × role matrix additions (append to docs/AUTH_FEATURES.md at build)

| Endpoint | ADMIN | USER |
|---|---|---|
| `GET /api/teams/{id}/rollover` | any team | 403 |
| `PATCH /api/tasks/{id}` with `weekOf` | ✓ | 403 |
| `GET /api/teams/{id}/history` | any team | own teams |

## UI changes (build phase)

- `TeamPage.tsx` gains a view-local state `view: "board" | "rollover" |
  "history"` (same pattern as the members panel — no App.tsx routing change).
- Board: week label in header, ADMIN banner, fold footer link.
- Rollover + History are new screens inside the team page main column —
  mockups per `DESIGN_PROMPT_WEEK_ROLLOVER.md`.
- `api.ts`: `teamRollover(teamId)`, `teamHistory(teamId, offset, limit)`,
  `updateTask` already covers the PATCHes.
