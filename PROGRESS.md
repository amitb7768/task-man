# taskman — progress log

## Session 5 (cont.) — 2026-07-10 (collapse completed in place)

User wanted done tasks to stop cluttering active views (asked for separate todo/
in-progress/done tabs). Steered the mechanism: NOT status tabs (would flatten the
day/week/month/time axis + split in-progress from todo) → collapse-completed-in-place,
user agreed. Spec: `docs/DESIGN_V5_COMPLETED_FOLD.md`. UI-only, no backend.
- Sonnet build: new `CompletedFold` component (exports canonical `isCompletedStatus`;
  active=todo|in_progress, completed=done|cancelled; collapsed "N completed ▸"
  disclosure, default closed, not persisted). Day/Week/Month render active-only + a
  fold (Day: bottom of primary list; Week/Month: one fold aggregating all completed at
  view bottom; context strips active-only). Team board: per-member fold (grouped) / one
  fold (flat), header status chips + bulk selection UNCHANGED (read full dataset; fold
  gates visibility only). All-tasks/Attention/Search untouched.
- Verified WITHOUT a full review wave (low-risk presentational): partition exhaustive by
  construction (single isCompletedStatus, active=!completed); team counts/selection read
  full board not the active subset; tsc/build green; bundle index-BKYiU99A.js live;
  index.css + server/ untouched. USER: refresh :8484 to confirm visually.

## Session 5 — 2026-07-10 (macOS app: Tauri v2 thin-client wrapper)

