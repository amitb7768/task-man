# v11 — Tag catalog (admin-managed tags) (contract)

Decided 2026-10-01, on top of v10 (`DESIGN_V10_TAGS.md`). User ask: "add a
Tags menu on the left where the admin can create new tags; admin and
members add those tags to existing or new tasks; filtering on those tags;
this limits tag creation and makes usage consistent."

Orchestrator decisions (locked):

1. **A catalog table is the source of truth.** `tags(name)` rows are
   created only by ADMIN on the new Tags page. A task may carry only
   catalog tags: `validateTaskFields` (create, patch) rejects any newly
   added tag not in the catalog with 400 `unknown tag "x"` — for
   everyone, ADMIN included; `RestoreTasks` instead strips unknown tags
   (Warn log with task id + dropped tags) so Undo never fails permanently (the catalog page is the one creation point;
   `#newtag` in a composer is no longer a way to mint a tag).
2. **Migration 0003 seeds the catalog** from the distinct tags already on
   tasks, so no existing task becomes invalid.
3. **Deleting a catalog tag is refused while any task carries it** (409
   `tag "x" is in use by 1 task` / `by N tasks`). No rename, no merge (YAGNI; the admin
   deletes an unused tag and creates another).
4. **The filter bar lists the catalog**, not "tags in use": `GET /api/tags`
   now returns every catalog tag with the caller-scoped open (or closed)
   count, count 0 included, ordered name asc. Catalog names are global and
   admin-authored, so listing them to USERs leaks nothing; the counts stay
   scope-bound exactly as in v10. The "stale pill" case disappears
   (a selected tag that was deleted is dropped from the stored filter on
   next load).
5. **Picker, not free text:** the TaskDetail tag input gains
   `<datalist>` of catalog names (native rung). Enter/comma still add; the
   server's 400 shows inline for an unknown name. Composer `#tag` stays as
   the fast path; an unknown tag fails the create with the server message
   in the existing error surface (no silent drop).
6. **Nav:** "Tags" entry in the left nav, ADMIN-only (same gate as
   Backlog), placed after Teams. The page is a plain list: name, open-task
   count, delete (×, disabled with a tooltip while in use), and a create
   input at the top (Enter adds; normalised by the server; 409 on
   duplicate shown inline).

---

## Data model

`internal/repo/migrations/0003_tag_catalog.up.sql`:

```sql
CREATE TABLE tags (
    name       TEXT COLLATE "C" PRIMARY KEY,
    created_by TEXT REFERENCES members(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL
);
-- Seed from what tasks already carry so nothing becomes invalid.
INSERT INTO tags (name, created_at)
SELECT DISTINCT x.tag, now() FROM tasks t, jsonb_array_elements_text(t.tags) AS x(tag)
ON CONFLICT DO NOTHING;
```

`model.Tag{Name string json:"name"; CreatedBy *string json:"createdBy,omitempty"; CreatedAt time.Time json:"createdAt"}`,
`TableName() = "tags"`. `model.TagCount` unchanged (`tag`, `count`).

## Service

- `validateTaskFields`: after `NormalizeTags`, if `len(t.Tags) > 0`,
  `SELECT name FROM tags WHERE name IN ?` inside the same tx/db handle and
  reject the first missing one: `badRequest("unknown tag %q", missing)`.
  The lookup is `FOR SHARE`, so inside the patch/restore tx it serialises
  against `DeleteTag`'s `FOR UPDATE` (on the autocommit create path the lock
  is released at once — accepted).
  On PATCH only tags not already on the loaded row (`orig.Tags`, passed as
  `keepTags`) are checked, so a task carrying an orphan tag stays editable
  and keeping or removing a tag never fails.
  `RestoreTasks` runs the same lookup (it already normalises) but strips
  unknown tags and logs a Warn (task id + dropped tags) instead of a 400.
- `ListTags(ctx, teamID *string, closed bool)` → catalog LEFT JOIN scoped
  counts:

```sql
SELECT g.name AS tag, count(t.id) AS count
FROM tags g
LEFT JOIN (SELECT id, tags FROM tasks WHERE <scope AND status cond AND team cond>) t
  ON t.tags @> to_jsonb(ARRAY[g.name])
GROUP BY g.name ORDER BY g.name ASC
```
  (or an equivalent with `jsonb_array_elements_text`; the shape of the
  result is what matters: every catalog tag, count ≥ 0).
