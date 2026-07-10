# taskman v3.1 — Task Composer build contract

Pixel/behavior spec: "/Users/amitbind/Downloads/pbh/taskman/Taskman UI redesign (2)/11 Task Composer.dc.html"
(34KB; the only new artifact — other files in that dir are byte-identical to
`design_handoff_taskman_ui 2/`; the nested copy there is stale — ignore it).
Written spec (authoritative where the mock is ambiguous): docs/DESIGN_PROMPT_COMPOSER.md.
Research: docs/research-task-composer.md. Standing rules: DESIGN_V2_UI.md (tokens-only,
CSS ownership, "" vs null) + DESIGN_V3_TEAMS.md interaction contract.

## Scope

ONE new shared component: `ui/src/components/TaskComposer.tsx` + `ui/src/styles/composer.css`.
Wired on the TEAM PAGE ONLY this pass (replaces the top-toolbar QuickAdd there).
- QuickAdd component itself is untouched and continues to serve Day/Week/Month headers
  (swap to composer is a later pass).
- TeamPage grouped-mode per-member "Add for ‹name›…" inline rows stay as-is (they're the
  assignee-first fast path; composer is the full path).

## Behavior contract (from DESIGN_PROMPT_COMPOSER.md — binding)

- Collapsed = 56px quick-add bar; Enter creates title-only (fast path, token parsing
  UNCHANGED via quickAddParser); expand affordance button + Shift+Enter expands in place
  (card, --shadow-md, list shifts down, no backdrop/modal).
- On expand: run quickAddParser ONCE over typed text → prefill pills, clean title.
  No live token parsing while expanded.
- Expanded card: borderless title input (autofocus) → description textarea (maps to
  `notes`) → property pill row (Assignee / Due date / Priority / Repeat; empty=icon+
  placeholder, set=icon+value) → footer (Create-more toggle · ⌘⏎ hint · Create button).
- Pill popovers (one open at a time, never nested): Assignee = team-scoped avatar chips
  + Unassigned, default = page's filtered member; Due = native <input type="date"> +
  Clear; Priority = none/low/medium/high rows with dots; Repeat = recurrence presets +
  every-N + weekday chips (same anatomy as detail panel).
- Only title required; Create disabled while empty. Esc closes popover first, then
  collapses WITH DRAFT PRESERVED (re-expand restores). Cmd/Ctrl+Enter creates from
  anywhere in card; plain Enter creates only from title with no popover open.
- Create-more ON: composer stays open after create; keeps assignee/priority/due/repeat;
  clears title/description. OFF: collapse to rest bar + brief inline confirmation
  (no toast for creation).
- Recurrence→horizon coupling (client-side): repeat chosen → horizon follows freq
  (daily/weekdays→daily+today, weekly→weekly+currentWeek, monthly→monthly+currentMonth);
  no repeat → daily+today. No horizon UI.
- notifyTasksChanged() after every create.

## Phase-0 architecture rules (from docs/BACKEND_ARCH_REVIEW.md — binding)

1. ALL creation goes through existing POST /api/tasks. No new endpoints, no api.ts
   endpoint additions (types only if needed).
2. Do not deepen the "personal = teamId:null" assumption anywhere new.

## Ownership

UI agent owns: components/TaskComposer.tsx (new), styles/composer.css (new),
views/TeamPage.tsx (ONLY the toolbar wiring swap QuickAdd→TaskComposer + passing team
members + filtered-member default), api.ts (types only if needed). Nothing else — not
QuickAdd, not index.css, not server/.

## v3.2 — Personal rollout (Day/Week/Month headers)

The SAME TaskComposer replaces the QuickAdd in the App header for the three planning
tabs. Differences are driven by a `context` prop ("team" vs "personal"):

- **Assignee pill: hidden** in personal context; `@Name` tokens are ignored (parser may
  strip them — parity with the old header QuickAdd, which also ignored assigneeName).
  No teamId/assigneeId ever sent.
- **Horizon/period:** default = the ACTIVE VIEW's horizon and its CURRENTLY VIEWED
  period (Day → daily + viewed date, Week → weekly + viewed week, Month → monthly +
  viewed month) — exact parity with the old App handleQuickAdd derivation from
  dayDate/weekPeriod/monthPeriod state. `#horizon` tokens ARE honored in personal
  context (the flag the team fix reserved); recurrence→horizon coupling still wins as
  today (parser precedent). Period for an overridden horizon = that tab's current
  period state (existing App behavior — keep parity, no cleverness).
- **Expansion layout:** in the header the card must NOT push the shell — it expands as
  an anchored overlay dropping over the content (absolute, --shadow-md), same
  precedent as the old QuickAdd preview panel. Team page keeps the in-flow push.
- **Draft:** one composer instance across the three tabs; horizon badge follows the
  active tab/period live; non-title draft fields survive tab switches (harmless,
  visible via the draft chip).
- **Repeat pill note:** collapsed badge shows the view's horizon+period exactly as the
  old QuickAdd did (e.g. "Daily · Today", "Weekly · W28").
- **QuickAdd retirement:** after the swap, if QuickAdd.tsx has zero remaining
  consumers, DELETE it (quickAddParser.ts stays — the composer uses it). Its styles in
  index.css may remain (foundation-owned) — do not edit index.css; just note orphaned
  selectors for a later sweep if any.
- Ownership for this pass: TaskComposer.tsx + composer.css (context variant),
  App.tsx (header swap + wiring only), QuickAdd.tsx (deletion only if orphaned).
  Phase-0 rules unchanged (POST /api/tasks only).

## Verification bar

tsc + build clean; tokens-only colors; live curl proof of one full-field create (title+
notes+due+priority+assignee+recurrence) through POST /api/tasks with correct horizon
coupling; draft-preservation and create-more verified by code inspection; :8484 bundle
refreshed. Then opus review (focus: draft/keyboard state machine, popover focus
handling, parser one-shot hydration correctness, create-more carry-over, pill states)
→ fixes → final verify.
