# Team Views — Industry Research (Landing + Per-Team + Member Filtering)

Purpose: ground the planned **teams landing page**, **per-team members-and-tasks page**,
and **member filtering** on how Asana, Linear, ClickUp, Monday.com, and Jira structure
team-facing views, filtered through taskman's actual constraints: single manager operator,
member *records* (not logins), optional teamId+assigneeId on tasks, due-date-centric
tracking, statuses todo/in_progress/done/cancelled, no effort/time-tracking field. Read
recommendations as "what to borrow at 1/50th the scale," not "build what Asana builds" —
see `docs/research-task-tools.md` for the same framing applied to solo/task-lifecycle
features.

---

## 1. Teams/projects landing page

| Convention | Exemplars | Fit note for taskman |
|---|---|---|
| Card/row per team: name, member avatar stack, an at-a-glance health signal (RAG color or progress %), click-through as the only real action | Asana Team Overview page, Asana Portfolio cards (on-track/at-risk/off-track) | Adopt the shape, drop the RAG subjectivity — taskman has no "health" field, so replace with **objective counts** (open, overdue). |
| Row-based multi-project dashboards built from filters/gadgets rather than a native card grid | Jira (no native cross-project landing; dashboards are hand-built with JQL gadgets) | Confirms a purpose-built landing page is a *gap* even in mature tools — worth building well since nothing to copy exactly. |
| Members tab lists people across the team, separate from the work | Asana Team Overview (Overview / All Work / Messages tabs) | Keep team roster and team tasks as two zones on the landing card/page, not interleaved. |

**Actions that live on landing cards, industry-wide:** open team, and increasingly little
else — bulk actions (add member, archive) live one level in, not on the card. Avoid
overloading the card with quick-add or inline edit.

## 2. Per-team task view: layout

| Layout | When tools default to it | Exemplars |
|---|---|---|
| **Flat filterable list/table**, sortable by due date, groupable by assignee/status | Default for issue-tracking-flavored tools; best when due dates and multiple cross-cutting filters matter more than a fixed pipeline | Linear (All Issues/Active — list is the base layout, board is opt-in), Asana List View |
| **Kanban by status** | Best for a small, stable set of workflow stages people actively *drag through*; becomes the anti-pattern past ~7 columns | Jira board (Cloud), Asana Board, ClickUp Board — all opt-in alternate views, never the only view |
| **Workload/timeline (person × time)** | Purpose-built for capacity balancing, not day-to-day triage; a manager tool, not a doer tool | Asana Workload, ClickUp Workload, Monday.com Workload widget — all **secondary views layered on the same task data**, never the landing view |
| **List grouped by assignee (swimlane-equivalent)** | The direct answer to "manager scanning multiple people's queues" | Jira swimlanes-by-assignee, Linear "group by assignee," ClickUp List grouped by assignee |

