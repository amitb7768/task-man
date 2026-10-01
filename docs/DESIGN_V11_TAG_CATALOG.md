# v11 — Tag catalog (admin-managed tags) (contract)

Decided 2026-10-01, on top of v10 (`DESIGN_V10_TAGS.md`). User ask: "add a
Tags menu on the left where the admin can create new tags; admin and
members add those tags to existing or new tasks; filtering on those tags;
this limits tag creation and makes usage consistent."

Orchestrator decisions (locked):

1. **A catalog table is the source of truth.** `tags(name)` rows are
   created only by ADMIN on the new Tags page. A task may carry only
   catalog tags: `validateTaskFields` (create, patch) and `RestoreTasks`
   reject any tag not in the catalog with 400 `unknown tag "x"` — for
   everyone, ADMIN included (the catalog page is the one creation point;
   `#newtag` in a composer is no longer a way to mint a tag).
2. **Migration 0003 seeds the catalog** from the distinct tags already on
   tasks, so no existing task becomes invalid.
3. **Deleting a catalog tag is refused while any task carries it** (409
   `tag "x" is in use by N tasks`). No rename, no merge (YAGNI; the admin
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
  `RestoreTasks` does the same check (it already normalises).
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
  409 `tag "x" is in use by N tasks`; else delete; 0 rows → 404.
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
- The catalog is global (not per team).