User chose to package taskman as a Mac app. Recommendation drove it: thin-client
WebView wrapper (reuses 100% of React + Go; a self-contained app was rejected — it
would fragment each Mac's data, breaking the shared team model just built). Decisions:
localhost default + settings override (same .app works for teammates on a LAN IP),
window-only v1 (menu-bar quick-add = fast-follow), unsigned/internal.

- Explore (sonnet): `docs/research-macapp.md` — confirmed ZERO frontend/server change
  (relative /api, same-origin, cookie fine in WebView); Rust was the only missing
  prereq. Installed rustup 1.97 (the one gate).
- Contract: `docs/MACAPP_PLAN.md`. Key design: window loads a bundled NATIVE SHELL
  (settings + connection-error/retry) at tauri://localhost, health-checks the server,
  then navigates the same window to the remote SPA — so settings/error UI exists even
  when the server is down, and the React app stays untouched.
- Build (sonnet): additive `src-tauri/` only (+ Makefile mac-dev/mac-build, README
  section). `cargo tauri build` → `src-tauri/target/release/bundle/macos/taskman.app`
  (adhoc/unsigned). NSAllowsLocalNetworking in Info.plist (plain-http to localhost +
  LAN IPs). Custom commands capability-scoped to the local shell only (remote origin
  can't invoke native) — verified against Tauri crate source. Default icons (favicon
  uses display-p3 the icon tool can't parse).
- Opus review: NO blocker/major. CRUX RESOLVED — login WILL work: window navigates
  fully to http://localhost:8484 so the SPA runs on the server's real origin, cookie
  isn't Secure, no incognito/ephemeral store → session survives the nav (and a settings
  round-trip). Capability scoping confirmed (remote origin can't invoke native). 5 minors.
  Orchestrator applied the 2 worthwhile ones directly + a doc caveat: health.rs IPv6
  bracket-strip + try-all-resolved-addrs (dual-stack); check_and_go re-validates scheme
  on the boot/retry path (deliberate defense-in-depth vs incidental host-check block);
  README ATS caveat (http only for localhost + RFC1918 LAN, not public IPs). Skipped:
  CSP (would need unsafe-inline on an already-XSS-safe static shell — theater),
  persist-before-check (intended), dead open_settings surface (harmless).
- Rebuilt: `cargo tauri build` green → taskman.app (9.8M, adhoc/unsigned, ATS
  NSAllowsLocalNetworking=true confirmed via PlistBuddy). ui/ + server/ untouched.

**State: macOS app COMPLETE.** Awaiting USER live confirmation: `make up` then open
`src-tauri/target/release/bundle/macos/taskman.app` (right-click→Open past Gatekeeper),
log in as admin@taskman.local. Only the live window render + real cookie round-trip
remain unverified here (no display / browser automation policy-blocked) — all reasoned
sound by the review.

Run: `make up` (server first) then `make mac-build` → open the .app (right-click→Open
if Gatekeeper balks), or `make mac-dev`.


## Session 4 — 2026-07-10 (v4 auth: BUILT, REVIEWED, FIXED, LIVE)

Stack brought back up (Docker daemon was down post-reboot; mongo + ./taskman-bin on
:8484, bundle BmAkmE2w). User green-lit the auth work. Research:
`docs/research-auth.md` (sonnet). Grilling closed 11 decisions — headline: UNIFY
members→users (credentials optional on member record), systemRole ADMIN|USER (string,
extensible; distinct from job-title `role`), tasks gain ownerId (tasks WIPED, no
backfill), personal tasks PRIVATE even from admin, USER = full edit in own teams
but DELETE admin-only, mustChangePassword on provision/reset, env-seeded first admin,
disabled-flag lifecycle, Mongo cookie sessions (sliding 12h, SameSite=Lax + JSON
guard, no CSRF tokens). Full contract incl. endpoint×role matrix + wave plan: `docs/AUTH_FEATURES.md`.

**v4 auth BUILT + REVIEWED + FIXED (2026-07-10).** User opted to WIPE tasks (no
ownerId backfill) — done. Wave A (backend sonnet): middleware seam, Mongo cookie
sessions (sliding 12h, SameSite=Lax, no CSRF tokens + jsonGuard), /api/auth/* +
admin enable-login/reset/disable, env-seeded admin, server-side scoping per matrix,
httptest + live verified. Wave B (UI sonnet): login + forced-change screens in design
system, /me boot + 401 dispatcher, sidebar identity/logout/change-pw, USER role gating
(no team/member mgmt, no delete anywhere incl. subtask delete), admin credential panel
(enable-login/reset with show-once temp pw, disable toggle). Wave C (OPUS security
review): found 1 BLOCKER + 2 MAJOR + 2 minor — all real, all live-proven:
  - BLOCKER: personal-task privacy leaked via unfiltered subtask CHILDREN + parentId
    injection into foreign private subtree.
  - MAJOR: USER could PATCH teamId→null to convert a team task to private, escaping
    admin oversight.
  - MAJOR: hard-delete of login-enabled users allowed (violated disable-don't-delete).
  - minor: forced-change users had no logout escape; login timing-enumeration oracle.
  Everything else (direct IDOR, foreign-team isolation, search/attention scoping,
  session fixation/invalidation, provisioning collisions, UI-vs-server gating) verified
  CLEAN. Sonnet fixer closed all 5 (children filtered via canAccessTask, parentId
  authorized on create/patch, USER blocked from any teamId transition, 409 on
  login-enabled delete + session cascade, logout exempt under mustChange + UI sign-out,
  dummy-bcrypt timing equalizer) + regression tests. Re-verified live on :8484 (fixed
  binary, pid 56747): BLOCKER child-leak → children:[], teamId→null → 403, login-enabled
  delete → 409. DB clean: 2 teams / 8 members (7 orig + seeded admin) / 0 tasks.

**Seeded admin:** admin@taskman.local / <the password you set — not committed> (CHANGE THIS via sidebar — it's
in agent logs). App live-gated at :8484 — provisioning teammates is now safe.

(v4 complete — see above.)

Multi-session workflow record. Newest session first. Each session ends with an entry
here: what was done, what's in flight, exact next steps.

## Session 3 — 2026-07-09 (Teams v3: feature definition + design prompt)

User verdict on v2: personal views good; Teams confusing — wants landing page +
per-team members/tasks page with member filtering and due-date tracking (extensible to
future member logins, not in MVP).

**Done:**
- Research (sonnet): `docs/research-team-views.md` — industry conventions (flat list
  default, avatar-chip member filter, never hide unassigned; anti-pattern = filter baked
  into layout, which is exactly the old board's flaw).
- Feature set closed with user: `docs/TEAMS_FEATURES.md`. Highlights: teams landing
  cards (avatars + open/overdue counts); team page = flat due-date list default +
  group-by-member toggle + avatar filter chips w/ workload counts; standing Unassigned
  bucket; status count chips; bulk bar w/ Reassign/Set-due/Done/Delete + undo; members
  side panel; horizon DE-EMPHASIZED on team tasks (default daily+today, detail-only);
  old member-columns board DELETED. Rejected: kanban, comments/mentions (login-dependent).
- Design prompt ready: `docs/DESIGN_PROMPT_TEAMS.md` (tokens verified against handoff —
  36/36 exact). Backend delta noted: GET /api/teams gains memberCount/openCount/
  overdueCount aggregation.

**Build (same day, from returned handoff `design_handoff_taskman_ui 2/` — v1 files
byte-identical + new 09/10 + updated README; screen 07 board explicitly superseded):**
- Contract: `docs/DESIGN_V3_TEAMS.md` (backend counts delta, file plan, 3 deliberate
  mock deviations: sidebar-only theme toggle; delete-team gated on ANY tasks per
  existing 409; team quick-add daily+today defaults with filtered-member auto-assign).
- Backend (sonnet): GET /api/teams gains memberCount/openCount/overdueCount
  (materialize-then-count, overdue matches Attention semantics). Live-verified.
- UI (sonnet): TeamsLanding + TeamPage (avatar filter chips w/ workload+overdue counts,
  flat due-sorted default w/ pinned Overdue, group-by-member toggle, always-last
  Unassigned, hover-reveal multi-select, bulk bar Reassign/Unassign/SetDue/Done/Delete
  all undoable, 452px members panel, localStorage team persistence). TeamsView +
  team-board.css DELETED. TaskRow gained additive assigneeAvatar prop.
- Opus review: NO blockers; undo machinery/nav/counts/filters all verified clean.
  2 MAJOR (closed tasks sorted above open; undated team rows showed fake "Today" chip
  from period fallback) + 4 minor + 2 hardenings → all 8 fixed by sonnet fixer,
  verified: closed-last sort, due chip from dueDate only + "No date" chip on team rows,
  live eyebrow counts, dismissToast() on new selection/team switch, typed member role,
  guarded bulk snapshots, exact-first @Name resolution, comment hygiene.

**State: Teams v3 COMPLETE.** :8484 live (v3 binary + bundle index-DZ3ryGlD.js).
Nothing committed to git. Pending user actions: acceptance pass on the new Teams flow;
skills extraction (user plans to capture workflow skills); git commit when ready.

**Task Composer (v3.1, in flight):** user verdict — team task creation "sucks" (tokens
undiscoverable, no description/due/assignee/priority controls). Research:
`docs/research-task-composer.md` (Linear pill-row is the fix; title-only required;
draft-preserving Esc; create-more carries defaults). Decisions: expandable quick-add
(in-place card, not modal), core fields + recurrence + create-more, ONE shared
component wired on team page first (personal views later). Tokens: collapsed fast-path
keeps parsing; expanding hydrates pills ONCE and cleans the title (no live sync).
Backend reviewed: NO api changes needed — POST /api/tasks covers all fields; composer
must honor recurrence→horizon coupling client-side. Design prompt:
`docs/DESIGN_PROMPT_COMPOSER.md` (deliverable 11).

**v3.1 BUILT + REVIEWED + FIXED (2026-07-09).** Handoff: "Taskman UI redesign (2)/11
Task Composer.dc.html" (only new artifact; no README §11 — mock + DESIGN_PROMPT_COMPOSER
are the spec). Contract: `docs/DESIGN_V31_COMPOSER.md` (incl. Phase-0 arch rules from
BACKEND_ARCH_REVIEW: creation only via POST /api/tasks; don't deepen teamId:null
assumption). Built: components/TaskComposer.tsx + styles/composer.css, wired on TeamPage
toolbar only (QuickAdd untouched for personal views; per-member add rows kept).
Builder's key calls: ISO weekday convention over mock's Sun-first demo data; interval
hidden for weekdays freq per mock. Opus review: payloads/state-machine/popovers/
ownership all clean; 1 MAJOR (Esc-preserved draft silently dropped by collapsed-Enter
fast path) + 3 minor + 1 cosmetic → all fixed: draft indicator chip ("N fields set") +
draft-aware Enter (tokens override draft fields), bare #horizon ignored (team always-
daily invariant restored), collapsed Esc clears whole draft, synchronous in-flight
guard, :has()-scoped toolbar alignment. All live-verified; bundle index-xkEuapNe.js.
Residual: popover stacking over task list reasoned-sound but not visually confirmed
(browser automation policy-blocked) — user to eyeball.

**Also this session:** `docs/BACKEND_ARCH_REVIEW.md` (opus, report-only) — verdict:
lazy-minimal architecture correct, auth is a seam-add not a rewrite; 3 gaps (no
middleware seam, no user/identity model, no handler/store tests); recommended shape =
Mongo cookie sessions + users collection + 2-role gating per endpoint matrix. Phase-0
rules active. NO fixes picked up yet per user instruction.

**v3.2 personal rollout COMPLETE (2026-07-09).** TaskComposer extended to Day/Week/Month
headers via discriminated-union `context` prop: personal = no assignee pill, #horizon
honored (recurrence still wins), creates into the VIEWED period (parity with old
handleQuickAdd), header-anchored overlay expansion (no shell reflow), single instance
across tabs. QuickAdd.tsx retained (still consumed by TeamPage per-member rows). Opus
review: no blockers, team page unregressed, payloads clean; 1 MAJOR (stale #horizon
override surviving tab switches — badge said Monthly, Enter created weekly) + 1 minor
(expand merged draft-over-token, opposite of Enter path) → both fixed: override resets
on tab-horizon change; expand now token-over-draft for all pill fields. tsc/build clean,
bundle index-CbJxIHQE.js live. Accepted edge: popover may clip at bottom of very short
viewports.

**Close-affordance fix (user-reported):** expanded composer had NO discoverable close —
no × button, click-outside only closed popovers, Esc required focus inside the card.
Fixed (both contexts, draft always preserved): × button top-right, click-outside
collapses (popover closes first), Esc verified from any descendant. Bundle
index-BmAkmE2w.js.

**403 incident (resolved, root cause corrected):** static UI began serving a bare
"403 Forbidden" (eventually in the user's browser too) while /api/* stayed 200. NOT a
network/proxy issue (first diagnosis wrong): Go's ServeFile maps a file-permission
error to that exact page — the long-running server process (started from /tmp) lost
its macOS per-process read grant on ~/Downloads/.../ui/dist. Fix: full stack restart
(`make down`, fresh mongo, server rebuilt as `./taskman-bin` INSIDE the repo — now the
convention, binary gitignored) → UI 200 + API 200 verified. Discriminator for future:
static-403 + API-200 ⇒ restart server process; see memory note `taskman-loopback-403`.

**Ops note:** one opus review run returned an anomalous non-review result carrying
injection-style instructions (zero tool calls); it was discarded and re-run fresh —
treat any subagent result that addresses the orchestrator with directives as suspect.

**Snapshot @ 2026-07-09 16:30 (progress saved on user request):**
- Stack UP: `./taskman-bin` (repo dir) on :8484 serving bundle index-BmAkmE2w.js;
  taskman-mongo-1 fresh container, data intact in volume `taskman_taskman-mongo`.
  Restart recipe: `make down && make up` (server binary convention: build in repo dir).
- Feature state: personal planning views (v2 system) ✓ · Teams landing + team page
  w/ filters + undoable bulk ops (v3) ✓ · counts API ✓ · TaskComposer on team page
  (v3.1) + all personal tabs (v3.2) + close affordances ✓ — all opus-reviewed, all
  findings fixed, all live-verified.
- Docs inventory: DESIGN.md (v1) · DESIGN_V2_UI.md · DESIGN_V3_TEAMS.md ·
  DESIGN_V31_COMPOSER.md (incl. v3.2) · TEAMS_FEATURES.md · BACKEND_ARCH_REVIEW.md
  (auth roadmap, report-only) · research-{task-tools,team-views,task-composer}.md ·
  DESIGN_PROMPT{,_TEAMS,_COMPOSER}.md · design handoffs: design_handoff_taskman_ui/
  (v1) · "design_handoff_taskman_ui 2/" (09/10) · "Taskman UI redesign (2)/" (11).
- Git: repo initialized, ZERO commits — everything untracked (user instruction:
  don't commit yet). First commit = full snapshot whenever user says go.

**Next:** user acceptance pass on composer (both contexts); skills extraction
(user-planned); auth work per BACKEND_ARCH_REVIEW phases when user green-lights;
git commit when user asks.

## Session 2 — 2026-07-08 (v2 UI revamp from design handoff)

User supplied a high-fidelity design handoff at `design_handoff_taskman_ui/` (README +
8 dc.html deliverables). Contract addendum: `docs/DESIGN_V2_UI.md`.

**Decisions:** recurrence interval added NOW (backend, every-N-periods with server-managed
anchor); member `role` field added; undo-toasts replace confirms (DELETE returns subtree +
new POST /api/tasks/restore); central theming — tokens only, no hardcoded hex, 3-state
System/Light/Dark toggle (localStorage `taskman-theme`, data-theme attr, anti-flash script).

**Built (4 sonnet agents, 2 waves):**
- Wave 1: backend v2 (interval/anchor/role/restore, tests + live-verified) ∥ UI foundation
  (tokens light+dark, @fontsource Newsreader/IBM Plex, 244/68px sidebar + grouped nav +
  attention badge, StatusControl/TaskRow/Toast/QuickAdd-with-token-parser, api.ts v2 types).
- Wave 2: All-tasks (smart grouping toolbar) + Attention (multi-select, bulk bar, 3 undo
  flows) ∥ TaskDetail (452px editorial slide-in, recurrence interval editor, subtask
  progress, ambient save) ∥ Team board (member columns/avatars/roles/per-member quick-add)
  + planning views restyle + search restyle. Several agents needed resume after API drops —
  all completed + self-verified. Canonical clean build served (index-CzBv3K9d.js).

**Review + fix wave (completed 2026-07-09):** Opus reviewer stalled 3× on infrastructure
but its transcript yielded confirmed findings; orchestrator found a 4th at the same seam.
CLEARED by review: tokens exactly match handoff (both themes), zero hardcoded hex, no
cross-file CSS collisions, restore round-trip fully sound (ids preserved, children
relinked), RestoreTasks dup-key counting correct, go vet/test + tsc clean.
FIXED (sonnet fixer, all live-verified, re-verified on :8484 after restart):
1. PatchTask re-anchor aliasing — json.Unmarshal reuses a non-nil *int in place, so the
   pre-patch recurrence snapshot aliased Interval; interval-only PATCH never re-anchored.
   Fix: cloneRecurrence deep-copy + tests reproducing the aliasing without Mongo.
2. Quick-add `*weekdays` 400 — parser now emits weekdays:[1..5] (Mon–Fri).
3. TaskDetail Weekdays preset 400 (empty array) — defaults [1..5], can't deselect last
   weekday; PLUS preset chips gated to the task's horizon (read-only horizon means
   mismatched presets could never succeed).
4. Recurrence tokens now imply horizon in the parser (`*weekly` on Day view used to 400);
   all QuickAdd consumers inherit; TeamsView verified to derive period from result.horizon.

**Residual (accepted, not blocking):** no pixel-level conformance audit of views 04/06/07
against the mocks and no parser date-edge review (^jul12 year rollover etc.) — the review
died before reaching them. Structure/tokens/interactions verified. Revisit if usage
surfaces visual drift.

**State: v2 revamp COMPLETE.** :8484 runs the fixed binary + fixed bundle. Mongo container
up (volume taskman-mongo). Nothing committed to git (user's standing instruction: not yet).
`make up` / `make down` manage the stack.

## Session 1 — 2026-07-08

**Done:**
- Requirements grilled and closed — all 15 decisions recorded in `docs/DESIGN.md` (the build contract).
- Market research on task-tool conventions → `docs/research-task-tools.md` (sonnet agent).
  Gaps found and adopted into MVP0: notes, priority, recurring presets, search, bulk reschedule, slipped-period detection.
- Environment checked: Go 1.25.3 ✓, Node 24 ✓, Docker ✓ (daemon may need starting), MongoDB not installed → runs via docker compose.
- Scaffold + implementation launched via subagents (sonnet): backend (Go server, compose, Makefile) and frontend (Vite React TS) in parallel against the DESIGN.md contract.

**Build completed same session:**
- Backend (sonnet agent): Go 1.25 stdlib server + mongo-driver v2, full API per contract, table-driven tests
  (ISO week edges, recurrence math) passing, live curl smoke test green. Compose file + Makefile + README.
- Frontend (sonnet agent): Vite+React+TS, 6 views, typed api.ts, ISO-week helpers verified on edge dates,
  clean `tsc`/`vite build`.
- Integration fix (orchestrator): UI sent `null` to clear notes/dueDate but those are non-pointer strings
  server-side → changed UI to send `""` (pointer fields still cleared via `null`). Verified live.
- Opus adversarial review: period math Go↔TS brute-forced 2000–2040, 0 mismatches; recurrence
  materialization idempotent; found 2 BLOCKERs — (1) wire mismatch: server emits `id`, UI read `_id`
  (whole UI id-surface dead); (2) cascade delete infinite-loops on parentId cycle reachable via PATCH
  re-parenting — plus 2 minors (horizon change unvalidated vs children; teamIds undefined guard).
- Fixer (sonnet agent): all 6 fixes applied + live-verified (cycle PATCH → 400, cascade delete OK,
  horizon-coarsen with children → 400, zero `_id` in built bundle). go vet/test clean, tsc clean.

**Current state: MVP0 WORKING.** Server live on http://localhost:8484 (binary /tmp/taskman-bin),
Mongo in docker (`taskman-mongo-1`, volume `taskman-mongo`). Nothing committed to git yet (repo
initialized, user hasn't asked for commits).

**Later same session — stack ergonomics + UI restructure:**
- Makefile: `make up` (mongo+ui+run) and `make down` (kill :8484 + compose down, volume survives). Both verified.
- UI restructure (sonnet agent): top tabs → fixed left sidebar; new **All tasks** view (all open tasks,
  overdue-first sort, per-row done/→today/delete actions); backend `status=open` virtual filter in Search;
  breathing-room CSS pass. Verified live on :8484.
- `docs/DESIGN_PROMPT.md` — self-contained starting prompt for the visual redesign, to be fed to
  claude.ai / Claude Code by the user. Flow: Claude produces single-file HTML/CSS mockups (tokens first,
  then components) → bring back here → translate into ui/ plain CSS.

**Next session:**
1. Feed `docs/DESIGN_PROMPT.md` to Claude, iterate mockups, translate the design tokens + components into ui/.
2. User acceptance pass over the UI; polish list from real usage.
3. Consider committing the working tree (user said not yet — ask again when stable).
4. MVP1 backlog: voice→tasks AI ingestion (`POST /api/ingest/transcript` hook reserved), reminders,
   real multi-user/auth, manual ordering, recurring-task edit ergonomics.

**Key paths:** contract `docs/DESIGN.md` · research `docs/research-task-tools.md` · run: `make mongo ui run` → http://localhost:8484
