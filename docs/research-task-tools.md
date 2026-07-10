# Task-Management Tools — Feature Research (Gap-Analysis Input)

Purpose: ground the planned app's requirements against how Todoist, TickTick, Things 3,
Asana, Linear, ClickUp, and Microsoft To Do / Planner actually behave. Personal tools
(Todoist, TickTick, Things 3, MS To Do) set the convention for solo daily/weekly/monthly
planning; team tools (Asana, Linear, ClickUp, Planner) set the convention for
assignment/status/visibility. The planned app is both, at small scale — read
recommendations with that in mind, not as "build what Asana builds."

---

## 1. Planning horizons (daily / weekly / monthly)

**Convention:** Every personal tool centers on a single **Today** list that is date-driven,
not a manually curated list — a task lands in Today automatically when its due/start date
matches. Layered around it:
- **Upcoming** (Todoist, Things 3) — a rolling agenda, list or calendar-grid layout, showing
  the next N days/weeks; not a fixed "weekly view" but a scrollable horizon.
- **Anytime/Someday** (Things 3) — an explicit *no date yet* bucket, separate from dated
  tasks, so undated backlog items don't pollute Today/Upcoming.
- **Calendar month view** exists (Todoist calendar layout, TickTick Week/Month view) but is
  secondary — used for *seeing* load distribution, not for primary task entry.
- **Weekly review** is a GTD-derived *ritual*, not a distinct product view — it's a
  checklist/template users run manually inside Today+Upcoming (Todoist even ships a GTD
  weekly-review template). No tool has a first-class "weekly planning mode" UI.
- Team tools invert this: Asana/ClickUp/Linear default to **project/list views** (board,
  timeline), with a personal Today-equivalent (Asana "My Tasks" Today section, populated by
  a rule) layered on top rather than being the home screen.

**MVP recommendation:** Build Today as a derived, date-filtered query (not a manually
maintained list); give weekly/monthly tasks their own filtered views by tag/type rather than
inventing a calendar-grid — a list grouped by due-date-bucket covers the "weekly review"
use case without building calendar UI.

## 2. Subtasks

**Convention:** Nesting depth and semantics vary sharply and this is the most inconsistent
area across tools:
- **Todoist:** 4 indent levels; subtasks are full tasks (own due date, priority) but only
  surface in Today if *they* carry a date — a dateless subtask under a dated parent is
  invisible in Today.
- **TickTick:** up to 5 levels; subtasks are lighter-weight, plus a separate "checklist"
  primitive for sub-steps that aren't full tasks.
- **Things 3:** no true subtasks — uses lightweight "checklist items" inside a to-do (not
  independently schedulable) and "Headings" to group to-dos within a project. Two different
  mechanisms for two different needs (structure vs. steps).
- **Asana:** exactly **one level** — subtasks are full tasks (own assignee-eligible, own due
  date) but do **not inherit** the parent's project/tag/assignee, and subtasks of subtasks
  aren't supported.
- **ClickUp:** unlimited nesting depth; subtasks inherit the parent List's status set.
- **Microsoft To Do:** "Steps" are checklist-only (no own due date, no independent
  scheduling) — closest to Things 3's model.
- **Rollup behavior — universal gap:** *no tool in this set auto-completes a parent when all
  subtasks are done*, and *no tool rolls up a progress bar by default*. This is one of the
  most-requested, still-unshipped features on both the Asana and ClickUp feedback forums.
  Marking a parent done is always an independent, manual action.

**MVP recommendation:** One level of subtasks under weekly/monthly tasks (per the
requirements list) is already ahead of Things 3/MS To Do and matches Asana; subtasks should
be full rows with their own optional due date, but completion should be **manual for the
parent** — don't build auto-rollup, since even mature tools deliberately don't.

## 3. Due dates & overdue

**Convention:**
- Overdue tasks are surfaced as a **standing, always-visible group**, usually pinned above
  or merged into Today (TickTick's "Plan Your Day" explicitly walks overdue-then-today,
  one by one; Asana's My Tasks rules move a task into "Today" the moment it's overdue).
- **Rescheduling is a first-class bulk action** — "reschedule all overdue to today" is a
  one-tap/one-click operation in Todoist and TickTick, not a per-task drag.
