# taskman v4 — Auth & user management (feature set + build contract)

Closed 2026-07-10 via grilling + `docs/research-auth.md` + `docs/BACKEND_ARCH_REVIEW.md`
(auth sketch). Internal LAN tool, bare-minimum auth to identify users and gate pages.

## Decisions (closed)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Identity | UNIFY: member records become users. Same collection; credential fields optional — a member without `passwordHash` is assignable-only (today's behavior), "enable login" upgrades them. |
| 2 | Roles | `systemRole: "ADMIN" \| "USER"` (string, extensible to e.g. MANAGER later without migration). DISTINCT from existing `role` job-title field — never conflate. |
| 3 | Ownership | Tasks gain `ownerId`. Personal task = ownerId set + teamId null. Personal views scoped to session user. NO backfill migration — user opted to wipe existing tasks instead (done 2026-07-10: tasks collection emptied; teams + members kept). |
| 4 | Privacy | Personal tasks visible ONLY to their owner — including from ADMIN. Admin oversight happens on team tasks. |
| 5 | USER team powers | Full edit (status/due/notes/priority/reassign/create) on tasks in teams they belong to. Task DELETE is ADMIN-only (everywhere). No team/member/user management. |
| 6 | First login | `mustChangePassword` set on provision AND on every admin reset; UI locks to change-password screen until cleared. Temp password shown once to admin. |
| 7 | Bootstrap | Env-seed on startup: if no login-enabled user exists, create ADMIN from `ADMIN_EMAIL` (+ `ADMIN_PASSWORD` or generate + print once to log). |
| 8 | Lifecycle | `disabled` flag: blocks login, kills sessions, keeps record + task history (dimmed in UI). No hard delete of login-enabled users in v4. |
| 9 | Sessions | Mongo-backed opaque cookie (`taskman_session`), HttpOnly, SameSite=Lax, no Secure (LAN http). Sliding 12h idle TTL (Mongo TTL index, touched per request). Logout endpoint. ALL sessions invalidated on password change/reset/disable. Concurrent sessions allowed. |
| 10 | CSRF/hardening | No CSRF tokens: SameSite=Lax + `Content-Type: application/json` guard on state-changing routes (per research — sufficient for zero-GET-mutation JSON API). bcrypt (dep already present). Naive per-IP login backoff (counter + sleep-on-failure). `lastLoginAt` recorded. |
| 11 | Email | Login identifier; unique partial index (only where credentials exist). Still optional for assignable-only members. |

## Data model changes

**members** (collection keeps its name; conceptually "people"):
```
+ email        (existing, optional) — unique partial index where passwordHash exists
+ passwordHash string, optional     — presence == login-enabled
+ systemRole   "ADMIN"|"USER"       — required when passwordHash set
+ mustChangePassword bool
+ disabled     bool
+ lastLoginAt  time, optional
```
**sessions** (new): `{_id, token (random 256-bit, indexed unique), userId, expiresAt}` +
TTL index on expiresAt. **tasks**: `+ ownerId ObjectID, optional` (required for
personal tasks going forward; backfilled by migration).

## API surface

New (`/api/auth/*`):
```
POST /api/auth/login            {email, password} → {user} + Set-Cookie   (public)
POST /api/auth/logout           → 204, clears cookie                       (session)
GET  /api/auth/me               → {user: id,name,email,systemRole,mustChangePassword,teams} (session)
POST /api/auth/change-password  {current, new} → 204; clears mustChangePassword; kills other sessions (session; the ONLY mutating route allowed while mustChangePassword)
```
Admin user management (extends member endpoints):
```
POST /api/members/{id}/enable-login  {systemRole} → {tempPassword}   (ADMIN)
POST /api/members/{id}/reset-password → {tempPassword}               (ADMIN)
PATCH /api/members/{id}  gains {systemRole?, disabled?}              (ADMIN)
```

