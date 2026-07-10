# taskman — Teams v3 feature set

Closed 2026-07-09 (grilling + `docs/research-team-views.md`). Replaces the current
single-board Teams view. Personal views are untouched.

## Navigation

`Teams` (sidebar) → **Teams landing** → click a team → **Team page** (members & tasks).
Breadcrumb/back affordance on the team page. Deep state: selected team persists per
session (localStorage) so returning to Teams reopens the last team's landing selection.

## Screen 1 — Teams landing

- One card per team: team name, member avatar stack (initials), open-task count,
  overdue count as a red badge (hidden when 0), member count.
- Click card → team page. No health/RAG scores (no team-status field exists).
- "New team" affordance (name only). Team rename/delete lives on the team page.
- Empty state for zero teams.

## Screen 2 — Team page (members & tasks)

**Layout: flat task list, due-date sorted, overdue pinned at top.** No kanban. The old
member-columns board is DELETED — grouping replaces it.

- **Avatar filter chips** across the top: one chip per member (initials avatar + first
  name + open-count workload number). Click to scope the list to that member (1:1 mode);
  click again to clear. Single-select. An "Unassigned" chip scopes to ownerless tasks.
- **Group by member** toggle: off = flat due-date list (default); on = member sections
  (each with per-member inline add), Unassigned section always rendered last — unassigned
  work is never hidden in either mode.
- **Status count chips** in the team header: todo / in-progress / done-this-view counts.
  Overdue-per-member: red count on a member's avatar chip when they have overdue tasks.
- **Task rows** (reuse the shared TaskRow anatomy): status control, priority tick, title,
  assignee avatar, due chip (red when overdue), progress chip. Horizon is DE-EMPHASIZED:
  not shown on rows; team tasks silently default to daily + today's period at creation;
  horizon/period visible only inside the task detail panel.
- **Creation**: single quick-add at top (tokens work; `@Name` assigns); per-member inline
  add rows in grouped mode (assignee-first). Due date editable in the detail panel and
  via a small inline date affordance on the row.
- **Bulk actions**: multi-select rows (same interaction as Attention) → bottom bulk bar:
  Reassign to <member select> (or Unassign), Set due date, Mark done, Delete — all with
  undo toasts (reuse restore/patch-back machinery).
- **Members management**: a "Members" side panel/section on the team page — list with
  name/role/email, add member (name, email, role, teams), edit, remove (confirm; removal
  unassigns their tasks — existing backend behavior). Team rename/delete also here
  (delete blocked while tasks exist — existing 409).

## Explicit exclusions (login-dependent or rejected)

- No comments/mentions/notifications/my-tasks — they need member logins (future; the
  schema already supports it: members have email; nothing here precludes auth later).
- No kanban-by-status, no workload effort estimates (counts only), no timeline/Gantt.
- Anti-patterns to avoid (from research): filter baked into layout (the old board's
  flaw), mixed personal/team contexts, over-columned boards.

## Backend delta (small)

- `GET /api/teams` response gains per-team `{memberCount, openCount, overdueCount}`
  (server-side aggregation; overdue = dueDate < today & status open).
- Everything else uses existing endpoints: board/search for tasks, PATCH per task for
  bulk ops (client loops, same as Attention), member CRUD unchanged.

## Design deliverables wanted (via Claude design)

1. Teams landing (cards + empty state).
2. Team page: flat mode + grouped mode + member-filtered (1:1) mode + bulk-selection
   state + members panel. Both themes, existing token system.
