# Starting prompt for Claude — taskman week-scoped team board, rollover & history (v6)

Copy everything below the line into claude.ai (same flow as the earlier
redesigns: iterate per deliverable, bring the HTML mockups back to be
translated into the app). Feature contract: `DESIGN_V6_WEEK_ROLLOVER.md`.

---

I'm extending **taskman**, my small-team task manager, with a week-scoped Team
board plus two new screens. You previously produced the app's full design
system — these screens must reuse it exactly (tokens, components, and
interaction rules below). Deliverables are self-contained HTML+CSS mockups I
can open in a browser.

## Existing design system (authoritative — reuse, don't reinvent)

**Tokens** (CSS custom properties; light on `:root`, dark under
`@media (prefers-color-scheme: dark)` and `:root[data-theme="dark"]`):

Light: `--bg #F7F4EE · --surface #FFFFFF · --surface-2 #F0ECE3 · --surface-3 #E9E4D8 ·
--border #E4DED2 · --border-strong #D3CBBB · --text #22201B · --text-muted #6E675B ·
--text-faint #9C9484 · --accent #455AC6 · --accent-soft #E6E9F8 · --accent-text #FFFFFF ·
--done #3C7A55 · --done-soft #E1EFE6 · --overdue #B5462B · --overdue-soft #F6E4DD ·
--warn #B07D22 · --warn-soft #F5EBD4`

Dark: `--bg #161512 · --surface #201E1A · --surface-2 #282520 · --surface-3 #322E28 ·
--border #332F29 · --border-strong #463F37 · --text #ECE7DC · --text-muted #A79F90 ·
--text-faint #736B5D · --accent #8A97EE · --accent-soft #282B44 · --accent-text #14131A ·
--done #6FB68C · --done-soft #1E2E26 · --overdue #E08163 · --overdue-soft #33221C ·
--warn #D9AD57 · --warn-soft #302816`

**Type:** Newsreader 500 for serif titles (view title 32px, section 26px); IBM Plex Sans
400/500/600 for UI (body 14–15px); IBM Plex Mono 400/500 for meta/eyebrows (11px
uppercase, letter-spacing .12–.16em). **Spacing:** 4px base scale. **Radius:** 6px
controls · 10px rows · 12–14px cards/panels · 999px chips/avatars. **Shadows:**
sm `0 1px 2px rgba(30,25,15,.06)` · md `0 4px 16px -6px rgba(30,25,15,.14)` ·
lg `0 18px 44px -14px rgba(30,25,15,.24)` (dark equivalents rgba(0,0,0,.4/.5/.6)).

**Existing components these screens must reuse:**
- App shell: 244px sidebar, main column with mono eyebrow + 32px serif title
  header. "Teams" is the active nav item; all three screens render in the main
  column of the Team page (which has its own back-to-teams header).
- Task row (48px, --surface, 1px --border, 10px radius): 20px cycling status
  control (todo=empty square · in-progress=◐ on --warn-soft · done=✓ on --done ·
  cancelled=✕) · 3px priority tick (high --overdue, med --warn, low --accent) ·
  title · meta cluster (progress chip, due chip, assignee avatar) · hover action
  cluster. Done/cancelled rows dim .6 + strikethrough.
- Completed fold: a full-width disclosure row at the bottom of a list —
  "N completed ▸" mono label, expands in place to dimmed rows.
- 34px initials avatars, mono pill chips, undo toast (dark pill, bottom-center),
  floating bulk bar (slides up when ≥1 selected), leading checkboxes on rows in
  selection contexts.
- The app now has logins and roles: **ADMIN** (manager) and **USER** (member).
  Some affordances below are ADMIN-only — show both variants where asked.

## The product problem

The team board currently shows every task ever created, forever. I'm making it
**week-scoped**: it shows this week's working set — tasks anchored to the
current ISO week (Mon–Sun) plus anything with a due date (dated tasks never
expire off the board until closed). Undated leftovers from past weeks disappear
from the board and pile up in a **rollover** queue that only the ADMIN can
triage: bulk-move them into the current week, mark done, or cancel. Completed
tasks fold away as today, but the fold only holds **this week's** completions —
older ones live on a paginated per-team **history** page.

## Screen 1 — Team board, week-scoped (revision of the existing team page)

Keep the existing team page exactly as-is (header, avatar filter row, toolbar,
flat/grouped list, bulk bar, members panel) with three additions:

1. **Week context**: the header's mono eyebrow line gains the current week —
   e.g. `TEAM · 2026-W29 · JUL 13–19`. Subtle, not a control.
2. **Rollover banner (ADMIN only)**: between toolbar and list, a slim
   --warn-soft banner: "6 tasks from past weeks need review" + a "Review →"
   button (opens Screen 2). Show a variant without it (USER view / zero stale).
3. **Fold footer link**: inside the expanded completed fold, a final quiet row:
   "View older →" (opens Screen 3).

States to show: ADMIN with banner · USER (no banner) · fold expanded showing
the footer link.

## Screen 2 — Rollover (ADMIN only, full main-column screen)

Header: back-to-board affordance + serif title "Rollover" + team name muted +
mono meta ("6 open tasks from 3 past weeks").

- List of stale open tasks **grouped by week**, oldest first: mono week group
  headers (`2026-W26 · 2 TASKS`) with hairline rules, task rows beneath (reuse
  row anatomy; these tasks have no due chip — they're undated by definition;
  show assignee avatar).
- Leading checkboxes + a select-all control per week group and globally.
- **Recurring tasks** get a small mono "↻ recurring" pill — they can be marked
  done/cancelled but are *skipped by Move* (the bulk bar hints this when the
  selection contains one).
- Bulk bar (slides up when ≥1 selected): "N selected" · **Move to this week**
  (primary, --accent) · Mark done · Cancel (quiet destructive). All actions →
  undo toast.
- Empty state: dashed panel, "Nothing to roll over — all caught up", with a
  back-to-board link.

States to show: default with mixed weeks + one recurring row · selection active
with bulk bar (including the recurring-skip hint) · empty state.

## Screen 3 — Team history (full main-column screen)

Header: back-to-board affordance + serif title "History" + team name muted +
mono meta ("completed & cancelled · past weeks").

- Flat list of done/cancelled task rows, **newest first**, dimmed +
  strikethrough as usual; due chip replaced by a status chip ("Done · Jul 8" on
  --done-soft, "Cancelled" on --surface-3); assignee avatar in the meta cluster.
- Optional quiet mono week separators if it helps scanability — your call.
- **"Load more" button** centered after the list (loads the next 50); a subtle
  end-of-list line when exhausted ("That's everything").
- Empty state consistent with the app's dashed-panel style.
- Read-only rows: no hover action cluster, no checkboxes — clicking a row still
  opens the detail slide-over.

States to show: populated mid-pagination (Load more visible) · exhausted ·
empty.

## Sample data

One team ("Platform"), 4 members. Board: ~9 current-week tasks (2 overdue with
due dates, 1 due next month — still visible), fold with 3 completions, banner
count 6. Rollover: 6 undated tasks across W26–W28, one recurring. History: ~14
rows spanning several weeks.

## Hard constraints (unchanged from the first handoff)

Plain CSS only, tokens above via custom properties, both themes
(`prefers-color-scheme` + `data-theme` override), native controls (`<select>`,
`<input type="date">`), inline SVG icons only, keyboard-visible focus states,
desktop-first, each deliverable ONE self-contained HTML file with realistic
sample data.

Start with Screen 1, show it in both themes, then move to Screens 2 and 3 after
I approve.
