# taskman — instructions for Claude Code

Standalone task-manager app (Go + React + Mongo) for a small team on a LAN.
It lives under `~/Downloads/pbh` for convenience only — the pbh workspace rules
(clusters, graphify, delegation, branch policy) do NOT apply here. Treat this
directory as its own project.

## Layout — where the context lives

| Path | What | Deeper context |
|---|---|---|
| `server/` | Go backend, flat `package main`, stdlib net/http, serves SPA + JSON API on :8484 | `server/CLAUDE.md` |
| `ui/` | React 19 + TypeScript + Vite SPA | `ui/CLAUDE.md` |
| `src-tauri/` | macOS thin-client wrapper (Tauri v2) — loads the SPA from the server URL, runs no server itself | `docs/MACAPP_PLAN.md` |
| `docs/` | per-feature design contracts (`DESIGN*.md`, `*_FEATURES.md`), research notes, `DEPLOYMENT.md` | `docs/AUTH_FEATURES.md` has the endpoint×role matrix |
| `PROGRESS.md` | session-by-session build log — read the tail for latest state | |

## Commands

- `make up` / `make down` — Mongo (Docker) + build UI + server on :8484
- `make mongo` / `make ui` / `make run` — the individual pieces
- `make test` (`go test ./...` — needs Mongo up) / `make vet`
- `cd ui && npm run build` — typecheck + build SPA. The Go server serves
  `ui/dist` fresh from disk per request: rebuild + browser refresh, no server
  restart.
- `make mac-dev` / `make mac-build` — Tauri app

Config is env-vars only (see `.env.example`; the server does NOT auto-load
`.env`). Never commit secrets.

## Domain model in one breath

Task: `horizon` (`daily|weekly|monthly`) + `period` (`YYYY-MM-DD` | `YYYY-Www` |
`YYYY-MM`); `status` `todo|in_progress|done|cancelled` — **terminal = done +
cancelled**, folded together as "completed" everywhere in the UI; `dueDate`
optional `YYYY-MM-DD` string (independent of period); `priority`
`""|low|medium|high`; subtasks via `parentId` (child horizon ≤ parent);
recurrence + `seriesId` (instances materialized lazily on every read path);
`activity` — an embedded, system-managed timeline of dated `note` entries
(author-owned) and auto-logged `status` transitions, only present on
detail/PATCH responses, never on list reads; `GET /api/summary` classifies
tasks over a date range into completed/updated/added for weekly reporting
(`docs/DESIGN_V9_NOTES_SUMMARY.md`).

**Personal task = `ownerId` set + `teamId` nil** (private, even from ADMIN).
**Team task = `teamId` set + `ownerId` nil.** `assigneeId` requires `teamId`.

Auth: opaque cookie sessions (`taskman_session`), roles `ADMIN|USER`, every
rule enforced server-side (UI only hides). Matrix: `docs/AUTH_FEATURES.md`.

Time: ISO-8601 weeks (Monday start), machine-local timezone everywhere.

## Cross-cutting conventions

- Bulk actions are client-side `Promise.all` of per-task PATCH; undo is a
  client-side snapshot + toast (6 s). The only server-side undo is
  delete → restore (raw doc replay).
- No pagination exists anywhere yet (client or server).
- Ship the minimal diff; each feature gets a short contract doc in `docs/`
  before build.
