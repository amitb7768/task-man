# taskman v2 — UI revamp contract (addendum to DESIGN.md)

Visual spec source of truth: `design_handoff_taskman_ui/` (README.md + the eight
`*.dc.html` deliverables). This file covers only what the handoff adds/changes at the
API and architecture level. DESIGN.md still governs everything it already defines.

## Backend extensions (v2)

### 1. Recurrence interval
`recurrence` gains two fields:
```
recurrence: {
  freq: "daily"|"weekdays"|"weekly"|"monthly",
  interval?: int ≥ 1 (default 1),          // "every N periods"
  anchor?: string,                          // SERVER-managed period string, not client-settable
  weekdays?: [1..7], dayOfMonth?: 1..31
}
```
- `anchor` = the period that occurrence math counts from. Set server-side when a task is
  created with recurrence (= that task's period; for freq=weekdays, the ISO WEEK containing
  the task's date). When a PATCH changes `recurrence` (freq or interval), re-anchor to the
  patched instance's period (same rule). Copied unchanged to spawned instances.
- Occurrence rule (period P is an occurrence iff):
  - daily:    daysBetween(anchor, P)   % interval == 0
  - weekly:   weeksBetween(anchor, P)  % interval == 0
  - monthly:  monthsBetween(anchor, P) % interval == 0
  - weekdays: weekday(P) ∈ weekdays AND weeksBetween(anchorWeek, isoWeek(P)) % interval == 0
- Materialization: unchanged mechanism (lazy catch-up to current period, unique
  {seriesId, period} index), but steps only through occurrence periods per the rule above.
- Validation: interval ≥ 1 if present; 400 otherwise. Absent interval == 1.
- Tests: extend the recurrence table tests with interval 2/3 cases per freq, incl.
  weekdays-with-interval crossing year boundary.

### 2. Member role
`members` gains optional `role` string (e.g. "Backend"). Pass-through on create/PATCH,
returned everywhere members are returned (incl. team board). No validation beyond string.

### 3. Undoable delete (exact restore)
- `DELETE /api/tasks/{id}` now returns **200 {"deleted": [Task, ...]}** — the full JSON
  docs of the entire deleted subtree (same shape as task JSON everywhere else), instead
  of 204.
- New `POST /api/tasks/restore` with body `{"tasks": [Task, ...]}` — reinserts the docs
  with their ORIGINAL ids (parse hex id strings back to ObjectIDs; InsertMany
  ordered=false; ignore duplicate-key errors; count actual inserts).
  Returns `{"restored": n}`. No revalidation beyond JSON decode — these are docs the
  server itself produced. This exists solely to power undo toasts.

Everything else in the API is unchanged.

## Frontend architecture (v2)

- **Theming (central):** every color in the app comes from the design tokens — no
  hardcoded hex values in any component/view CSS, ever. Tokens are defined once in
  index.css: light values on `:root`, dark values applied BOTH under
  `@media (prefers-color-scheme: dark)` (for theme=system) AND under
  `:root[data-theme="dark"]`; `:root[data-theme="light"]` re-asserts the light values so
  an explicit choice beats the OS in both directions. A 3-state toggle (System / Light /
  Dark) lives in the sidebar footer (the handoff's "theme indicator" slot), persists to
  localStorage (`taskman-theme`), and stamps/removes `data-theme` on `<html>`;
  "System" removes the attribute. Shadows/elevation tokens follow the same mechanism.
- **CSS ownership:** `ui/src/index.css` holds ONLY: font imports, design tokens
  (`:root` + dark override), resets, and app-shell + shared-primitive styles
  (task row, status control, chips, badges, toast, quick-add). Each view puts its own
  styles in `ui/src/styles/<view>.css`, imported by that view's component. No view may
  edit index.css.
- **Fonts:** self-hosted via npm `@fontsource/newsreader`, `@fontsource/ibm-plex-sans`,
  `@fontsource/ibm-plex-mono` (weights per handoff README). No Google Fonts runtime fetch.
- **Shared primitives** (ui/src/components/): `StatusControl` (20px cycling
  todo→in_progress→done→cancelled→todo per README spec), `TaskRow` (48px workhorse with
  hover action cluster + .18s collapse-on-delete), `Toast` (undo toast, 6s auto-dismiss,
  imperative show(message, onUndo)), `QuickAdd` (56px field, token parser, "Will create"
  preview).
- **Quick-add token parser** (hand-rolled, no date lib), documented forms only:
  `!high !med !low` priority · `^today ^tomorrow ^mon..^sun ^<mon><day>` (e.g. ^jul12)
  and `^YYYY-MM-DD` due · `@Name` assignee (team context only) · `*daily *weekly
  *monthly *weekdays` recurrence · `#daily #weekly #monthly` horizon override.
  Unrecognized tokens stay in the title text.
- **Undo wiring:** single-task delete and bulk actions never confirm(); they act
  immediately and show the undo toast. Undo for delete = POST /api/tasks/restore with
  the DELETE response. Undo for bulk reschedule / bulk done = PATCH each task back to
  its remembered prior {period, dueDate, status}.
- **Status naming:** wire value stays `in_progress`; the handoff mocks show
  `in-progress` as a display label only.
- **Attention badge:** sidebar Attention item shows overdue+slipped count; refresh it
  opportunistically (on mount and after any mutation callback).
- **Team switcher:** segmented control as designed while teams ≤ 5, else a <select>.

## Status quo reminders (unchanged v1 rules the mocks must respect)
- Clearing notes/dueDate over the wire = send `""`; clearing pointer fields
  (recurrence/parentId/teamId/assigneeId) = send `null`.
- Child horizon ≤ parent horizon; parent completion manual; progress = direct children.
- Personal planning views exclude team tasks; attention includes both.
