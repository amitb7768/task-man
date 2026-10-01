# v10 — Tags on tasks (contract)

Decided 2026-10-01. One build wave (backend + UI in parallel), then
review, then browser E2E on a scratch instance. Prod deploy is a separate,
user-approved step (schema migration + binary restart).

Orchestrator decisions (locked; built directly from the existing design
system, no mockup round):

1. A task carries **0..N free-form string tags**. Tags are plain strings,
   not entities: no tags table, no colours, no rename/merge admin. The set
   of "known tags" is whatever is on tasks right now.
2. **Storage: `tasks.tags JSONB NOT NULL DEFAULT '[]'`** + GIN
   (`jsonb_path_ops`). jsonb over `TEXT[]` because the Scanner is
   `json.Unmarshal` (no array-literal quoting rules to get wrong) and the
   containment operator is the same shape. Model type `model.Tags []string`
   with Valuer/Scanner; nil persists as `[]`, never SQL NULL / jsonb null.
3. **Normalisation is server-side, in `validateTaskFields`** (so create AND
   patch AND restore-of-foreign-payloads are all covered): trim, lowercase,
   drop empties, dedupe preserving first occurrence. Rejections (400):
   a tag containing whitespace or `,` or `#` ("invalid tag %q"), a tag
   longer than 30 runes, more than 20 tags. Normalised set is what persists.
4. **Filter semantics: `tag` query param, repeatable, AND** — a task
   matches when it carries *every* requested tag (`tags @> '["a","b"]'`).
   One SQL fragment covers single and multi. Filtering is **server-side on
   every list endpoint** (not client-side), so paginated history stays
   correct and every view gets it through the same one-liner.
5. **Known-tags source: `GET /api/tags`** → `{ tags: [{tag, count}] }`
   over the caller's `taskScope` (personal-task privacy preserved: an
   ADMIN never sees tag names from a USER's personal tasks), open tasks
   only (done/cancelled excluded — the filter bar is a planning tool),
   ordered count desc, tag asc. Optional `teamId` narrows to that team.
   Optional `status=open|closed` (default `open`, anything else 400):
   `closed` counts done/cancelled tasks instead, so TeamHistory's bar
   offers the tags that actually exist in history.
6. **Filter state is global per browser**: one selected-tag set, persisted
   in localStorage (`taskman-tagfilter`), shared by every view through a
   `useTagFilter()` hook. Switching tabs keeps the filter; a visible
   "clear" affordance exists on every view.
7. **Editing tags**: (a) quick-add syntax `#word` in every composer /
   QuickAdd (the `#` sigil is already the horizon override; `#daily`,
   `#weekly`, `#monthly` stay horizons, every other `#word` becomes a tag —
   accepted behaviour change: `#foo` no longer stays in the title);
   (b) a "Tags" row in TaskDetail (chips with ×, text input: Enter or `,`
   adds, Backspace on empty removes last, blur adds pending text).
   No bulk "add tag to selected" (YAGNI; the bulk bars can grow it later).
8. **JSON: `tags` is always present** on TaskView (`[]` when empty).
   PATCH: key absent = unchanged; `null` or `[]` = clear; array =
   replace-all. Recurring instances spawned by Materialize **inherit the
   series head's tags**. `SummaryTask` does NOT get tags (reporting
   export unchanged; add when someone asks).

---

## Data model

`internal/repo/migrations/0002_tags.up.sql`:

```sql
ALTER TABLE tasks ADD COLUMN tags JSONB NOT NULL DEFAULT '[]'::jsonb;
CREATE INDEX tasks_tags ON tasks USING GIN (tags jsonb_path_ops);
```

`internal/model/tags.go`:

```go
// Tags is a task's normalised tag list, stored as a jsonb array. nil and
// empty both persist as '[]' and serialise as [] — never null.
type Tags []string
func (t Tags) Value() (driver.Value, error)   // json.Marshal(nonNil(t)) → string
func (t *Tags) Scan(src any) error            // []byte|string → json.Unmarshal
func (t Tags) MarshalJSON() ([]byte, error)   // nil → "[]"
```