**Dominant default across all five tools: a sortable/groupable flat list, not kanban.**
Kanban and workload are opt-in secondary layouts every mature tool ships *in addition to*
the list, never instead of it. For a due-date-centric tool, this maps directly: **default
per-team view = flat task list sorted by due date, with a "group by assignee" toggle** —
skip building kanban-by-status as the primary surface (taskman already has status as a
field; a 4-column kanban isn't wrong, just not what should greet the manager first).

## 3. Member-centric filtering ("show me one person's tasks")

| Pattern | Speed for a 1:1 review | Exemplars |
|---|---|---|
| **Avatar row filter chips** (click a face, board/list scopes instantly, click again to clear) | Fastest — one click, no menu, visually scannable roster doubles as the filter UI | Jira board/backlog assignee avatars, Linear's per-cycle assignee breakdown |
| Sidebar member list (persistent, click to scope) | Fast, plus always-visible roster | ClickUp Workload "Assignees" sidebar |
| Dropdown/select assignee filter | Slower (extra click to open menu) but scales past ~15 people | Generic filter bars (Jira advanced filters, Monday board filters) |
| Grouped sections + collapse-all-but-one | Good for "scan everyone" then narrow, weaker for a fast single-person jump | Linear "group by assignee," ClickUp List grouped by assignee |

**Verdict: avatar-row filter chips are the fastest 1:1-review pattern** and need no
login/identity system — they filter on the existing `assigneeId` field. Pair with
"group by assignee" as the *unfiltered* default so the manager can scan everyone, then
click one avatar to drop into a single person's queue for the 1:1 itself.

## 4. Manager tracking affordances

- **Overdue-per-member surfacing:** every tool with a manager-facing view puts overdue as
  a distinct, usually red-badged count next to the person, not buried in a status column
  (Todoist Business People tab: "each member's assigned and overdue tasks... quickly spot
  where someone might need support"). This is the single most transferable pattern for a
  due-date-centric tool.
- **Workload/balance indicators:** universally **task count** is the default unit (hours/
  effort/story-points are opt-in alternates in ClickUp/Linear, not the default). taskman
  has no effort field, so task-count-per-member is the correct — and only feasible —
  metric; don't add a story-point/estimate field just to enable a workload view.
- **Status-at-a-glance:** small colored counts/dots per status (todo/in-progress/done
  count chips) beat a full kanban for a glance-and-move-on read; RAG (red/amber/green)
  coloring is the general vocabulary for "needs attention," which maps cleanly onto
  overdue(red)/due-soon(amber)/on-track(green) without inventing a new taxonomy.
- **Unassigned work:** never hidden in any tool studied — Jira renders unassigned issues
  as their own swimlane (configurable above/below named lanes); Ganttic gives it a
  standing top-of-board section. **Always render an explicit "Unassigned" bucket/section**,
  don't drop unassigned tasks from grouped views.

## 5. Task creation in team context

- **Inline add within a grouped section auto-fills the group's field** — creating a task
  under a person's group in ClickUp's assignee-grouped List, or under a status column,
  sets that field automatically. This is the "assignee-first" flow and is the fastest path
  for a manager adding one task while looking at one person's queue.
  taskman's existing quick-add token parser (`@Name` for assignee, per `DESIGN_V2_UI.md`)
  already supports a task-first flow with inline assignee — keep both entry points: quick-
  add token *and* "+ add" under a member section that pre-fills `@Name`.
- **Modals are reserved for full detail entry** (description, subtasks, recurrence), not
  the default single-line add — every tool studied keeps the fast path to one line.
- **Bulk assignment/reassignment** is table-stakes once tools have multi-select (Asana's
  bottom black action bar, ClickUp/Teamwork bulk edit) — worth a "select N tasks → reassign"
  action on the per-team list for rebalancing, cheap to add given taskman already has
  bulk-reschedule/undo machinery per `DESIGN_V2_UI.md`.

## 6. Anti-patterns to avoid (confusing team views)

1. **Mixing personal and team context in one view** — Asana users routinely confuse
   "My Tasks" with team project views because assignee-you and viewer-you overlap in the
   UI. taskman already avoids this by design (personal planning views exclude team tasks)
   — keep that separation explicit in the new pages too.
2. **Over-columned kanban** — more than ~7 columns (or columns used as both status *and*
   assignee *and* priority simultaneously) is a named anti-pattern; the fix is swimlanes
   or a separate grouping axis, not more columns. taskman's 4 statuses are safely under
   the threshold — the risk is a *future* team wanting per-status-per-assignee columns;
   don't build toward that.
3. **Hidden/buried filters** — the current one-board-page design's "member columns +
   per-member quick-add" *is* a filter (implicitly, one column = one person's filtered
   view) but with no way to see cross-member due-date urgency at a glance, and no
   unassigned bucket. The fix per §2–4: make due date and overdue-count the first-class
   visible signal, filters explicit (avatar chips) rather than baked into layout.

## 7. Login-dependent conventions — do not build these

| Feature | Needs login/identity? | Why |
|---|---|---|
| Comments, @mentions, notifications | **Yes** | Requires a distinguishable actor per member to attribute/notify — taskman members don't log in. |
| "My Tasks" / personalized homepage per member | **Yes** | Assumes the member is the one opening the app; taskman's only operator is the manager. |
| Activity feed ("who changed what") beyond a simple audit log | **Yes, in practice** | Tools show it as *your* team's feed inside *your* session; without auth there's no session to scope it to. |
| Assignee field, avatar filter chips, due-date sort/group, overdue badges, workload-by-count, unassigned bucket, bulk reassign | **No** | All are queries/filters over data the manager already owns and edits directly — no second identity required. Confirmed safe to build now. |
| Role-based status-change permission | **No, and skip anyway** | Per `research-task-tools.md` §5, even multi-login tools don't gate status changes by role at small scale. |

Design forward-compatibility: keep `assigneeId` as a plain foreign key to the member
record (already true) so that *if* login is added later, these same views keep working —
just don't build the login-dependent row above until there's an actual second user.

---

## Recommended shape

**(a) Teams landing page:** one card per team — name, member avatar stack (not full list),
**open-task count** and **overdue count** (red badge if >0), click-through to the team
page. No inline actions beyond "open team" + a lightweight "+ new team" affordance outside
the card grid. Skip health/RAG scoring — taskman has no status-of-team field to back it.

**(b) Per-team page:** default layout = **flat task list sorted by due date** (overdue
pinned/flagged at top), with a **"group by assignee" toggle** for the scan-everyone view.
Member filter = **avatar row filter chips** across the top (click a face to scope to one
person, click again to clear) — fastest pattern for a 1:1 review and needs no login. Always
render an explicit **Unassigned** section/chip, never hide it. Kanban-by-status can exist
as an optional secondary view toggle, not the default.

**(c) 5–8 features that matter most for a single-manager tracking tool:**
1. Overdue-per-member badge (red count), visible without opening the team page.
2. Avatar-row member filter, instant scope for 1:1s.
3. Due-date-sorted flat list as the default per-team layout.
4. Explicit Unassigned bucket, always visible.
5. Task-count-based workload indicator per member (no effort/estimate field needed).
6. Inline "+ add" under a member's group that pre-fills assignee (assignee-first fast
   path), alongside the existing `@Name` quick-add token.
