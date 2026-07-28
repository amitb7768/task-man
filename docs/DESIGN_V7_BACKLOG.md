# v7 — Backlog (unstaffed planning pool) + Month-view daily rollup (contract)

Decided 2026-07-28. Two features, one build wave. No design-mockup round —
built directly from the existing design system (user decision; the only new
component is the Assign popover).

User decisions (locked):
1. Backlog task = sentinel **horizon `"backlog"`** with empty period — not a
   flag, not an optional-horizon loosening.
2. **Private per-ADMIN**: ownerId = creating admin (existing derivation
   untouched); endpoints `requireAdmin`; nav tab hidden for USERs.
3. **Hidden from All Tasks and Search by default**; SearchView's horizon
   filter gains an explicit "Backlog" option as the opt-in.
4. Assignment via an **Assign popover** (row hover + bulk bar): team →
   member (with open-task workload counts — the bandwidth signal) →
   optional due date.
5. Backlog items carry **title, notes, priority only** — dueDate and
   recurrence are rejected server-side (Attention leakage / Materialize
   breakage; see below).
6. List is **priority-grouped** (High/Med/Low, newest first within), no
   manual ordering.
7. Nav: **Plan group, after Month**, label "Backlog" (orchestrator call).

---

## Data model

`"backlog"` becomes a legal `horizon` value (`HorizonBacklog`):

- `validHorizon` accepts it; `validatePeriod` requires `period == ""` for it
  (any non-empty period → 400).
- New invariants enforced in `validateTaskFields` (final-state validation, so
  create AND patch are covered):
  - horizon `"backlog"` ⇒ `teamId == nil` (400 "backlog tasks are personal") —
    a team task can never sit on horizon backlog.
  - horizon `"backlog"` ⇒ `dueDate == ""` (400) — a dated backlog task would
    leak into Attention's overdue filter (it keys on dueDate only).
  - horizon `"backlog"` ⇒ `recurrence == nil` (400) — Materialize walks
    periods; backlog has none.
- `assigneeId` is already impossible pre-team (existing 400 "assigneeId
  requires teamId"). `weekOf` stays absent (teamId nil).
- **Assignment** is the existing personal→team flip, no new machinery:
  `PATCH {teamId, horizon:"daily", period:<today>, assigneeId?, dueDate?}`
  → server clears ownerId, stamps `weekOf = currentWeek` (v6 rule), validates
  assignee-belongs-to-team. The popover always sends horizon+period.
- Free (API-legal, not in UI scope): un-assigning a team task back to the
  pool via `PATCH {teamId:null, horizon:"backlog", period:""}` — the
  team→personal flip sets ownerId = patcher.

## Endpoints

- **`GET /api/backlog`** — `requireAdmin`. Returns `{ tasks: [TaskView] }` =
  `findPersonal({horizon:"backlog"})` (owner-scoped by findPersonal), sorted
  `createdAt` desc. Client does the priority grouping.
- **`Store.Search`**: when no horizon param is given, add
  `{"horizon": {"$ne": "backlog"}}`. An explicit `horizon=backlog` passes
  through the existing param — that's the Search opt-in. All Tasks (which
  calls search with no horizon) is thereby clean with zero UI change.
- No new write endpoints — bulk assign/delete is client `Promise.all` of
  per-task PATCH/DELETE with snapshot-undo toast (app-wide convention).
- Day/Week/Month/Attention need **no filter changes** — they key on the three
  real horizons and never match backlog (verified against each filter).

## Month-view daily rollup (separate enhancement, same wave)

Week view already rolls daily tasks up into per-day subsections — no change.
Month view gets the missing half, **server-only, ~10 lines in `ViewMonth`**:
in the existing per-week loop, widen the filter to

```
$or: [ {horizon:"weekly", period: w},
       {horizon:"daily",  period: {$in: datesInWeek(w) ∩ month}} ]
```

(month-clamp = `strings.HasPrefix(date, month)` — string idiom). Response
shape unchanged (`weeks: Record<week, TaskView[]>`); MonthView.tsx renders
the mixed list as-is; completed-fold counts recompute automatically.

Edge semantics (accepted): a month-spanning ISO week appears in both months
(pre-existing `weeksInMonth` behavior); a daily task inside such a week
appears only in the month its date belongs to.

## UI

- `App.tsx`: `NavKey` + `"backlog"`; Plan group entry after Month, rendered
  only when `user.systemRole === "ADMIN"` (first role-gated nav item; server
  enforces regardless).
- **`views/BacklogView.tsx`** (new): mono eyebrow + serif "Backlog" title,
  composer in a new `context="backlog"` mode (title + notes + priority pills
  only — no due/repeat/assignee), High/Med/Low sections (mono headers +
  hairline rules, All-Tasks anatomy), rows with status control + priority
  tick but no due chip/avatar, leading checkboxes, hover Assign…/delete,
  bulk bar "N selected · Assign… · Delete", CompletedFold at bottom
  (parked items can still be done/cancelled = discarded).
- **`components/AssignPopover.tsx`** (new): team `<select>` (`listTeams`) →
  member `<select>` from `teamBoard(teamId).members` showing
  "Name · N open" + an Unassigned option → optional `<input type="date">` →
  Assign. One PATCH per task; undo re-PATCHes
  `{teamId:null, horizon:"backlog", period:"", dueDate:""}` per snapshot.
- `api.ts`: `Horizon` type gains `"backlog"`, `backlog()` fetch,
  SearchView horizon `<select>` gains the Backlog option.

## Endpoint × role matrix additions (append to docs/AUTH_FEATURES.md at build)

| Endpoint | ADMIN | USER |
|---|---|---|
| `GET /api/backlog` | own backlog | 403 |
| `POST /api/tasks` with `horizon:"backlog"` | ✓ (personal) | ✓ but tab hidden; harmless personal parking |
| `PATCH` assigning backlog→team | ✓ (flip is ADMIN-only anyway) | 403 (existing flip rule) |

Note: creation with horizon backlog is not role-blocked server-side — a USER
doing it via raw API just gets an invisible personal task: it's inert and
invisible to admins too (personal-task privacy — GET /api/backlog is
owner-scoped, so no admin's backlog view ever surfaces another user's task),
and the flip rule already stops them staffing it anywhere. Removing it
requires an ADMIN with the raw task id (Task DELETE is ADMIN-only,
unqualified by ownership) — the creating USER has no delete power at all.
Accepted, not worth a special-case 403.

## Accepted consequences (deliberate, don't "fix")

- Per-admin privacy: a second admin gets their own backlog, not a shared one.
- Backlog items absent from All Tasks entirely; Search requires the explicit
  filter. The dedicated tab is the home.
- No manual reorder; priority is the ranking tool.
- No UI path to send a team task back to the backlog (API allows it).

## Tests (real-Mongo harness, extend existing pattern)

- Create validations: backlog + dueDate/recurrence/teamId each 400; empty
  period required.
- `GET /api/backlog`: USER → 403; ADMIN sees only own items.
- Assignment PATCH: ownerId cleared, weekOf stamped current week, horizon
  flipped, assignee-membership 400 still fires.
- Search: default excludes backlog; `horizon=backlog` returns it.
- `ViewMonth` rollup: daily task lands in its week's bucket; boundary-week
  daily task in the right month only; weekly-task regression guard.
