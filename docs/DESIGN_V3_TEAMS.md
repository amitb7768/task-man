# taskman v3 — Teams build contract (addendum to DESIGN.md + DESIGN_V2_UI.md)

Visual spec source of truth: `design_handoff_taskman_ui 2/` — README sections 09/10 +
`09 Teams Landing.dc.html` + `10 Team Page.dc.html`. Screen 07 (member-columns board)
is SUPERSEDED — delete its implementation. Feature rationale: `docs/TEAMS_FEATURES.md`;
research: `docs/research-team-views.md`. All v2 rules (tokens-only colors, CSS
ownership, undo wiring, "" vs null clearing) still apply.

## Backend delta (the only one)

`GET /api/teams` — each team in the response gains:
```
memberCount   int   // members whose teamIds include this team
openCount     int   // tasks with this teamId, status in (todo, in_progress)
overdueCount  int   // of those, dueDate != "" and dueDate < today
```
Server-side aggregation; run recurrence materialization first (counts must see spawned
instances, same as other reads). No other endpoint changes — the team page reuses
GET /api/teams/{id}/board; bulk ops loop PATCH/DELETE client-side like Attention.

## Frontend architecture

- **Navigation state (App.tsx):** the `Teams` nav item shows **TeamsLanding**; selecting
  a team shows **TeamPage** (selected teamId in App state, persisted to localStorage
  `taskman-team` so returning to Teams reopens it; landing accessible via the team
  page's back affordance). Header eyebrow/title per mock: landing = "teams · N members"
  eyebrow + serif "Teams"; team page renders its own in-content header per "10".
- **Files:** `views/TeamsLanding.tsx` (new) + `views/TeamPage.tsx` (new, includes the
  members panel) replace `views/TeamsView.tsx` (delete it). Styles:
  `styles/teams-landing.css` + `styles/team-page.css` (delete `team-board.css`).
- **TaskRow extension (additive only):** optional `assigneeAvatar?: {initials: string,
  tone: number}` prop → renders a small avatar in the meta cluster; and optional
  `hideMeta?: ("period")[]`-style suppression is NOT needed — team rows simply don't
  get horizon/period chips today (TaskRow never showed period; verify and leave alone).
  Existing consumers unaffected.
- **Avatar tones:** deterministic per member (hash of member id → small token-based
  tone palette), consistent between chips, sections, cards, and row avatars.

## Deliberate deviations from the mocks (decided; do not "fix")

1. NO theme toggle in page headers — the global 3-state toggle lives in the sidebar
   footer only (mock's header toggle is a demo affordance).
2. Delete team: disabled (with the mock's hint "Move or delete this team's tasks
   first") while the team has ANY tasks — matches the existing 409 which blocks on any
   tasks, not just open ones.
3. Team page quick-add creates with horizon daily + today's period (de-emphasized
   horizon per TEAMS_FEATURES.md); `@Name` assigns; when an avatar filter is active the
   created task auto-assigns to the filtered member (mock behavior) unless `@Name`
   overrides; the Unassigned filter auto-assigns nobody.

## Interaction contract (from README "10", binding)

- Avatar filter: single-select; scopes BOTH flat and grouped modes; selected chip =
  --accent-soft bg + --accent border; Unassigned chip scopes to assigneeId-less tasks.
- Flat mode default: due-date sorted (no due date last, then createdAt); overdue pinned
  on top under a red group header matching All-tasks' Overdue treatment.
- Grouped mode: member sections (avatar + name + count + hairline) with inline
  "Add for ‹name›…" rows; Unassigned section ALWAYS rendered, ALWAYS last.
- Multi-select: leading checkbox revealed on row hover OR when any selection exists;
  selected row = --accent-soft bg + --accent border. Bulk bar (same mechanics as
  Attention): "N selected" + clear · Reassign to <member select> · Unassign · Set due
  date (<input type="date">) · Mark done · Delete. EVERY bulk action undoable:
  reassign/unassign/set-due/done snapshot prior field values and PATCH back on undo;
  delete restores via POST /api/tasks/restore.
- Members panel: 452px slide-in (same mechanics/easing as task detail); list (38px
  avatar, name, muted role, faint email) + edit/remove; add form (name, email, role);
  team rename; gated Delete team. Member remove keeps confirm() (not restorable).
- Status count chips in team header: todo / in_progress / done counts over the team's
  tasks (unfiltered by the avatar selection).

## Verification bar

Backend: go vet/test clean; counts verified live (side port) incl. overdue math.
UI: tsc + build clean; TeamsView/team-board.css gone; all colors via tokens; every
bulk action + undo exercised against the API (curl-level for wiring; UI-level manual).