7. Bulk multi-select reassignment for rebalancing.
8. Status-at-a-glance as small count chips (todo/in-progress/done), not a full kanban.

**(d) 3 anti-patterns to explicitly avoid:**
1. Blending personal and team views into one context (keep them structurally separate,
   as taskman already does for planning views).
2. Growing the per-team view into an over-columned kanban (status × assignee × priority
   all as columns) — keep grouping to one axis at a time.
3. Letting filtering be implicit in layout (one column = one person, as in the current
   board) instead of an explicit, visible filter control — hidden filters are the root of
   the "confusing" complaint driving this redesign.

---

## Sources consulted
Asana Help Center (My Tasks, Workload, Universal Workload, Portfolio progress/dashboards,
Team Overview page, list view, bulk edit); Asana Blog/forum (Team Overview redesign,
Workload launch); Linear Docs (Triage, Teams, Display options, Board layout, Custom Views,
User views, Conceptual model); Linear Changelog (Combined board/issue view, Project
backlog & grouping); ClickUp Help Center (Workload view + capacity/availability, Hierarchy/
Spaces/Lists, List view grouping, task creation from grouped views, Assign tasks); Monday.com
Support (Workload Widget, board views, groups/filters); Jira Cloud docs (swimlanes,
backlog, board customization) + Atlassian Community threads (avatar filtering, multi-project
dashboards); Todoist Help Center (Team activity, People tab, team roles); Height.app
marketing/docs (workflows — noted product shut down Sept 2025, used only as a data point,
not a live convention source); Carbon Design System / RAG-status explainer articles (status
indicator pattern vocabulary); general kanban anti-pattern sources (Multiboard, I.M.
Wright's Hard Code) for the column-count guidance in §6.
