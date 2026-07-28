# taskman — MVP0 design

Single-operator task manager: personal planning (daily/weekly/monthly) + team tracking.
All decisions below were closed in the 2026-07-08 grilling session; research backing is in
`docs/research-task-tools.md`. This file is the build contract — implementation agents
follow it exactly.

## Closed decisions

| # | Decision | Choice |
|---|----------|--------|
| 1 | Users/auth | Only Amit operates. No auth, no login. Team members are records, not users. |
| 2 | Tag semantics | Task = horizon (daily/weekly/monthly) + period anchor (specific date / ISO week / month). Views are queries. |
| 3 | Nesting | `parentId`, unlimited depth; rule: child horizon ≤ parent horizon (daily < weekly < monthly). |
| 4 | Rollup | Manual parent completion; show child progress (done/total). Parent may complete with open children. |
| 5 | Lifecycle | `todo / in_progress / done / cancelled`, any-to-any transitions, no state machine. |
| 6 | Overdue | Standing query + bulk "reschedule to today". No silent auto-rollover. |
| 7 | Slippage | Attention list = overdue (due < today) ∪ period-slipped (period fully past, open, no due date). |
| 8 | Team model | ONE task entity: optional `teamId` + `assigneeId` (single). Members can belong to multiple teams. |
| 9 | View mixing | Separate worlds: planning views = personal tasks only (teamId null); team board per team. |
| 10 | MVP0 extras | Notes, priority, recurring tasks (presets only), search & filters — ALL in. |
| 11 | Recurrence | Presets: daily / specific weekdays / weekly / monthly-on-day-N. No RRULE. |
| 12 | Mongo | Docker container (mongo:7, named volume, compose file in repo). |
| 13 | Week start | Monday, ISO-8601 weeks. Timezone: machine local (Asia/Kolkata). |
| 14 | UI stack | Vite + React + TS SPA, plain CSS, no component/state libs. Served by the Go binary. |
| 15 | Backend | Go 1.25 net/http (method-pattern ServeMux), mongo-go-driver. No framework. Port 8484. |

Out of scope for MVP0: voice/AI ingestion (future `POST /api/ingest/transcript`),
notifications/reminders, auth, multi-user, drag-and-drop ordering, time estimates,
calendar-grid UI.

## Data model (Mongo db `taskman`)

### tasks
```
_id         ObjectId
title       string, required, non-empty
notes       string, optional
horizon     "daily" | "weekly" | "monthly"          (required)
period      string, required — daily: "2026-07-08", weekly: "2026-W28" (ISO, Mon start), monthly: "2026-07"
dueDate     string "YYYY-MM-DD", optional
status      "todo" | "in_progress" | "done" | "cancelled"   (default "todo")
priority    "" | "low" | "medium" | "high"           (default "")
parentId    ObjectId, optional — validate: child horizon ≤ parent horizon; parent must exist
teamId      ObjectId, optional
assigneeId  ObjectId, optional — only valid with teamId; member must belong to that team
recurrence  { freq: "daily"|"weekdays"|"weekly"|"monthly", weekdays?: [1..7 ints, ISO Mon=1], dayOfMonth?: 1..31 }, optional
seriesId    ObjectId, optional — groups recurring instances; first instance's _id
createdAt / updatedAt / completedAt (set when status→done, cleared when it leaves done)
```

Indexes: `{horizon, period}`, `{dueDate}`, `{parentId}`, `{teamId, assigneeId}`,
unique partial `{seriesId, period}` (where seriesId exists), text `{title, notes}`.

Period format must match horizon (validate). Horizon rank: daily=1, weekly=2, monthly=3;
child.rank ≤ parent.rank.

### teams
`_id, name (required, unique), createdAt`

### members
`_id, name (required), email (optional), teamIds ObjectId[], createdAt`

Deleting a task cascades to all descendants. Deleting a team fails with 409 if it has
tasks; deleting a member unassigns their tasks (assigneeId cleared).

## Recurrence semantics

