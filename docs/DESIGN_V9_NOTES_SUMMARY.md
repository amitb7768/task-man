# v9 — Daily notes (task activity log) + date-range summary (contract)

Decided 2026-09-03. Two features: (A) a per-task, append-only activity log
("daily notes") with author + date, status transitions auto-logged; (B) a
date-range summary of tasks for weekly reporting — rendered in-app, copied
as Markdown, downloaded as CSV.

User decisions (locked):
1. **Timeline log.** Entries `{date, at, by, text}` on the task; several
   per day allowed; author may edit/delete own entry (ADMIN any). Status
   changes are auto-logged as `kind:"status"` entries so the timeline reads
   "Mon: todo → in_progress · Tue: note…".
2. **Me-scope = personal + assigned-to-me.** With no team selected the
   summary covers the caller's private tasks plus team tasks assigned to
   them — what a person pastes into their weekly update.
3. **Sections Completed / Updated / New.** Completed: closed (done or
   cancelled) in range. Updated: still has activity in range but not
   closed in range. New: created in range, open, no activity, not backlog.
   Untouched tasks never appear.
4. **Outputs: in-app panel + CSV download + Copy as Markdown.** CSV and
   Markdown are rendered CLIENT-SIDE from the JSON (one endpoint, no
   non-JSON server surface).

---

## A. Data model — `Task.Activity`

```go
type ActivityEntry struct {
	ID       bson.ObjectID  `bson:"_id"                json:"id"`
	Kind     string         `bson:"kind"               json:"kind"`     // "note" | "status"
	Date     string         `bson:"date"               json:"date"`     // YYYY-MM-DD, machine-local; the "daily" bucket
	At       time.Time      `bson:"at"                 json:"at"`       // server instant of the write
	By       *bson.ObjectID `bson:"by,omitempty"       json:"by,omitempty"`
	ByName   string         `bson:"byName,omitempty"   json:"byName,omitempty"` // denormalized at write time (no join on read)
	Text     string         `bson:"text,omitempty"     json:"text,omitempty"`   // kind=note only
	From     string         `bson:"from,omitempty"     json:"from,omitempty"`   // kind=status only
	To       string         `bson:"to,omitempty"       json:"to,omitempty"`     // kind=status only
	EditedAt *time.Time     `bson:"editedAt,omitempty" json:"editedAt,omitempty"`
}

// on Task:
Activity []ActivityEntry `bson:"activity,omitempty" json:"activity,omitempty"`
```

Embedded array on the task doc (not a collection): cascade delete + raw-doc
restore round-trip it for free, `GetTaskDetail` carries it for free, no new
index. `ponytail:` 16 MB doc ceiling is irrelevant for a note log; split
into a collection only if a task ever accrues thousands of entries.

Invariants (add to `server/CLAUDE.md`):
- `activity` is **system-managed**: never client-settable through
  `POST /api/tasks` or `PATCH /api/tasks/{id}`. In `CreateTask` set
  `t.Activity = nil`. In `PatchTask`: set `t.Activity = nil` BEFORE
  `json.Unmarshal(raw, t)` and `t.Activity = orig.Activity` AFTER it.
  (Unmarshal into a non-nil slice resets len to 0 and appends into the
  SAME backing array — it would silently corrupt `orig.Activity`'s
  elements. Nil first so any client-sent array allocates fresh; then
  discard it.)
- **List reads exclude it**: `findViews` and `TeamHistory`'s `Find` add
  `SetProjection(bson.M{"activity": 0})` so board/view/search payloads
  don't carry every task's log. Full-doc reads (`getTaskRaw`,
  `DeleteTaskCascade`, `Materialize`) keep it. Consequently: **never write
  a doc back (`ReplaceOne`/`InsertOne`) that came from a projected read**
  — it would wipe the log. Today no path does; keep it that way.
- Status auto-log: in `PatchTask`, after validation passes and right
  before `ReplaceOne`, `if orig.Status != t.Status` append
  `{Kind:"status", Date: today-local, At: now, By: u.ID, ByName: u.Name,
  From: orig.Status, To: t.Status}` (u may be nil in direct Store tests →
  By nil, ByName ""). `CreateTask` logs nothing (createdAt is the event).
  `Reschedule` / restore / `Materialize` never touch it; spawned recurring
  instances start with an empty log (they build a fresh `Task` literal —
  do not add `Activity` to it).
- Note mutations bump `updatedAt` (the task was touched).

## A. Endpoints — notes