- `CreateTag(ctx, name) (*model.Tag, error)`: ADMIN only (service-level
  check like other admin flows, plus `requireAdmin` at the route);
  `NormalizeTags([]string{name})` → exactly one tag else 400; insert;
  unique violation → 409 `tag "x" already exists`.
- `DeleteTag(ctx, name)`: ADMIN only; count tasks with `tags @> ?`; >0 →
  409 `tag "x" is in use by 1 task` / `by N tasks`; else delete; 0 rows → 404.
- `ListCatalog(ctx) ([]model.Tag, error)` — plain list, name asc (the
  Tags page uses `GET /api/tags` for counts; this one is for the picker).

## HTTP

- `GET /api/tags` (requireAuth) — unchanged shape, catalog-backed (see
  above). `?status=open|closed`, `?teamId=` as in v10.
- `GET /api/tags/catalog` (requireAuth) → `{"tags":[{name, createdBy?,
  createdAt}]}` never null.
- `POST /api/tags/catalog` (requireAdmin) body `{"name": "..."}` → 201
  `{name, createdAt, createdBy}`; 400 invalid; 409 duplicate.
- `DELETE /api/tags/catalog/{name}` (requireAdmin) → 204; 404 unknown;
  409 in use.
- `docs/AUTH_FEATURES.md` matrix rows for the three catalog routes;
  `internal/httpapi/CLAUDE.md` route note.

## UI

- `api.ts`: `Tag` type, `api.tagCatalog()`, `api.createTag(name)`,
  `api.deleteTag(name)`. `api.tags()` unchanged.
- `App.tsx`: `NavKey` + `"tags"`; nav entry `{key:"tags", name:"Tags",
  icon:"tag"}` after Teams, filtered by `user.systemRole === "ADMIN"` like
  backlog; `metaByTab.tags = { eyebrow: "Admin", title: "Tags" }`; render
  branch `<TagsView/>`. A small tag icon in the existing inline-SVG style.
- `views/TagsView.tsx` (new) + `styles/tags-page.css`: create input at top
  (`input.tags-create-input[aria-label="New tag"]`, Enter submits; inline
  error `div.tags-create-error`), then a list of rows
  (`li.tags-row[data-tag=...]`): `#name` chip, mono count "N open", delete
  button `button.tags-delete[aria-label="Delete tag X"]` disabled with
  `title="In use by N tasks"` when count>0 (server still enforces). Uses
  `api.tags()` for counts + `api.tagCatalog()`; after create/delete reload
  both and call `notifyTasksChanged()` so filter bars refresh.
  Empty state: "No tags yet. Create the first one above."
- `TaskDetail` TagsEditor: `<datalist id="tag-catalog">` fed by
  `api.tagCatalog()` (loaded once per panel open), input gets
  `list="tag-catalog"`. Placeholder "Pick a tag…". Already-applied tags are
  filtered out of the datalist options.
- `TagFilter`: unchanged markup; since `/api/tags` now returns the whole
  catalog, the component drops any stored selection not in the response
  (replaces the v10 stale-pill rendering). Pills with count 0 render
  normally (muted count).
- Composer/QuickAdd: no change beyond error surfacing — verify that a 400
  from createTask reaches the user in each composer context (fastCreate,
  createFromDraft, create, QuickAdd) and, where it was previously swallowed,
  show it via the existing error/toast surface of that component.

## Tests

- Go: migration seeds existing tags (insert a task with tags via SQL in a
  scratch schema at version 2? — simpler: `TestMigrate` subtest creates a
  task with tags on a migrated schema, then asserts the tag exists in
  `tags` after re-running Migrate is a no-op; plus a direct test that the
  seed SQL is idempotent); create/patch/restore reject unknown tags and
  accept catalog tags; `CreateTag` normalises + 409 duplicate + USER
  forbidden; `DeleteTag` 409 in use / 204 / 404; `ListTags` returns count-0
  catalog entries and still hides other users' personal counts; http:
  routes, auth, shapes.
- UI: `npm run build`, `npm test`, `npm run lint`.
- E2E (browser, scratch schema, :18485): admin creates tags on the Tags
  page (duplicate → inline 409); USER cannot see the nav entry and gets 403
  on POST; task detail picker offers catalog tags, unknown tag → inline
  error; composer `#unknown` → visible error, `#known` works; delete in-use
  tag refused, unused tag deleted and disappears from the filter bar and
  picker; filter bar shows count-0 tags; seed: a task tagged before the
  migration keeps its tag and the tag appears in the catalog.