`model.Task` gains `Tags Tags \`gorm:"column:tags;type:jsonb" json:"tags"\``
(no omitempty). The byte-stable-JSON note on the struct is about the
migration's parity test; adding a field is in scope.

`model.NormalizeTags(in []string) (Tags, error)` — pure, unit-tested:
trim → lowercase → drop "" → reject (whitespace | `,` | `#` | >30 runes)
→ dedupe (first wins) → reject >20.

## Service

- `validateTaskFields`: `t.Tags, err = model.NormalizeTags(t.Tags)`; error
  → `badRequest(...)`. Runs for create, patch, restore (restore already
  calls validation? — if it does not, normalise there explicitly so a
  hand-crafted restore payload can't store un-normalised tags).
- `patchTaskTx`: before `json.Unmarshal(raw, t)`, `t.Tags =
  slices.Clone(t.Tags)` — `orig := *t` shares the backing array and
  stdlib decoding into an existing slice reuses it. (Same hazard class as
  the Activity nil-out two lines above; comment it.)
- Materialize spawn struct literal: copy `Tags` from the head.
- New helper `tagCond(tags []string) taskCond`: empty → zero cond;
  else `.and("tags @> ?::jsonb", mustJSON(tags))`.
- **Every list read takes tags and ANDs `tagCond`** (signatures grow by one
  `tags []string` param; empty slice = today's behaviour):
  `ViewDay`, `ViewWeek` (tasks, each day bucket, monthContext),
  `ViewMonth` (tasks, each week bucket), `ViewAttention` (both conds),
  `Backlog`, `Search` (via `SearchParams.Tags []string`), `TeamBoard`
  (the visible-tasks cond; `staleOpen` count unfiltered — it is a board
  health signal, not a list), `TeamHistory`. `TeamRollover`, `Summary`,
  `GetTaskDetail` children: unfiltered (not list views the filter bar
  owns).
- `ListTags(ctx, teamID *string) ([]model.TagCount, error)`:

```sql
SELECT x.tag, count(*) AS count
FROM tasks t, jsonb_array_elements_text(t.tags) AS x(tag)
WHERE <taskScope(u)> AND t.status IN ('todo','in_progress') [AND t.team_id = ?]
GROUP BY x.tag ORDER BY count DESC, x.tag ASC
```

Note: Materialize is NOT called by ListTags (pure read of what exists).

## HTTP

- Every list handler above parses `r.URL.Query()["tag"]` (repeatable; blank
  values dropped; values are normalised with `NormalizeTags` so `?tag=Foo`
  matches `foo`; an invalid tag → 400) and passes it down.
- `GET /api/tags?teamId=` — `requireAuth`. Response `{ "tags": [{"tag":
  "backend", "count": 3}, ...] }`; `tags` always non-null (`orEmpty`).
  Unknown teamId → empty list (no 404; it is a filter source).
- `docs/AUTH_FEATURES.md` matrix: `GET /api/tags` — ADMIN: own personal +
  all team tasks; USER: own personal + own teams' tasks.
- `internal/httpapi/CLAUDE.md` route list gains the endpoint.

## UI

- `api.ts`: `TaskView.tags: string[]`; `CreateTaskInput.tags?: string[]`;
  `UpdateTaskInput.tags?: string[] | null`; every list fetch gains an
  optional `tags?: string[]` arg serialised as repeated `tag=`
  (`qs()` must support array values); `SearchParams.tags?: string[]`;
  `api.tags(teamId?)` → `{tags: {tag, count}[]}`.
- `src/tags.ts` (pure, Node-testable, `.ts` imports only):
  `normalizeTag(s)` (same rules as server, for client-side preview — the
  server is still the authority), `TAG_FILTER_KEY`.
- `src/components/TagFilter.tsx` + `useTagFilter()` (same file):
  hook = `[tags, setTags]` backed by localStorage (try/catch), cross-view
  via a module-level listener set (same pattern as `notifyTasksChanged`).
  Component: renders nothing when `/api/tags` returns no tags AND the
  filter is empty; otherwise a chip row (`.tag-filter`): known tags as
  toggle pills (`.tag-pill[.active]`, count in mono), selected tags not in
  the known set still shown active (so a stale filter can be cleared), and
  a "clear" link when any selected. Refetches on mount and on
  `notifyTasksChanged`. Props: `teamId?`. Fetch failure (old binary) →
  renders nothing.
- Views (all nine): call `useTagFilter()`, pass `tags` into the fetch, add
  `tags` to the reload effect deps, render `<TagFilter/>` at the top slot
  the exploration identified (Day/Week/Month: first child of `pl-view`;
  Backlog: under `bl-toolbar`; AllTasks: in `all-tasks-toolbar-right`;
  Attention: own row above the toolbar, rendered even when `hasItems` is
  false so a filter that hides everything can be cleared; TeamPage: a
  sibling row under `tp-filter-row`, `teamId` passed; TeamHistory: between
  `hi-header` and `hi-body`; Search: in `pl-toolbar`). Empty-state copy
  when the filter hides everything: "No tasks match the tag filter ·
  clear" (reuse each view's existing empty-state element; no new
  component).
- Row chips: `TaskRow` meta cluster, `bl-row`, `attn-row`, `hi-row` each
  render `task.tags.slice(0,3)` as `span.chip.tag` + `span.chip.tag.more`
  "+N" when longer. CSS once in `index.css` next to `.chip.subtle`:
  `--surface-2` bg, `--text-muted`, a leading `#` via `::before`.
  Clicking a chip is NOT a filter action (rows already have click = open).
- `TaskDetail`: "Tags" `td-row` → `.td-tags`: chips with × (patch
  replace-all without that tag) + `<input>` (Enter/`,` → add, Backspace on
  empty → remove last, blur with text → add). Optimistic: use the server
  response (it is the normalised truth) to set `detail`.
- `quickAddParser.ts`: `#word` → `result.tags.push(word.toLowerCase())`
  unless it is a horizon; `ParsedQuickAdd.tags: string[]`. Preview chip
  `.qa-chip.tag`, legend entry `#tag`. `QuickAddResult.tags`,
  `TeamPage.createOnTeam`, `TaskComposer` fastCreate/createFromDraft/
  create/expand/clearDraft/draftFieldCount all carry `tags` through (the
  composer keeps tags as parsed-from-title only — no extra pill; the
  expanded composer shows the parsed tags as read-only chips under the
  title so the user sees what will be applied).
- Undo snapshots in existing bulk actions are unaffected (they re-PATCH
  specific fields, never `tags`).

## Tests

Go (`TASKMAN_TEST_PG_DSN`, scratch schema per test — 0002 is applied
automatically by both harnesses; check `repo/migrate_test.go` for a
hardcoded version/table assertion and extend it):

- `model`: `NormalizeTags` table test (trim/lower/dedupe/empties/each
  rejection); `Tags` Value/Scan/MarshalJSON nil → `[]`.
- `service`: create with tags persists normalised; patch absent/null/[]/
  replace semantics + orig-aliasing guard (patch tags on a task that had
  tags, assert the FSM/other path sees correct values); invalid tag 400;
  `ViewDay`/`ViewWeek` (tasks + day bucket + context) / `Backlog` /
  `Search` / `TeamBoard` / `TeamHistory` filter by one tag and by two
  (AND); Materialize instance inherits tags; `ListTags` scope (USER sees no
  other user's personal tags; ADMIN same), counts, open-only, teamId.
- `httpapi`: `GET /api/tags` 401 unauthenticated, shape with `tags: []`
  non-null; `GET /api/search?tag=A&tag=b` normalises; `?tag=a b` → 400.
- `go vet ./... && go test -race ./...` green.

UI: `npm run build` (tsc) + `npm run lint` clean; `test/tags.test.ts`
for `normalizeTag`.

E2E (browser, scratch instance on :18485 + schema `taskman_tags_e2e`, admin
seeded by env, run from this worktree so prod's `ui/dist` is untouched):
create via `#tag` in composer → chips on Day row → TaskDetail add/remove →
filter on Day/Week/Month/All/Search/Backlog/Attention/Team board/History →
filter persists across tabs → clear → `#daily` still a horizon → invalid
tag rejected in detail with the server message → `GET /api/tags` counts.

## Accepted consequences (deliberate, don't "fix")

- `#foo` in a title is now a tag, not title text.
- Tags are case-folded: `Backend` and `backend` are the same tag.
- No tag colours, no rename, no autocomplete in the detail input (known
  tags are one click away in the filter bar).
- The filter is global, not per-view: filtering on Day also filters the
  team board until cleared. The bar is always visible when a filter is on.
- `staleOpen` on the board and the sidebar attention badge ignore the
  filter.
- The known-tags bar on Day/Week/Month includes tags that exist only on
  team tasks. Those views are personal-only, so such a pill yields an empty
  list — the count shown is the scope-wide open count, not the view's.
- List fetches are not sequenced: a fast filter toggle can briefly show a
  stale response until the next change (the same pre-existing pattern as
  date changes).

## E2E results (run 2026-10-01, commit d8928d7)

Headless agent-browser against the branch binary on :18485, schema
`taskman_tags_e2e` (fresh, migrations 0001+0002 applied at boot —
`schema_migrations.version = 2`, `tags jsonb NOT NULL DEFAULT '[]'` + GIN
`tasks_tags` present), admin seeded by env, `ui/dist` from this worktree.
Fixtures: team "Platform", member Uma (login enabled → USER).

**Verdict: PASS — no tags defects found.** 347 requests served, zero 5xx,
zero panics, zero FSM errors; every 4xx is an intended one (5 tag-validation
400s on PATCH, 1 on history `?tag=a%20b`, unauthenticated 401s, 2
pre-existing USER `GET /api/members` 403s from App.tsx's sidebar count —
not touched by this diff). 27 UI requests carried `tag=`; zero browser
console errors in either session.

| # | Result | Evidence |
|---|---|---|
| 1 | **PASS** | Day composer "Fix login #backend #Urgent" → row chips `backend`,`urgent` (lowercased), title "Fix login". "Standup notes #daily" → daily, `tags: []`; "Morning check #daily #ops" typed in the **Week** composer → `horizon daily` + `tags [ops]` (horizon override intact). `GET /api/tags` → `{"tags":[{"tag":"backend","count":2},{"tag":"urgent","count":2}]}`; later ordering verified count desc / tag asc (`chores 3, home 3, backend 2, …`). Overflow: 5 tags → `backend frontend blurtag +2` (`chip.tag.more`). |
| 2 | **PASS** | TaskDetail: Enter adds `frontend`; real `,` keydown adds `infra`; × on `urgent` removes; Backspace on empty removes last (`infra`); blur with pending text adds `blurtag`. `a b` → inline `invalid tag "a b"`, text kept in input, task unchanged (server PATCH 400, DB tags unchanged). `Backend` (dup after fold) → no request, no duplicate chip, input cleared. 31 chars → `tag "abcdefghijabcdefghijabcdefghijk" is longer than 30 characters`. |
| 3 | **PASS** | Second personal task `#docs`; weekly "Plan sprint #planning" → `weekly 2026-W40 [planning]`; monthly "Quarterly review #planning #finance" → `monthly 2026-10`; expanded Month composer shows `div.composer-tags` chips `finance`,`q4` and "Create task" persists them; Backlog composer "Research caching #idea #backend" → `horizon backlog, period "" , [idea, backend]`; team composer → team task `[backend, urgent]`; member-row QuickAdd "Deploy pipeline #infra #weekly" → preview `qa-chip tag:#infra` + horizon chip, created with `assigneeId` Uma, `[infra]` (horizon forced daily — existing DESIGN_V3_TEAMS deviation #3, not a regression). |
| 4 | **PASS** | Day pill `#backend` → only matching row, request `views/day?date=…&tag=backend`; + `#docs` → `&tag=backend&tag=docs`, AND → "No tasks match the tag filter · clear"; `tag-empty-clear` restores all rows and removes the localStorage key. With `#planning` active the bar is present + active on Day (`pl-view`; today bucket empty-state, week-context bucket filtered), Week, Month (`pl-view`), All tasks (`all-tasks-toolbar-right`), Search (`pl-toolbar`), Backlog (empty-state), Attention (`attention-view`, bar + empty-state though no items), Team board (`tp-tagfilter`, stale pill `#planning 0` shown active + empty-state), History (`hi-tagfilter`); each fetch carried `tag=planning` (`/views/week`, `/views/month`, `/search?status=open`, `/search`, `/backlog`, `/views/attention`, `/teams/{id}/board`). Clear from Search → no active pill on Day/Week/Month/All/Backlog/Attention/Team. Filter survives a full page reload (`taskman-tagfilter=["docs"]`). Attention with real (overdue) items: `#home` keeps both, `#home`+`#docs` → empty-state, clear restores. |
| 5 | **PASS** (fixture) | `weekOf` is stamped to the current week at create, so history needs a past week: two team tasks completed (one via the board's Mark done, one via PATCH), then `week_of` backdated to `2026-W39` **in the scratch schema** (no clock change). History unfiltered → both; pill `#infra` → only "Old infra job", request `history?offset=0&limit=50&tag=infra`. Curl: `?tag=docs` → docs job, `?tag=Infra&tag=legacy` → normalised match, `?tag=infra&tag=docs` → `[]`, `?tag=a%20b` → 400 `invalid tag "a b"`. |
| 6 | **PASS** (fixture) | "Water plants *daily #home #chores" → series head `[home, chores]`. Materialize spawns only up to the *current* period (navigating Day to tomorrow spawns nothing, by design), so the head was aged to 2026-09-29 in the scratch schema as in the PG E2E; next view load spawned 09-30 and 10-01, both `["home","chores"]`, same series. PATCHing the 10-01 instance to add `garden` left the head and 09-30 unchanged (no slice aliasing). |
| 7 | **PASS** | Day row Delete on "Write docs" (`[docs]`) → toast Undo → `DELETE 200` then `POST /api/tasks/restore 200`; same id `63b3964a…`, `tags ["docs"]`, chip back on the row. |
| 8 | **PASS** | Uma (USER, temp password → forced change) sees only `#infra` (her team) — none of the admin's personal tags. She creates "Private errand #secretuser"; her `/api/tags` lists it. As ADMIN: `GET /api/tags` (curl and in-page fetch) and the Day filter bar have no `secretuser`; `GET /api/search?tag=secretuser` → `{"tasks":[]}`. `?teamId=<Platform>` → `[infra]` only; unknown teamId → `{"tags":[]}`. Done/cancelled excluded: `urgent` and `docs` counts dropped when their tasks completed. |
| 9 | **PASS** | Server log: 347 requests, 0 × 5xx, 0 panic, 0 fsm. Note the request log prints path only (no query string), so `tag=` was counted from the browser network log instead: 27 requests with `tag=`. |
| + | PASS | Contract API edges: PATCH `["  Docs ","docs",""]` → `[docs]`; key absent → unchanged; `null` → `[]`; `[]` → `[]`; `a#b`, `a,b` → 400 `invalid tag …`; 21 tags → 400 `too many tags (max 20)`; `search?tag=&tag=DOCS` → blank dropped, `docs` matched. |

**Bugs found:** none in the tags contract.

**Observations (minor UX, not filed as defects — orchestrator's call):**
1. *History is unreachable from the board while a filter hides every
   completed-this-week team task.* The only entry point is the "View older"
   footer inside `CompletedFold`, which returns null when its (filtered)
   list is empty. Repro: Team board, select a tag no current-week completed
   team task carries → the fold and footer disappear; the user must clear
   the filter, open History, then re-select. Pre-existing entry-point
   design, but the global filter makes it reachable much more often.
2. *History's pills come from `/api/tags?teamId=` (open tasks only, by
   decision 5)*, so tags that exist only on completed history rows (e.g.
   `legacy`, `docs` here) are not offered on the History page itself; they
   can still be applied from another view (the filter is global).

Driver notes (not app issues): server log has no query strings (use
`ab network requests`); `ab type` uses insertText, so the `,` keydown path
needs `ab press ","`; row hover actions replace the meta cluster (chips),
so `ab click` on Mark done/Delete needs an `ab hover` on the row first
(otherwise "covered by span.chip.tag"); sidebar "Attention" text carries a
badge count. Fixtures written directly to the scratch schema: `week_of`
backdate (2 rows, item 5), recurring head period/anchor backdate (1 row,
item 6). Schema `taskman_tags_e2e` left in place for the orchestrator to drop.
Screenshots: `/tmp/tags-1-day-chips.png`, `/tmp/tags-1-overflow.png`,
`/tmp/tags-2-invalid-tag.png`, `/tmp/tags-3-composer-expanded.png`,
`/tmp/tags-3-quickadd-preview.png`, `/tmp/tags-4-day-filter-backend.png`,
`/tmp/tags-4-day-and-empty.png`, `/tmp/tags-4-day-planning.png`,
`/tmp/tags-4-teamboard-stale.png`, `/tmp/tags-4-attention-empty.png`,
`/tmp/tags-5-row-hover.png`, `/tmp/tags-5-history-infra.png`,
`/tmp/tags-6-recurrence-instance.png`, `/tmp/tags-8-user-secret.png`.

### Re-check (run 2026-10-01, commit d33a17b)

Review fixes only. Same binary setup on :18485, the same scratch schema
`taskman_tags_e2e` (data carried over from the run above), headless
agent-browser. **Verdict: PASS (6/6).** Server log: 93 requests, 0 × 5xx,
0 panics, 0 fsm. The only 4xx were the intended ones: 2 unauthenticated
401s, the `status=bogus` 400, the QuickAdd 400 and the `a b` PATCH 400.

| # | Result | Evidence |
|---|---|---|
| 1 | **PASS** | Day composer `Ship it #release, now` → `POST 201`, title "Ship it now", `tags [release]`. `Done #daily.` → `POST 201`, horizon daily, `tags []`. |
| 2 | **PASS** | Grouped board, "Add for Uma…" row, `Too long #abcdefghijklmnopqrstuvwxyz12345` + Enter → `POST /api/tasks 400`, inline `tag "abcdefghijklmnopqrstuvwxyz12345" is longer than 30 characters`, and the input still holds the full typed text. |
| 3 | **PASS** | The History tag bar lists `#legacy 1` (legacy is on a completed task only and does not appear among the Day/open pills) next to backend/docs/infra/urgent. Request: `GET /api/tags?teamId=…&status=closed`. Clicking `#legacy` → `history?offset=0&limit=50&tag=legacy` → only "Old infra job". `curl /api/tags?status=bogus` → 400 `{"error":"status must be open or closed"}`. `status=closed` → `{backend,docs,infra,legacy,urgent}`. |
| 4 | **PASS** | TaskDetail "Ship it now": click the tag input, type `foo` + Enter, then `bar` + Enter typed through the keyboard only, with no re-click. `activeElement` stayed `.td-tags-input` and the chips read `release, foo, bar` (2 × PATCH 200). `a b` + Enter → inline `invalid tag "a b"`, text kept, PATCH 400. Blur by clicking Description (focus moved to the TEXTAREA) → PATCH count for the task stayed 3 in both the server log and the browser network log. DB tags `[release, foo, bar]`. |
| 5 | **PASS** | Flat board, `#infra` filter (the only completed-this-week team task lacks it): the completed fold is gone and a standalone `View older` button shows below the list. Clicking it opens History (`history?…&tag=infra`). Header `tp-open-label` reads "1 open · filtered" with only the tag filter active. |
| 6 | **PASS** | Create more on. Collapsed `B2 #x` + Shift+Enter → expanded with `composer-tags` chip `x`. Create task → `B2 [x]`; after create the composer stays expanded with no chips. Next `C2 plain` → `tags []`. Note: text typed **inside** the expanded title (`A #x`) is taken literally by design (`create()` comment, "no token re-parsing while expanded") → title "A #x", `tags []`. This behaviour predates the fixes and is not a regression. |

Screenshots: `/tmp/tags-recheck-2-quickadd-400.png`,
`/tmp/tags-recheck-3-history-legacy.png`, `/tmp/tags-recheck-4-invalid.png`,
`/tmp/tags-recheck-5-flat-viewolder.png`, `/tmp/tags-recheck-6-after-create.png`.
New rows in the scratch schema: "Ship it now", "Done", "A #x", "B2", "C2 plain".
