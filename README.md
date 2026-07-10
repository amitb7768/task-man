# taskman

A lightweight task manager for a small team, meant to run on a single machine or
an internal LAN. It combines **personal planning** (daily / weekly / monthly) with
**team tracking** (a manager provisions accounts and follows each member's work).

- **Frontend** — React + TypeScript (Vite), one SPA.
- **Backend** — Go (stdlib `net/http`), serves the SPA **and** a JSON API from a
  single binary on port **8484**.
- **Database** — MongoDB (run locally via Docker).
- **Desktop** — an optional macOS app (`src-tauri/`) that wraps the web UI in a
  native window — see [Sharing the macOS app](#macos-app).

---

## What it does

**Personal planning**
- Day / Week / Month views. Every task is anchored to a concrete period (a date, an
  ISO week, or a month), so the views are real plans, not just filters.
- Tasks have a title, notes, optional due date, priority, and a status
  (`to do → in progress → done / cancelled`).
- **Subtasks** — a child's horizon must be ≤ its parent's; the parent shows a
  done/total progress count.
- **Recurring tasks** — daily / specific weekdays / weekly / monthly, every N periods.
- **Attention** view surfaces overdue tasks *and* undated tasks whose period has
  passed, with one-click bulk reschedule (undoable).
- **Completed work folds away** — done tasks collapse into a "N completed" disclosure
  at the bottom of each view, keeping your active list clean.
- **Quick add with tokens** — type `Ship release !high ^fri *weekly` and the composer
  parses priority, due date, and recurrence into editable controls; expand it for the
  full form (assignee, description, repeat).

**Team tracking**
- Create teams and add members (name, email, role).
- A per-team board: filter to one person, group by member, sort by due date, with
  bulk reassign / set-due / mark-done / delete — all undoable.
- The manager sees each member's team tasks; members' **personal** tasks stay private.

**Accounts & roles** (see [Roles](#roles-admin-vs-user))
- Login by email + password. Two roles: **ADMIN** and **USER**.
- The admin provisions accounts and hands out one-time passwords; users change theirs
  on first login. No self-registration, no email server needed.

---

## Quick start (single machine)

Prerequisites: **Go 1.25+**, **Node 20+**, **Docker** (for MongoDB).

```sh
make up          # start Mongo (Docker) + build the UI + run the server on :8484
```

`make up` is `make mongo` + `make ui` + `make run`. Then open **http://localhost:8484**.

First run bootstraps an admin account (see [First run](#first-run--provisioning-your-team)).
`make down` stops the server and the Mongo container (your data survives in the Docker
volume).

> Day boundaries use the machine's local timezone — run the server on the host, not in
> a UTC container.

To deploy on a LAN server so teammates can connect, see
**[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)**.

---

## First run & provisioning your team

On startup, if no login-enabled account exists yet, the server creates the first admin
from environment variables:

```sh
ADMIN_EMAIL=you@example.com make run        # or set it in your deploy env
```

- `ADMIN_EMAIL` — required to trigger seeding; becomes the admin's login email.
- `ADMIN_PASSWORD` — optional. If omitted, a random password is generated and printed
  to the server log **once**. Either way the admin must change it on first login.

Then, as admin:
1. Log in and change your password.
2. Open a team → **Members** → add each teammate (name, email, role).
3. **Enable login** on a member → pick ADMIN or USER → copy the one-time password shown
   (it's displayed only once) and hand it to them.
4. They log in, are forced to set a new password, and see only what their role allows.

When someone leaves, **disable** their account (login blocked, sessions ended, history
kept) rather than deleting it.

---

## Roles (ADMIN vs USER)

| | ADMIN | USER |
|---|---|---|
| Personal Day/Week/Month planning | ✓ (own, private) | ✓ (own, private) |
| Create teams, add/edit/remove members, provision logins | ✓ | — |
| See teams | all | only teams they belong to |
| Create / edit tasks in a team | any team | only their own teams |
| Delete tasks | ✓ | — |
| See another user's **personal** tasks | no (private to the owner) | no |

Every rule is enforced on the server; the UI just hides what a USER can't do.

---

## macOS app

`src-tauri/` is a thin [Tauri v2](https://tauri.app) wrapper: a native window that
loads the SPA straight from the server (so login and everything else work exactly as in
the browser), plus a small built-in screen to set the server address and to show a
retry prompt if the server is unreachable. It does **not** run the server itself.

```sh
# one-time: install Rust (https://rustup.rs) and the Tauri CLI
cargo install tauri-cli --version '^2'

make up          # the server must be running — the app is a thin client over it
make mac-dev      # run the app in dev
make mac-build    # build src-tauri/target/release/bundle/macos/taskman.app (+ a .dmg)
```

The build is **unsigned** (internal tool). Building it, distributing it to teammates,
and pointing it at a LAN server are covered in
**[docs/DEPLOYMENT.md → Sharing the macOS app](docs/DEPLOYMENT.md#sharing-the-macos-app)**.

---

## Configuration

The server reads everything from environment variables — nothing sensitive is committed.
Copy the template and fill it in: `cp .env.example .env` (the server doesn't auto-load
it — `.env.example` explains how to apply the values).

| Var | Default | Purpose |
|---|---|---|
| `PORT` | `8484` | HTTP port (binds all interfaces, so it's reachable on the LAN). |
| `MONGO_URI` | `mongodb://localhost:27017` | MongoDB connection string. |
| `MONGO_DB` | `taskman` | Database name (use a scratch name for local testing). |
| `ADMIN_EMAIL` | — | Set once to seed the first admin (idempotent afterward). |
| `ADMIN_PASSWORD` | — | Optional seed password; generated + logged once if unset. |

Sessions are opaque cookies (`taskman_session`, HttpOnly, SameSite=Lax, sliding 12h),
backed by a Mongo collection. There is **no TLS** — this is an internal LAN tool. Do not
expose it to the public internet without a reverse proxy that adds HTTPS.

---

## Development

```sh
make test        # go test ./...
make vet         # go vet ./...
cd ui && npm run build   # type-check + build the SPA
```

The Go server serves `ui/dist` fresh from disk per request, so during UI work you can
rebuild the SPA and just refresh the browser without restarting the server.

Design decisions, feature contracts, and research live in **`docs/`** — start with
`docs/DESIGN.md`.

---

## Project layout

```
server/     Go backend (single package): HTTP, store, auth, sessions, period/recurrence math
ui/         React + TS SPA; build output (ui/dist) is served by the Go binary
src-tauri/  macOS desktop wrapper (Tauri v2) — thin client, optional
docs/       Design contracts, research notes, and DEPLOYMENT.md
docker-compose.yml   MongoDB (mongo:7, named volume)
Makefile    up / down / mongo / ui / run / test / vet / mac-dev / mac-build
```