All three: `requireAuth`, `canAccessTask` (personal-task privacy applies:
a foreign USER, or ADMIN, on someone's personal task → 403).

| Method | Path | Body | Rule | Returns |
|---|---|---|---|---|
| POST | `/api/tasks/{id}/notes` | `{"text": "...", "date"?: "YYYY-MM-DD"}` | text non-empty after trim, ≤ 4000 chars; date valid (`parseDate`) else 400; date defaults to today-local | 201 `ActivityEntry` |
| PUT | `/api/tasks/{id}/notes/{noteId}` | `{"text": "...", "date"?: "..."}` | entry must exist (404) and be `kind:"note"` (400 otherwise); own entry or ADMIN (403); omitted `date` KEEPS the entry's date (never silently re-dates a backdated note) | 200 `ActivityEntry` (with `editedAt`) |
| DELETE | `/api/tasks/{id}/notes/{noteId}` | — | entry exists (404), `kind:"note"` (400), own or ADMIN (403) | 204 |

Mongo ops (atomic, no read-modify-write of the array):
- add: `UpdateOne({_id}, {$push: {activity: e}, $set: {updatedAt: now}})`
- edit: `UpdateOne({_id, "activity._id": nid}, {$set: {"activity.$.text": .., "activity.$.date": .., "activity.$.editedAt": now, updatedAt: now}})`
- delete: `UpdateOne({_id}, {$pull: {activity: {_id: nid}}, $set: {updatedAt: now}})`
The permission check reads the task via `getTaskRaw` first (it needs the
entry's `by`), then applies the atomic update. Path segment name:
`{noteId}` via `r.PathValue("noteId")` → `bson.ObjectIDFromHex` (400 on
garbage). Store methods: `AddNote`, `EditNote`, `DeleteNote`.

Backdating is allowed and intended (people write Friday's note on Monday);
future dates are allowed too (no bound — "any valid date").

## B. Endpoint — summary

`GET /api/summary?from=YYYY-MM-DD&to=YYYY-MM-DD[&teamId=][&assigneeId=]`
(`requireAuth`; handler parses query → `Store.Summary(ctx, SummaryParams)`).

Validation (400): from/to required + valid dates; `from <= to`;
`assigneeId` without `teamId`. No range cap ("any two dates").

Scope filter:
- `teamId` set → same membership check as `TeamBoard` (ADMIN any team,
  USER must be in `u.TeamIDs` else 403); team must exist (404). Filter
  `{teamId: T}` plus `{assigneeId: A}` when given.
- `teamId` absent → `{$and: [taskScopeFilter(u), {$or: [{ownerId: u.ID}, {assigneeId: u.ID}]}]}`.

Candidate filter (AND-ed with scope), where `fromStart = time.Date(from…,
0:00, time.Local)`, `toEnd = to + 1 day` (exclusive):
```
{$or: [
  {completedAt: {$gte: fromStart, $lt: toEnd}},
  {status: "cancelled", updatedAt: {$gte: fromStart, $lt: toEnd}},
  {activity: {$elemMatch: {date: {$gte: from, $lte: to}}}},   // MUST be $elemMatch — "activity.date": {$gte,$lte} lets DIFFERENT elements satisfy each bound
  {createdAt: {$gte: fromStart, $lt: toEnd}},
]}
```
Call `Materialize` first (every read path does). Read full docs (activity
included), then classify in Go — in this order, first match wins:

1. **completed**: `status ∈ {done, cancelled}` AND `closedDate ∈ [from, to]`.
   `closedDate` = done → local date of `completedAt`; cancelled → date of
   the LAST `kind:"status"` entry with `to == "cancelled"`, else local date
   of `updatedAt` (pre-log fallback; documented approximation — also the
   fallback for a legacy `done` doc with no `completedAt`).
2. **updated**: has ≥1 activity entry with `date ∈ [from, to]`.
3. **added**: `createdAt ∈ [fromStart, toEnd)` AND status open
   (todo/in_progress) AND `horizon != "backlog"`.
4. otherwise dropped (e.g. cancelled long ago, title edited this week).

Each section sorted by `strings.ToLower(title)`. Per-task `notes` = the
task's activity entries with `date ∈ [from, to]`, sorted by `(date, at)`
ascending — both kinds. `overdue` = open AND `dueDate != ""` AND
`dueDate < min(to, today)` (string compare, the repo idiom). Names
resolved with one `members` `$in` query (assignees) and one `teams` `$in`
query (me-scope spans teams); `byName` comes from the entry itself.

Response (all arrays non-nil — `orEmpty`, including each `notes`):
```json
{
  "from": "2026-08-31", "to": "2026-09-06",
  "teamId": "…", "teamName": "…",            // present only in team scope
  "assigneeId": "…", "assigneeName": "…",    // present only when filtered
  "completed": [SummaryTask], "updated": [SummaryTask], "added": [SummaryTask]
}
SummaryTask = {
  "id","title","status","priority","horizon","dueDate"?,
  "teamId"?,"teamName"?,"assigneeId"?,"assigneeName"?,
  "createdAt","closedDate"?,"overdue":bool,
  "notes":[ActivityEntry]
}
```

## UI

### `api.ts`
Types `ActivityEntry`, `SummaryTask`, `SummaryResponse`, `SummaryScope =
{teamId?: string; assigneeId?: string}`. Add `activity?: ActivityEntry[]`
to `TaskView`. Calls: `addNote(id, {text, date?})`, `editNote(id, noteId,
{text, date?})`, `deleteNote(id, noteId)`, `summary(from, to, scope)`.

### `TaskDetail.tsx` — "Daily notes" section
- New block **after `.td-subtasks`, inside `.td-scroll`**, copying the
  subtasks section idiom (`.td-sub-head` header + list + add row).
- Compose row: `<textarea>` (1–3 rows, auto-grow optional) + native
  `<input type="date">` defaulting to `today()` + "Add" button; Cmd/Ctrl+
  Enter submits. On success: `api.getTask` reload of the detail (one GET)
  and `onChanged()` is NOT needed (lists don't show notes).
- List: newest date first, entries within a date by `at` desc. Note
  entry: `formatCompactDate(date) · byName` meta line + text (preserve
  newlines). Status entry: muted one-liner "todo → in_progress" with the
  same meta line — not editable.
- Own note (entry.by === current user id from `AuthContext`; ADMIN → all):
  "Edit" swaps the row into a textarea+date+Save/Cancel; "Delete" removes
  immediately and shows the standard toast with an **undo** that re-POSTs
  `{text, date}` (new id — fine). **No `confirm()` dialogs.**
- After a status change through `patch({status})`, the PATCH response
  already carries the new `activity` — the existing `patch` merge updates
  the list without a refetch.
- The existing description textarea (`.td-notes`, placeholder "Add
  notes…") sits above; change its placeholder to **"Description…"** so it
  doesn't read as the same thing as Daily notes. No other rename.
- Styles in `task-detail.css`, prefixed `.td-act-*`; reuse `.td-sub-*`
  spacing/typography values.

### `SummaryPanel.tsx` (new, `components/`)
Props `{scope: SummaryScope; scopeLabel: string; from: string; to: string;
onClose}`. Slide-over built on the `.td-backdrop`/`.td-panel` shell (same
as TaskDetail; own `.sp-*` classes in a new `styles/summary-panel.css`).
- Header: "Summary" + scopeLabel + close. Toolbar: two native
  `<input type="date">` (from/to, prefilled), auto-fetch on open and on
  every change (`from > to` → inline `.error`, no fetch).
- Body: three sections `Completed (n)` / `Updated (n)` / `New (n)`; task
  row = title, status pill, `assigneeName` (team scope), `dueDate`,
  `overdue` marker, `closedDate` for completed; under it the dated notes
  (status entries muted). Empty state: `.empty` "Nothing in this range".
- Footer buttons: **"Copy as Markdown"** (`navigator.clipboard.writeText`
  → toast "Copied") and **"Download CSV"** (Blob + object URL +
  synthetic `<a download>` click; filename `summary_<from>_<to>.csv`).
  `ponytail:` blob downloads don't fire inside the Tauri webview —
  Copy-as-Markdown is the Tauri path; documented, not fixed.

### `summaryFormat.ts` (new, `components/`, PURE — no React, no DOM)
`toMarkdown(s: SummaryResponse, scopeLabel: string): string` and
`toCSV(s: SummaryResponse): string`.
- CSV: UTF-8 BOM prefix, `\r\n` rows, EVERY field double-quoted with
  internal `"` doubled. Columns:
  `section,task,status,priority,horizon,team,assignee,dueDate,closedDate,overdue,noteDate,noteBy,noteKind,note`.
  One row per (task, note); a task with zero notes in range → one row with
  empty note columns. Status entries: `noteKind=status`, `note="todo → done"`.
- Markdown:
  ```
  # Summary 2026-08-31 → 2026-09-06 · <scopeLabel>
  ## Completed (2)
  - **Title** — closed 2026-09-02 · high · due 2026-09-01 · Alice
    - 2026-09-01 · Alice: note text
    - 2026-09-02 · Alice: todo → done
  ## Updated (n) … ## New (n) …
  ```
  Omit empty meta parts; an empty section prints `_none_`.
- ONE runnable check: `ui/test/summaryFormat.test.ts` using `node:test`
  + `node:assert`, run by a new `"test": "node --test test/**/*.test.ts"`
  script (a bare directory arg is treated as an entry script on Node 26;
  Node strips types natively; the file lives outside `src/` so
  `tsc -b` ignores it; `summaryFormat.ts` must use only erasable syntax
  — the tsconfig already enforces `erasableSyntaxOnly`). Cover: quote
  escaping, newline in a note, zero-note task row, section counts.

### Mount points
- **TeamPage**: "Summary" button in `.tp-toolbar` beside the group
  toggle. Scope `{teamId, assigneeId: filterId unless null/UNASSIGNED}`;
  scopeLabel `"<team name> · <member name>"` or `"<team name>"`; range =
  `weekDates(board.week)[0]`..`[6]`. Mounted as a conditional sibling next
  to `TaskDetail`.
- **App.tsx, week tab**: "Summary" button in `.header-controls` gated to
  `tab === "week"`. Scope `{}` (me), scopeLabel `"My tasks"`, range =
  `weekDates(week)[0]`..`[6]`. Mounted next to `<ToastHost/>`.

## Tests — `server/v9_test.go` (real-Mongo harness)
1. Add note as USER on own personal task → 201 with `by`/`byName`/`date`
   = today; explicit backdate honored; `text:""` → 400; bad date → 400;
   GET detail carries `activity`; `GET /api/views/day` rows do NOT carry an
   `activity` key (projection).
2. Team task: member A adds, member B (same team) edits/deletes it → 403;
   A edits → 200 with `editedAt`; ADMIN deletes → 204; editing a status
   entry → 400. Foreign USER on A's PERSONAL task → 403; ADMIN on it → 403.
3. `PATCH {status}` appends a status entry (`from`/`to`, today's date);
   `PATCH {title}` appends nothing; `PATCH {activity: []}` (and
   `{activity:[{…}]}`) leaves the log intact.
4. DELETE cascade → `POST /api/tasks/restore` with the returned docs →
   activity round-trips.
5. Summary classification, one fixture week `[from,to]`: A done in range →
   completed; B open + note in range → updated; C created in range, no
   activity → added; D backlog created in range → absent; E created
   before range, untouched → absent; F done before range → absent; G
   cancelled in range (status entry) → completed with `closedDate`; H done
   in range but ALSO has a note in range → completed (first match wins),
   `notes` includes both entries. Sorting by title; `overdue` flag.
6. Summary scope: team+assignee filter; USER on a foreign team → 403;
   me-scope returns personal + assigned-to-me but NOT a team task assigned
   to someone else; `from > to` → 400; `assigneeId` without `teamId` → 400.

## docs/AUTH_FEATURES.md — matrix additions
- `POST/PUT/DELETE /api/tasks/{id}/notes[/{noteId}]` — any authed user
  within `canAccessTask`; edit/delete own entry only, ADMIN any.
- `GET /api/summary` — any authed user; `teamId` scope follows the team
  board rule (ADMIN any team / USER own teams); me-scope = own personal +
  assigned-to-me.

## Accepted consequences (deliberate, don't "fix")
- A cancelled task that predates the log is dated by `updatedAt` — an
  approximation until every cancel goes through the auto-log. That fallback
  applies ONLY while the task has no activity at all: once it has any entry,
  `closedDate` is `""` and the task falls through to *updated*, since every
  note bumps `updatedAt` and would otherwise re-date the cancel to today.
- `POST /api/tasks/restore` (ADMIN-only raw-doc replay) can insert a fresh
  doc with an arbitrary `activity`, forged `by`/`byName` included. Pre-v9 an
  ADMIN could already plant whole tasks this way, so this is accepted, not
  fixed.
- Deleting a note and undoing it re-creates the entry under a new id.
- A task done in range and reopened after `to` shows as *updated* (its
  status entries are in the notes), not *completed* — sections follow
  CURRENT status.
- A reopened-and-re-done task carries several status entries; the log is
  the truth, `completedAt` is just the latest done instant.
- CSV download is a browser feature; the Tauri shell gets Copy-as-Markdown.
- The `UNASSIGNED` chip on TeamPage is not expressible as a summary scope;
  with it selected the Summary covers the whole team (label says so).