- A task with `recurrence` set is the live instance of a series (`seriesId` = first instance's `_id`, set on create).
- Materialization is lazy and idempotent: during any view/attention/search read, for each
  series whose newest instance's period is past OR done/cancelled, create instances for
  the missing periods up to the current period (status todo; copy title/notes/priority/
  horizon/recurrence/team/assignee/seriesId; dueDate NOT copied; parentId NOT copied).
  The unique `{seriesId, period}` index makes concurrent creation safe (ignore dup-key).
- freq→horizon: daily/weekdays → daily tasks; weekly → weekly; monthly → monthly
  (dayOfMonth only affects dueDate? No — keep simple: monthly recurrence just spawns the
  monthly-period instance; dayOfMonth optional and stored but only used to set dueDate of
  the spawned instance when present. weekdays freq spawns one daily instance per listed
  weekday.)
- Editing a live instance's recurrence affects future spawns only. Removing recurrence
  ends the series.

## API contract (JSON, prefix `/api`)

Errors: `{"error": "message"}` with 400 (validation), 404, 409, 500.
IDs are hex strings in JSON. Every returned task is a **TaskView**: all task fields +
`progress: {done, total}` counting DIRECT children (done = done+cancelled).

```
POST   /api/tasks                 create (fields above) → TaskView
GET    /api/tasks/{id}            → TaskView + children: [TaskView] (direct, sorted by createdAt)
PATCH  /api/tasks/{id}            partial update, any mutable field (incl. status, period, parentId — revalidate rules)
DELETE /api/tasks/{id}            cascade-delete subtree → 204

GET    /api/views/day?date=2026-07-08     → {tasks: [TaskView], weekContext: [TaskView]}
       tasks: horizon=daily, period=date, teamId=null. weekContext: weekly tasks of the containing week.
GET    /api/views/week?week=2026-W28      → {tasks: [TaskView], days: {"2026-07-06": [TaskView], ...}, monthContext: [TaskView]}
       tasks: weekly of that week; days: daily tasks per date in week; monthContext: monthly of containing month. Personal only.
GET    /api/views/month?month=2026-07     → {tasks: [TaskView], weeks: {"2026-W28": [TaskView], ...}}
       tasks: monthly of that month; weeks: weekly tasks per ISO week overlapping the month, plus daily tasks dated within it. Personal only.
GET    /api/views/attention               → {overdue: [TaskView], slipped: [TaskView]}
       overdue: dueDate < today AND status in (todo,in_progress) — personal AND team tasks.
       slipped: no dueDate AND period fully past AND status open. A task never appears in both.
POST   /api/tasks/reschedule  {"ids": [...]}  → {updated: n}
       For each: period → current period of its horizon; dueDate → today only if it had one.

GET    /api/search?q=&status=&priority=&horizon=&teamId=&assigneeId=&overdue=true
       q = text search over title+notes (Mongo text index; empty q = filters only). → {tasks: [TaskView]}

POST   /api/teams        {name} → team          GET /api/teams → [team]
PATCH  /api/teams/{id}   {name}                 DELETE /api/teams/{id} (409 if tasks exist)
POST   /api/members      {name, email?, teamIds} → member     GET /api/members?teamId= → [member]
PATCH  /api/members/{id}                        DELETE /api/members/{id} (unassigns tasks)
GET    /api/teams/{id}/board → {team, members: [{member, tasks: [TaskView]}], unassigned: [TaskView]}
       Tasks grouped by assignee, any period; sorted: open before closed, then priority desc, then dueDate.
```

Views trigger recurrence materialization before querying.

## Layout & run

```
taskman/
  docker-compose.yml       mongo:7, port 27017, named volume taskman-mongo, restart unless-stopped
  Makefile                 targets: mongo (compose up -d), ui (npm ci + build), run (go run ./server), test
  README.md                run instructions
  docs/                    DESIGN.md, research-task-tools.md, PROGRESS.md lives at repo root
  server/                  package main: main.go, handlers.go, store.go, period.go, recur.go, *_test.go
  ui/                      Vite + React + TS; build output ui/dist served by Go at /
```

Server: `MONGO_URI` env (default `mongodb://localhost:27017`), `PORT` (default 8484).
Serves `/api/*` + static `ui/dist` with SPA fallback to index.html.
Go tests (table-driven, no mocks-for-mongo): period math (ISO week edges, month
boundaries, year rollover), horizon validation, recurrence next-period computation.

## UI spec

Tabs: **Day | Week | Month | Attention | Teams | Search**. Plain CSS, dark-friendly,
keyboard-fast.

- Planning views (Day/Week/Month): period navigation (‹ today ›), quick-add input at top
  (type title + Enter → creates task with view's horizon+period). Context strips
  (weekContext/monthContext) render collapsed above.
- Task row: status control (checkbox = toggle done; small dropdown for all 4 states),
  title, priority badge, due-date chip (red if overdue), progress chip (3/5) when it has
  children, recurrence icon. Click row → detail panel: notes, due date
  (`<input type="date">`), priority, recurrence editor, subtask list + add-subtask,
  team/assignee (Teams context only), delete.
- Attention: overdue + slipped sections, checkboxes + "Reschedule selected to today".
- Teams: team list + create; team board grouped by member with per-member add-task;
  unassigned section.
- Search: text input + status/priority/horizon/team dropdowns.

Dev: vite proxy `/api` → `localhost:8484`; prod: Go serves `ui/dist`.