- **No tool auto-rolls a task's due date forward silently.** Overdue tasks stay overdue
  (visually flagged, usually red) until the user explicitly reschedules or completes them —
  automatic rollover-to-today is a *third-party/niche* app pattern (Sunsama, TeuxDeux,
  Obsidian plugins), not what Todoist/TickTick/Asana/Linear do. This matters directly for
  the "rollover" candidate gap below.
- Recurring tasks reschedule from **today** on completion/postponement (not from the
  original due date) in modern implementations (Todoist changed this in 2026 specifically
  to avoid recurrence pileup).

**MVP recommendation:** Keep overdue as a visible, separate list (matches the requirements
doc) with a one-click "move to today" bulk action; do **not** auto-move overdue tasks
silently — that breaks the audit trail managers rely on for team tasks.

## 4. Task lifecycle states

**Convention:** Two clearly different state models by tool category:
- **Personal tools:** binary — done / not done. No in-progress, no cancelled. (Todoist,
  TickTick, Things 3, MS To Do all work this way; "priority" substitutes for "in progress.")
- **Engineering/PM tools:** Linear's default is the de facto industry template —
  **Triage → Backlog → Todo → In Progress → Done / Cancelled** (Cancelled and Duplicate as
  terminal non-success states, customizable per team). ClickUp groups any custom status into
  three buckets: **Active → Done → Closed**, explicitly separating "finished but still
  visible" (Done) from "fully archived" (Closed) — this two-tier "done" is a deliberate
  design most tools lack.
- **Asana has no native Cancelled/Won't-do state at all** — teams hack it with a tag on a
  Completed task, and it's a long-standing, unresolved feature request. This is a concrete
  example of enterprise tools *not* having something people assume they have.
- **"Blocked"** is not a standard state in any of these tools — it's modeled as a dependency
  relationship (Asana "Dependencies," Linear "blocked by") layered on top of a normal state,
  not a state itself.

**MVP recommendation (MVP-essential vs. bloat):**
| State | Include in MVP? | Why |
|---|---|---|
| Todo / not done | Yes | universal baseline |
| Done | Yes | universal baseline |
| Cancelled / won't-do | Yes, but simple | requirements list implies manager lifecycle control; Asana's pain shows teams want this even when the tool lacks it — cheap to add, avoid Asana's mistake |
| In-progress | Optional | only add if the team view needs to distinguish "started" from "queued"; skip for solo daily/weekly/monthly tasks |
| Blocked | Skip for MVP | model as a note/flag, not a state machine — no tool treats it as a first-class status either |

## 5. Team features

**Convention:**
- **Single assignee is the norm** for clear ownership (Todoist, Linear both enforce
  exactly one assignee per task/issue); ClickUp is the outlier allowing multiple assignees.
- **Manager visibility is filter/view-based, not a separate permission tier** — a manager
  sees member tasks by grouping/filtering a shared view (Todoist: filter by assignee across
  shared projects; Planner: Charts view showing per-member task counts colored by progress;
  Asana: group My Tasks-equivalent by assignee). None of these tools ship a distinct
  "manager dashboard" data model — it's the same task list, sliced differently by
  permission.
- **Who can change status:** in every tool studied, **any project/team member with edit
  access can change any task's status**, not just the assignee or a manager. Status-change
  restriction by role is an enterprise-tier permission feature (custom permission sets),
  not baseline behavior.
- **Comments + activity trail are standard** even in lightweight tools (Todoist has
  per-task comments + a project activity log; ClickUp's Activity view logs every field
  change). This is table-stakes, not enterprise bloat.
- Buckets/sections (Planner "Buckets," Asana "Sections") are the common way to group a
  team's tasks by phase/owner — orthogonal to status.

**MVP recommendation:** Single assignee per task; any team member can change status (don't
build role-gated status transitions — no reference tool does this at small scale); manager
"visibility" = a filtered view (assignee = X) over the same task table, not a separate
data model; add lightweight per-task comments early — it's cheap and universally expected,
unlike time tracking or custom permissions.

## 6. Commonly expected features NOT in the requirements list — gap analysis

