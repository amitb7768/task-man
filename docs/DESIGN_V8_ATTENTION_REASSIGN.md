# v8 — Attention scheduling + backlog moves + single-task reassign (contract)

Decided 2026-08-05. Planned as UI wiring + server regression tests only
(`UpdateTaskInput` already carries every field needed); review then forced
two surgical server fixes: explicit ADMIN `weekOf` now survives the
personal→team flip stamp (the undo primitive was being clobbered), and
`taskScopeFilter` normalizes nil TeamIDs (team-less USERs 500'd on
Attention/Search).

User decisions (locked):
1. "Schedule to date D" **generalizes the existing reschedule rule**: dated
   task → `dueDate = D`; undated task → `period =` the period containing D
   for its horizon (daily→D, weekly→D's ISO week, monthly→D's month), staying
   undated. Same semantics as reschedule-to-today with D substituted.
2. Move-to-backlog **skips ineligible tasks and reports in the toast**
   ("moved N, skipped M") — v6 rollover-Move precedent. Ineligible:
   recurring instances (`recurrence != nil || seriesId != nil` — parking
   them collides on the unique `{seriesId, period:""}` index and the series
   respawns anyway); team tasks when the actor is USER (flip is
   ADMIN-only); and tasks with subtasks — backlog has no horizon rank;
   server 400s the parent. ADMIN parking a team task un-teams it into their
   private backlog.
3. Single-task reassign lives in **TaskDetail** (new Assignee row) — reaches
   every view that opens the panel. No row-hover duplicate.
4. **Attention rows open TaskDetail**: clicking the title/reason area opens
   the slide-over; the checkbox remains the selection affordance (TeamPage
   row behavior).

---

## Attention bulk bar — two new actions

### Schedule for… (date picker)
- Native `<input type="date">` in the bulk bar (min = today), plus the
  existing "Reschedule to today" button unchanged (it keeps using
  `api.reschedule`; the picker covers arbitrary D).
- Mechanic: client-side `Promise.all` of per-task `api.updateTask` (bulk
  convention — NOT the reschedule endpoint, which has no date param and is
  a raw-update wart not to be spread):
  - `t.dueDate` set → `PATCH {dueDate: D, period: periodOf(D, t.horizon)}`
    (dated tasks still carry a horizon/period pair; the period must move
    along with the due date or it goes stale)
  - undated → `PATCH {period: periodOf(D, t.horizon)}` (period.ts helpers;
    daily = D, weekly = ISO week of D, monthly = `D.slice(0,7)`)
- Recurring instances are NOT skipped (existing reschedule-to-today doesn't
  skip them; period-only patches leave `Recurrence.Anchor` untouched by
  design — verified in research).
- Undo: snapshot `{id, period, dueDate}` → re-PATCH (existing runReschedule
  undo shape).
- Known accepted edge (pre-existing class): patching a recurring instance's
  period onto a period where a sibling instance already exists trips the
  unique `{seriesId, period}` index → that task's PATCH fails; fail-fast
  `Promise.all` partial-failure behavior is the established repo idiom
  (v7 review m8 — systemic ticket, not this wave). Since the dated branch
  now also sends `period`, this collision edge applies equally to dated
  recurring instances, not just undated ones.

### Move to backlog
- Eligibility filter (client-side): NOT recurring AND (personal OR actor is
  ADMIN). Skipped tasks counted in the toast by reason.
- PATCH per eligible task:
  - personal → `{horizon:"backlog", period:"", dueDate:""}`
  - team (ADMIN) → `{teamId:null, assigneeId:null, horizon:"backlog",
    period:"", dueDate:""}` (recurrence:null unnecessary — recurring are
    skipped)
- Undo: snapshot `{id, horizon, period, dueDate, teamId, assigneeId,
  weekOf}`; restore PATCH built **conditionally** — include
  `teamId/assigneeId/weekOf` ONLY for tasks that were team tasks (sending a
  `weekOf` key as USER → 403 per v6 rule; personal tasks never had one).
  Team restore is a single PATCH (flip re-stamps weekOf to current week,
  the explicit `weekOf` in the same patch must win — regression-tested
  server-side, see Tests).

## Attention rows — open detail
- `AttentionRow` title/reason area becomes clickable → opens `TaskDetail`
  (same onOpen wiring as TeamPage rows); checkbox untouched. Keyboard:
  focusable + Enter opens.
- Detail patches trigger the existing reload/notify path.

## TaskDetail — Assignee row
- Rendered only for team tasks (`detail.teamId` set). Lazy-fetches
  `api.teamBoard(teamId)` when the row first renders; `<select>` options:
  "Unassigned" + members as "Name · N open" (open count derived the same
  way AssignPopover does).
- On change → `patch({assigneeId: value || null})`. No role gate in the UI:
  any team member may reassign within their team (existing server rule,
  AUTH_FEATURES decision #5); server still 400s non-member assignees.
- Show the team name next to the row label if the board response makes it
  free; skip otherwise.

## Server — no code changes; regression tests only

New `server/v8_test.go` (real-Mongo harness):
1. Slipped personal task (stale period): `PATCH {period: <future period>}`
   → gone from ViewAttention. Overdue task: `PATCH {dueDate: <future D>}`
   → gone from ViewAttention.
2. Recurring instance `PATCH {period: <future same-horizon period>}` →
   200, `Recurrence.Anchor` unchanged (assert exact anchor).
3. Park personal (USER and ADMIN): `{horizon:"backlog", period:"",
   dueDate:""}` → task appears in that user's `/api/backlog` (for USER:
   create via API despite hidden tab — v7 accepted behavior — then verify
   403 on the backlog endpoint but the task simply exists parked; assert
   via search horizon=backlog as owner).
4. Park team task: ADMIN single PATCH `{teamId:null, assigneeId:null,
   horizon:"backlog", period:"", dueDate:""}` → 200, in admin's backlog;
   USER same PATCH → 403 (flip rule).
5. Un-park undo round-trip: ADMIN restores with ONE PATCH
   `{teamId, assigneeId, horizon, period, dueDate, weekOf:<original>}` →
   assert weekOf equals the ORIGINAL value, not the flip-stamped current
   week (explicit patched weekOf must override the flip stamp; if it does
   not, that is a finding to surface, not to code around).
6. Reassign: USER member PATCHes `assigneeId` to another member of the same
   team → 200; to a non-member → 400 (existing validator, regression).

## docs/AUTH_FEATURES.md — no matrix changes
No new endpoints, no gate changes. (Reassign-by-USER-within-team is already
documented as decision #5.)

## Accepted consequences (deliberate, don't "fix")
- Partial-failure behavior of bulk PATCHes is the repo-wide fail-fast idiom.
- A USER sees "Move to backlog" skip their team tasks (toast explains) —
  no pre-selection filtering, rows carry no team indication today.
- Parking a team task re-homes it to the ADMIN's private backlog (ownerId =
  patcher); the team loses sight of it — that is the point of parking.
- Schedule-for date picker allows any date ≥ today; past dates not offered.