### Endpoint × role matrix (server-enforced; UI hides accordingly)
| Surface | ADMIN | USER |
|---|---|---|
| auth login/logout/me/change-password | ✓ | ✓ |
| Teams create/rename/delete | ✓ | — (403) |
| Members CRUD + enable-login/reset/disable | ✓ | — |
| GET /api/teams (+counts) | all teams | own teams only |
| GET /api/teams/{id}/board | any team | own teams only |
| Task CREATE | any | personal (owner=self) or team task in OWN team |
| Task PATCH | any | own personal; any task in own teams |
| Task DELETE + restore | ✓ | — (403) |
| Views day/week/month | own personal | own personal |
| All-tasks / Attention / Search / reschedule | own personal + ALL team tasks | own personal + OWN teams' tasks |
| GET /api/teams/{id}/rollover (v6) | any team | — (403) |
| PATCH /api/tasks/{id} with weekOf (v6) | ✓ | — (403) |
| GET /api/teams/{id}/history (v6) | any team | own teams only |
| GET /api/backlog (v7) | own backlog | 403 |
| POST /api/tasks with horizon:"backlog" (v7) | ✓ (personal) | ✓ but nav tab hidden; harmless personal parking |
| PATCH assigning backlog→team (v7) | ✓ (flip is ADMIN-only anyway) | 403 (existing flip rule) |
| POST/PUT/DELETE /api/tasks/{id}/notes[/{noteId}] (v9) | any task within `canAccessTask` (so NOT another user's personal task); edit/delete any entry | any task within `canAccessTask`; edit/delete own entry only (403 otherwise) |
| GET /api/summary (v9) | `teamId` scope: any team | `teamId` scope: own teams only (403 otherwise); me-scope (no `teamId`) = own personal + assigned-to-me |

Note (v7): creation with horizon `"backlog"` is not role-blocked server-side
— a USER doing it via raw API just gets an invisible personal task: it never
surfaces in their own UI (nav tab hidden) or an ADMIN's (personal-task
privacy — GET /api/backlog is owner-scoped, so it isn't even that user's own
admin session's problem to clean up), and the flip rule already stops them
staffing it anywhere. Removing it requires an ADMIN who has the raw task id
(Task DELETE is `requireAdmin`, unqualified by ownership) — the creating USER
cannot delete it themselves. Accepted, not worth a special-case 403.

Unauthenticated request to anything but login/static → 401 → SPA shows login.
Role failure → 403. `mustChangePassword` session → 403 on everything mutating except
change-password (reads allowed so the shell can render).

## Server architecture (per BACKEND_ARCH_REVIEW Phase 1+2)

- `middleware.go`: wrap point on the mux — requestLog → jsonGuard (state-changing) →
  sessionLoad (cookie → user in ctx) → per-route requireAuth / requireAdmin.
- ctx carries `{userId, systemRole}`; store methods gain scoping params where the
  matrix requires (personal queries filter ownerId; team queries filter team membership
  for USER).
- Seed on startup (idempotent): ensure new indexes (unique partial email-with-credentials,
  sessions TTL, tasks ownerId); admin env-seed. No task backfill (collection was wiped;
  every task created post-v4 carries ownerId).

## UI changes (existing design system, no external mockup round — login/change-password
screens follow tokens + card anatomy; user-management extends the members panel)

- Boot: fetch `/api/auth/me`; 401 → login screen (card, email+password, error state).
- Forced-change screen when mustChangePassword (locks the shell).
- Sidebar footer: signed-in identity (avatar+name) + logout; theme toggle stays.
- Role gating (USER): hide New-team, members management, all delete buttons,
  login-provisioning UI; Teams landing lists own teams; team page without member panel
  edit affordances. Attention/All-tasks/Search show own-scope automatically (server
  enforces anyway).
- Admin: members panel gains Enable login (role picker → shows temp password ONCE with
  copy button), Reset password (same reveal), Disable toggle (dimmed row), systemRole
  badge. Disabled users dimmed on boards.
- 401 mid-session (expired) → redirect to login preserving nothing (bare minimum).

## Build plan (usual pipeline)

- **Wave A (sonnet)**: backend — middleware seam, sessions, auth endpoints, seed +
  migration, scoping per matrix, tests (middleware + scoping table tests; httptest for
  auth flow), side-port live verification.
- **Wave B (sonnet, after A's contract locks — can start on UI shell in parallel)**:
  login/forced-change/me-boot, role-gated nav + views, admin user management UI.
- **Wave C (opus)**: adversarial review — scoping bypasses (IDOR: USER patching another's
  personal task by id, board access outside own teams), session fixation/invalidation,
  matrix conformance, migration idempotency. Then sonnet fixer + live e2e verify.

## Explicit non-goals (v4)

HTTPS/TLS, email flows, OAuth/SSO, password complexity policies beyond min-length 8,
account self-registration, audit log (candidate v4.1), per-team roles, API tokens.