| Candidate gap | Convention across tools | Verdict for this app |
|---|---|---|
| **Recurring tasks** | Universal in every personal tool (daily/weekly/monthly/custom); reschedules from completion date in modern implementations | **Real gap.** The requirements list has daily/weekly/monthly *tags* but no repeat rule — users will expect a daily task to regenerate, not be manually recreated. Recommend adding minimal recurrence (daily/weekly/monthly) before launch. |
| **Priorities** | Universal (P1–P4 or High/Med/Low); used to sort Today/Upcoming | **Real gap, but low cost.** Add a simple 3-level priority field; skip P1–P4 granularity. |
| **Task notes/description** | Universal — every tool has a free-text body | **Real gap.** Near-zero cost, high expectation; add a plain description field. |
| **Search/filter** | Universal, table-stakes even in simplest tools | **Real gap for anything beyond a handful of tasks.** Minimum: filter by tag (daily/weekly/monthly), assignee, and status; full-text search can wait. |
| **Carry-over of incomplete daily tasks** | **Not** silent auto-rollover in any mainstream tool studied (see §3) — overdue stays overdue until the user acts, usually via a one-click bulk reschedule | **Partially in requirements already** (overdue list). Don't build silent auto-carry; add a one-click "move overdue to today" action instead — matches convention and preserves audit trail. |
| **Ordering/manual sort** | Universal (drag-to-reorder within a list) | **Real gap but cheap.** Add a sort-order field per list; skip drag-and-drop polish for MVP, a simple up/down or numeric order is enough. |
| **Archiving** | Universal, and ClickUp's Done-vs-Closed split shows tools deliberately separate "finished" from "put away" | **Minor gap.** A simple "hide completed older than N days" view suffices; don't build a formal archive state. |
| **Quick-add / keyboard entry** | Universal in personal tools (Todoist `Q`, natural-language date parsing, global hotkey) | **Nice-to-have, not MVP.** High value for a personal daily tool but requires NLP date parsing; a plain "add task" form with a manual date picker is an acceptable MVP substitute. |
| **Reminders/notifications** | Universal, but usually push/email — infra-heavy | **Defer.** No reference tool treats this as optional, but it needs a notification channel this single-machine app may not have yet; explicitly flag as post-MVP. |
| **Week-start convention** | Universal user setting (Sunday/Monday/Saturday), because calendar-grid views need it | **Not a gap if no calendar-grid view is built** (per §1 recommendation). Only becomes relevant if a month/week calendar UI is added later. |
| **Time estimates** | Present in ClickUp/Asana but positioned as PM/enterprise (billing, resourcing), not baseline — Todoist/TickTick/Things 3 (the closest personal-tool analogues) largely skip it | **Not a gap — correctly excluded.** This is enterprise bloat for a personal+team tool with no billing/resourcing use case. |

### Top gaps ranked by (expectation × cost)
1. **Task description/notes** — near-universal, near-zero cost. Add.
2. **Overdue → today bulk reschedule action** — matches convention exactly, cheap. Add.
3. **Basic recurring tasks (daily/weekly/monthly repeat)** — universal expectation, moderate
   cost. Add a minimal version; this is the biggest true absence in the requirements list.
4. **Simple priority field** — universal, cheap. Add.
5. **Tag/assignee/status filtering** — universal, cheap at this scale. Add before search.

Explicitly **not** MVP: time tracking/estimates, role-gated status permissions, calendar
month-grid view, auto-rollup of subtask completion, silent task rollover, multi-assignee.

---

## Sources consulted
Todoist Help Center (subtasks, quick add, calendar layout, 2026 changelog); TickTick Help
Center (multilevel tasks, week view, recurring tasks); Things 3 / Cultured Code support
docs; Asana Help Center + Community Forum (subtasks, My Tasks rules, cancelled-status
requests); Linear Docs (triage, workflow states, cycles, sub-issues); ClickUp Help Center
(statuses, subtasks, recurring tasks, time estimates); Microsoft Support / Q&A (To Do steps
and recurrence, Planner buckets and Charts); Asian Efficiency GTD weekly review guide;
Sunsama/TeuxDeux rollover documentation (used as contrast, not convention).
