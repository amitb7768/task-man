# Auth & User-Management — Gap-Fill Research

Scope: fills what `BACKEND_ARCH_REVIEW.md` §4 deliberately left open (it locked: Mongo opaque
cookie sessions, bcrypt, `users`/`sessions` collections, "link don't merge" with `members`,
admin/user two-role model). This doc covers UX + hygiene + hardening conventions for a
5-15-person LAN-only tool, no email server, no TLS. Identifier = **email**, not username
(supersedes the arch review's placeholder `username` field).

## 1. Admin-provisioned credential UX

| Pattern | Needs email server? | Convention |
|---|---|---|
| Admin sets temp password, shown once, force-change flag | No | **Dominant** — Auth0, Azure AD, most internal tools |
| Magic link | Yes | Skip — no mail infra here |
| Admin sets permanent password, no forced change | No | Anti-pattern — credential never rotates away from the admin who typed it |

Convention (confirmed via NIST 800-63B guidance + Azure AD/Auth0 docs): generate a temp
password, flag the account, force a change on first successful login before granting any
other access. This also matters because two people (admin + user) briefly *know* the same
secret — forcing a change closes that window fast.

**Fit:** add `mustChangePassword bool` to `users`. Login still succeeds and issues a session
(don't add a second unauthenticated flow) but `sessionAuth` middleware 403s every route except
`/api/auth/me` and a new `PATCH /api/auth/password` while the flag is set; UI redirects to a
"set a new password" screen instead of the app shell. Same flag flips on **any** admin-issued
reset, not just first login — the "admin now knows your password" case is identical.

## 2. Cookie session hygiene (plain http://, LAN)

- **HttpOnly:** always. **Secure:** cannot set — no TLS on LAN http. This is an accepted,
  documented gap (see §5), not an oversight; revisit only if the tool ever gets a reverse
  proxy with TLS.
- **SameSite=Lax** (already decided) is correct over `Strict` — Strict can drop the cookie on
  first cross-context navigation (e.g. opening a task link from chat) with no functional
  upside for a single-origin app; Lax is the OWASP-recommended default.
- **TTL — idle vs absolute:** OWASP's Session Management Cheat Sheet: idle timeout 15-30 min
  for low-risk apps, absolute 4-8h for an office-worker app. Bare minimum for taskman: **one
  sliding TTL** — bump `sessions.expiresAt` on each authenticated request, Mongo TTL index does
  the eviction for free (already the arch review's schema, zero new fields). Set idle window to
  **~12h** (covers a workday without re-login nagging at 5-15-user low-risk scale). Skip a
  separate absolute cap for MVP — cheap to add later (`createdAt` already exists) if ever
  needed, not worth the second field now.
- **Logout:** delete the session doc. Simple, correct, already the plan.
- **Concurrent sessions:** allow multiple per user (phone + laptop + two browsers) — default in
  nearly every non-banking tool. Single-session-enforcement (kill old session on new login)
  adds real complexity (must notify/redirect the displaced session) for no benefit at this
  scale — skip.
- **Invalidate on password change/reset:** `sessions.deleteMany({userId})` on both self-service
  change and admin reset. This is universal practice and cheap — do it unconditionally.

## 3. Member-record vs user-account unification

Three patterns tools use when "assignable person" predates "login account":

| Pattern | Example | Failure mode |
|---|---|---|
| **Merge** into one collection, credentials optional | small CRMs | Schema conflates identity+auth concerns; adding auth fields later touches every place that reads a "person" |
| **Link** via FK, two collections | Jira (assignable user vs licensed user), taskman's `members`/`users` | Orphaned duplicate identity if the link isn't made explicitly at account-creation time |
| **Keep fully separate**, no link | ad hoc tools | Assignee dropdown and login roster silently diverge — "who is this" confusion |

Jira's model is the closest analogue: an "assignable user" record can exist and receive
assignments without ever having logged in; a real account is a separate identity that a person
uses when they *do* log in. Taskman's existing `members` (assignee records, pre-auth) map
cleanly onto this.

**Fit:** the arch review's `memberId` FK on `users` is the right call — don't change it. The
one addition this research surfaces: **never auto-link by fuzzy-matching name/email** when an
admin creates a user account. Make "link to existing member" vs "create new member" an
explicit choice in the admin's create-user UI. Auto-matching is exactly how the duplicate-
identity failure mode above happens (typo'd email, "Amit" vs "Amit B.").

## 4. Role gating: Go stdlib middleware + React SPA

- **Middleware chain** (already sketched in the arch review): plain
  `func(http.Handler) http.Handler` composition — `sessionAuth` resolves cookie → session →
  user → `context.Context`; `requireAdmin`/`requireUser` wrap individual routes. No framework
  needed; this is the standard net/http pattern.
- **401 vs 403, by convention:** 401 = no/invalid/expired session → SPA redirects to `/login`.
  403 = valid session, wrong role → SPA shows an in-app "not allowed," does **not** redirect
  (redirecting on 403 loses the user's place for no reason). Keep these distinct in the fetch
  wrapper.
- **Identity discovery — boot-time `/me` fetch is the dominant convention**, not embedding
  user/role into `index.html`. Embedding requires per-request server templating, which breaks
  a plain static-file SPA build (`ui/dist`) and its caching story — avoid. `/api/auth/me`
  (already in the endpoint matrix) doubles as the SPA's "am I still logged in" check on load.
- **Route hiding vs component hiding:** do both, cheaply, from one `AuthContext` populated by
  `/me` — hide nav entries/routes AND disable/hide inline actions (e.g. delete-team button) for
  non-admins. Client-side hiding is UX only; the server-side role check is the actual boundary
  — never skip the latter because the former exists.
- **401 mid-session:** centralize in `ui/src/api.ts`'s fetch wrapper — any 401 clears local
  auth state and redirects to `/login` (optionally with a "session expired" toast). One place,
  not per-call handling.

## 5. Login endpoint hardening at LAN scale

- **bcrypt** (`bcrypt.CompareHashAndPassword`) is already constant-time by construction —
  nothing to add on top; don't layer a manual `subtle.ConstantTimeCompare`, that's solving an
  already-solved problem.
- **Rate limiting:** naive per-IP counter (`map[string]*rate.Limiter` from
  `golang.org/x/time/rate`, or a hand-rolled mutex+sliding window) is sufficient — no Redis,
  no distributed store, at 5-15 users on one LAN. In-memory counters resetting on process
  restart is an acceptable trade at this scale. Recommend e.g. 5 failed attempts / 15 min per
  IP. (Sleep-on-failure is a valid alternative but adds latency to every legitimate login too
  — pick the counter, not both.)
- **CSRF — the researched answer:** OWASP is explicit that `SameSite=Lax` **alone is not a
  complete CSRF defense** for JSON APIs (a crafted `enctype="text/plain"` form can assemble a
  JSON-shaped body without triggering CORS preflight). *However* that bypass only matters if
  the forged request can still carry the session cookie — and `SameSite=Lax` already blocks
  cookies on **cross-site POST** (Lax only forwards cookies on top-level-navigation **GET**).
  Taskman's API has **no state-changing GET endpoints** (arch review's matrix is all
  POST/PATCH/DELETE), so the classic Lax gap doesn't apply here. Add one more cheap layer:
  reject any mutating request whose `Content-Type` isn't `application/json` — this is a 3-line
  middleware check, not a token system. Net: **do not build CSRF tokens for MVP**; do add the
  `Content-Type` guard; explicitly accept the residual risk of pre-2019 browsers ignoring
  `SameSite` (not a realistic fleet concern on a managed corporate LAN).

## 6. Enhancements checklist — ranked value/cost for a 10-person tool

| Feature | Value | Cost | Verdict |
|---|---|---|---|
| Account disable (not delete) | High — offboard a teammate without breaking `assigneeId` FKs/history | Low — one bool + a check in `sessionAuth` | **Do at auth MVP** |
| "Who am I" nav indicator | High (trust signal) | ~Zero — falls out of the `/me` boot fetch already needed | **Do, bundled** |
| lastLogin display | Medium-high (admin visibility) | Low — one field write on login | **Do soon after MVP** |
| Password minimum length (~8 chars) | Medium — admin issues initial passwords, worth a floor | Trivial | **Do at MVP** (NIST 800-63B: favor length over complexity rules) |
| Audit trail of admin actions | Medium (small team, low dispute risk today) | Medium — new collection + write at every admin action | **Defer** until >1 admin or an actual incident |
| Remember-me | Low — LAN tool typically left open in a tab all day | Adds session-lifetime branching | **Skip** |

## Recommended minimal auth shape

Consistent with `BACKEND_ARCH_REVIEW.md` §4, filled in:

```
users:    _id, email (unique, identifier), passwordHash (bcrypt),
          role "admin"|"user", memberId ObjectId? (explicit link, not auto-matched),
          mustChangePassword bool, lastLoginAt time?, disabled bool, createdAt
sessions: _id (random 32B token, HttpOnly+SameSite=Lax cookie), userId,
          expiresAt (TTL index, sliding — refreshed on each authenticated request, ~12h idle)
```

- Middleware: `requestLogger → contentTypeGuard(mutating routes) → sessionAuth → requireAdmin
  /requireUser`. `sessionAuth` also 403s (except `/auth/me`, `/auth/password`) while
  `mustChangePassword` is set, and rejects `disabled` users outright (401).
- `POST /api/auth/login` (rate-limited 5/15min per IP) · `POST /api/auth/logout` ·
  `GET /api/auth/me` · `PATCH /api/auth/password` (self) · admin-only user CRUD incl. reset
  (sets `mustChangePassword`) and disable/enable.
- Any password change or admin reset: `sessions.deleteMany({userId})`.
- No CSRF tokens, no JWT, no RBAC framework, no rate-limit service, no email flow — matches the
  arch review's explicit non-goals list.

## Sources

- [OWASP — SameSite](https://owasp.org/www-community/SameSite) ·
  [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) ·
  [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [Auth0 — force password change on first login](https://support.auth0.com/center/s/article/force-new-user-to-change-password-on-first-login) ·
  NIST SP 800-63B (temp-credential + length-over-complexity guidance)
- Atlassian docs — Jira assignable vs licensed user model (member/account separation precedent)
