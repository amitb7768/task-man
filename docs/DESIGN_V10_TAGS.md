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