## Accepted consequences

- Admins must create a tag before anyone can use it; `#typo` on a composer
  now fails the create instead of minting a tag.
- No rename/merge; delete only when unused.
- Undo/restore of a task whose tag was deleted from the catalog in the
  meantime brings the task back without that tag (logged), rather than
  failing.
- The catalog is global (not per team).

## E2E results (run 2026-10-01, commit 604780e)

Headless agent-browser (two sessions: default = ADMIN, `catalog-user` =
USER Uma) against a binary built from **HEAD 604780e** (built 16:16, before
the uncommitted review fixes landed in this worktree at 16:21+; `ui/dist`
16:08), port :18485, schema `taskman_catalog_e2e` (fresh; boot migrated it
to `schema_migrations.version = 3`, `tags` present + empty; note the boot
log prints no migration lines — version read from SQL). Admin seeded by env;
fixtures: team "Platform", member Uma (login enabled → USER). Viewport set
to 1280×900 (see driver notes).

**Verdict: PASS on all 8 checklist items.** Two behaviours of 604780e worth
the orchestrator's attention (B1, B2 below) — both are already addressed by
the uncommitted worktree changes (keepTags on PATCH, strip-on-restore,
"1 task" wording), which this run did **not** exercise.

| # | Result | Evidence |
|---|---|---|
| seed | **OBSERVED** | Tags page created `seedtag`; Day composer `Seed task #seedtag` → chip. Server stopped, `DELETE FROM tags`, restarted: migration no-op (version 3), catalog stays empty (seed only runs inside 0003 — expected). The orphan-tag task still **renders** with its `seedtag` chip on Day, but the filter bar is absent (`/api/tags` → `{"tags":[]}`), the Tags page doesn't list it. Any PATCH on the task — title rename *and* status change — → 400 `unknown tag "seedtag"` (B1). Re-running the 0003 seed `INSERT … SELECT DISTINCT` by hand restored the row → pill `#seedtag 1` back, PATCH 200. |
| 1 | **PASS** | Empty state "No tags yet. Create the first one above." Created `backend`, `ops`, `CI/CD` → rows `backend`,`ci/cd`,`ops` (name asc, normalised), each "0 open", input cleared. `OPS` → inline `div.tags-create-error[role=alert]` `tag "ops" already exists` (POST 409), text kept. `a b` → inline `invalid tag "a b"` (POST 400). Typing clears the error. |
| 2 | **PASS** | Day filter bar: `#backend 0`, `#ci/cd 0`, `#ops 0`, each count `span.tag-pill-count.zero`. |
| 3 | **PASS** | Day composer `Fix login #backend` → POST 201, row chip `backend`. `Fix #nosuch` → POST 400, `.composer-error-row .error` `unknown tag "nosuch"`, input keeps `Fix #nosuch`. Bonus: expanded composer (Shift+Enter, chip `nosuch2`, Create task) → 400 shown inline. Team "Platform", grouped board: "Add for Uma…" `Deploy #ops` → created with chip `ops`, assignee U; `X #nosuch` → `div.error` (child of `.team-page`, under the header) `unknown tag "nosuch"`, text retained; team composer `Team composer #nosuch` → its own `.composer-error-row` error. |
| 4 | **PASS** (Enter) | Detail of "Fix login": `input.td-tags-input[list=tag-catalog]`, placeholder "Pick a tag…", datalist `[ci/cd, ops]` (applied `backend` excluded). Type `ops` + Enter → PATCH 200, chips `backend, ops`, datalist `[ci/cd]`. `zzz` + Enter → PATCH 400, `div.td-tags-error` `unknown tag "zzz"`, text kept. Mouse-pick of a suggestion (simulated: value set + `InputEvent insertReplacementText`, since headless Chrome's datalist popup is not in the DOM) only fills the input — no PATCH for 1.2 s; it was committed by the next blur (the existing v10 blur-add), not by the selection itself. So a mouse pick needs Enter or a blur. |
| 5 | **PASS** | Tags page: `backend 1 open`, `ci/cd 1 open` (from the blur in #4), `ops 2 open`; every delete disabled with `title="In use by 1 task"` / `"In use by 2 tasks"`. Mark done on "Fix login" → `backend 0 open`, delete enabled (`title="Delete tag"`); click → DELETE 409, `div.tags-list-error` `tag "backend" is in use by 1 tasks` (no confirm dialog; row stays). Detail of the completed task → × `backend` → PATCH 200; Delete → 204, row gone; Day pills `[ci/cd, ops]`; detail datalist no longer offers `backend`. |
| 6 | **PASS** | Uma (temp password → forced change): nav `Day Week Month All tasks Attention Teams Search` — no Tags (no Backlog either). Her cookie: `POST /api/tags/catalog` → 403 `{"error":"admin only"}`, `DELETE /api/tags/catalog/ops` → 403 `admin only`, `GET /api/tags/catalog` → 200 with both names. Admin creates personal open "Admin private" `[ci/cd]`: ADMIN `/api/tags` → `ci/cd 1, ops 1`; USER → `ci/cd 0, ops 1` (ops = her assigned team task). Uma creates "Uma errand", picker datalist `[ci/cd, ops]`, types `ci/cd` + Enter → chip; her `/api/tags` → `ci/cd 1`; admin's stays 1. |
| 7 | **PASS** | `#ops` selected on Day → `taskman-tagfilter=["ops"]`; switch to Week/Month → still active, requests carry `tag=ops`. Created `temp`, selected it → `["ops","temp"]`. Tags page delete `temp` → 204 (row gone; storage still `["ops","temp"]` while on the Tags page, which has no filter bar). Back to Day → storage `["ops"]`, pills `[ci/cd, ops✓]`; Week same. Network: the first Day fetch after the delete still carried `tag=ops&tag=temp` (200), immediately followed by `tag=ops` — a transient stale fetch, same unsequenced-fetch pattern accepted in v10. |
| 8 | **PASS** | 262 requests, 0 × 5xx, 0 panics. All 4xx intended: tag-catalog 400/403×2/404/409×3, POST /tasks 400×4 (unknown tag), PATCH 400×3 (`zzz` + 2 orphan probes), 401×4 unauthenticated, 403×3 USER `GET /api/members` (pre-existing sidebar call, as in v10), 415×4 = my curl DELETEs without `Content-Type` (driver error). |

**Bugs / findings (604780e):**

- **B1 — a task carrying a tag missing from the catalog is frozen.**
  Repro: task with tag `x`; remove the `tags` row for `x` out-of-band (or
  any path that orphans it); `PATCH /api/tasks/{id}` `{"title":"…"}` or
  `{"status":"in_progress"}` → 400 `unknown tag "x"`. The UI cannot even
  complete it; removing the chip is the only way out (that PATCH no longer
  carries `x`). By contract the orphan state is unreachable through the app
  (seed + delete refusal), so severity is low; the worktree's uncommitted
  `keepTags` change targets exactly this.
- **B2 — Undo/restore after the tag was deleted fails.** Repro (API):
  create tag `t2`, task `[t2]`, `DELETE /api/tasks/{id}`, `DELETE
  /api/tags/catalog/t2` → 204 (now unused), `POST /api/tasks/restore
  {"tasks":[…]}` → 400 `unknown tag "t2"` — the deleted task is
  unrecoverable. Matched the 604780e contract (decision 1) but the
  updated "Accepted consequences" now say restore strips the tag instead;
  re-test once that lands.
- **Minor copy:** 409 reads `is in use by 1 tasks` (also fixed in the
  worktree). Tags-page row says "0 open" while delete is refused because a
  *closed* task carries the tag — the 409 explains it, but the row gives
  no hint before the click.

**Observations (not defects):** QuickAdd's "WILL CREATE" preview renders
`#nosuch` as a normal tag chip before submit (unknown only on 400). Tag
delete has no confirmation step (fine: refused while in use). Catalog
`createdAt` serialises as UTC `Z` while task timestamps carry `+05:30`.

**Driver notes:** default agent-browser viewport is 1280×577, at which the
sidebar footer overlaps the Tags nav item (click intercepted by
`div.sidebar-footer`) — set `ab set viewport 1280 900` (screenshot
`/tmp/catalog-0-short-viewport-nav.png`; worth a look as a short-window
layout issue, nav not scrollable under the footer). Mid-run the default
session came back on `about:blank` with a reset viewport and empty
localStorage (re-logged in; #7 was run fully in the new page). Datalist
popups are native and not scriptable — selection simulated as described in
#4. `ab type` = insertText. Row hover actions need `ab hover @row` before
Mark done. DELETE routes need `Content-Type: application/json` from curl
(415 otherwise). Fixture SQL in the scratch schema: `DELETE FROM tags`
(seed step) and a manual re-run of the 0003 seed INSERT. Schema
`taskman_catalog_e2e` left in place.
Screenshots: `/tmp/catalog-0-seed-orphan.png`,
`/tmp/catalog-1-empty.png`, `/tmp/catalog-1-dup-409.png`,
`/tmp/catalog-1-invalid-400.png`, `/tmp/catalog-2-day-zero-pills.png`,
`/tmp/catalog-3-composer-unknown.png`, `/tmp/catalog-3-expanded-unknown.png`,
`/tmp/catalog-3-quickadd-unknown.png`, `/tmp/catalog-3-team-errors.png`,
`/tmp/catalog-4-detail-unknown.png`, `/tmp/catalog-5-tags-counts.png`,
`/tmp/catalog-5-delete-409.png`, `/tmp/catalog-5-after-delete-detail.png`,
`/tmp/catalog-6-user-nav.png`, `/tmp/catalog-6-user-picker.png`,
`/tmp/catalog-7-temp-selected.png`, `/tmp/catalog-7-temp-dropped.png`.

### Re-check (run 2026-10-01, commit 7866d3a)

Targeted browser re-check of the review fixes only. Binary
`/tmp/taskman-catalog-e2e` and `ui/dist` both rebuilt 16:27 at 7866d3a
(previous run: binary 16:16, `ui/dist` 16:08; new bundle
`index-DsjJmW1x.js`). Port :18485, schema `taskman_catalog_e2e` reused with
the previous run's data; log `/tmp/taskman-catalog-e2e-2.log`. The admin's
password had been changed during the previous run and is not recorded, so
the scratch admin's `password_hash` was reset by SQL to a known bcrypt hash
(scratch schema only).

**Verdict: PASS on all 6 checks.** B1, B2 and the plural copy are fixed.

| # | Result | Evidence |
|---|---|---|
| 1 orphan tag | **PASS** | SQL `tags='["orphanx"]'` on "Admin private". Title rename in detail → PATCH 200 (×2), row Mark done → PATCH 200; DB `status=done`, `tags=["orphanx"]`, chip still shown. Picker `nope` + Enter → PATCH 400, `.td-tags-error` `unknown tag "nope"`, text kept, chips `[orphanx]`. Catalog tag `ops` → PATCH 200, DB `["orphanx","ops"]`. |
| 2 undo after tag deletion | **PASS** | Created tag `t3`, Day composer `Undo task #t3` (201). Row hover → Delete (DELETE 200, toast), Tags page delete `t3` → 204 at 16:30:55; toast still visible → clicked Undo at 16:31:01 → `POST /api/tasks/restore` 200; DB task back with `tags=[]`; log `WARN restore: dropped tags not in the catalog task=da1d1910… tags=[t3]`. |
| 3 plural | **PASS** | Tag `solo` on one (completed) task → Tags-page delete → 409 `.tags-list-error` `tag "solo" is in use by 1 task`. Control: `ci/cd` (3 tasks, one private to Uma) → `is in use by 3 tasks`. |
| 4 tooltip | **PASS** | Enabled delete buttons (`ci/cd`, `solo`) `title="Delete tag (refused if any task, including closed or private ones, still uses it)"`; disabled `ops` keeps `"In use by 1 task"`. |
| 5 datalist pick | **PASS** | Simulated pick (native value setter + `InputEvent insertReplacementText` `ci/cd`) → PATCH 200 within 1.2 s with the input still focused (no blur, no Enter), chip added, input cleared. Typed `o`,`p`,`s` one char at a time: no add after `o`/`op`, chip `ops` added and input cleared on `s`. |
| 6 log | **PASS** | 111 lines, 0 × 5xx, 0 panics. 4xx: 401 initial `/me`, PATCH 400 (`nope`), DELETE 409 ×2 (solo, ci/cd) — all intended. |

Screenshots: `/tmp/catalog-r1-nope.png`, `/tmp/catalog-r5-pick.png`,
`/tmp/catalog-r2-undo.png`, `/tmp/catalog-r3-plural.png`. Schema
`taskman_catalog_e2e` left in place (now also holds tag `solo` and task
"Undo task").
