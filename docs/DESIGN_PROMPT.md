# Starting prompt for Claude — taskman UI redesign

Copy everything below the line into claude.ai (or Claude Code) to kick off the UI
design work. Iterate component by component; bring the resulting HTML/CSS mockups
back here to be translated into the app (React + TS + plain CSS).

---

I'm redesigning the UI of **taskman**, a personal + team task manager I run locally.
The current UI works but is cramped and unintuitive. I want you to act as a product
designer: propose a clean visual system, then design components one at a time as
self-contained HTML+CSS mockups I can review in the browser and hand to my dev setup.

## Product in one paragraph

Single operator (me). I plan personal tasks on three horizons — **daily, weekly,
monthly** — each task anchored to a concrete period (a date, an ISO week, or a month).
Tasks have: title, optional notes, optional due date, status (todo / in-progress /
done / cancelled), priority (none/low/medium/high), optional recurrence preset, and
optional subtasks (a child's horizon must be ≤ its parent's — e.g. weekly subtasks
under a monthly goal, with a done/total progress count on the parent). Separately I
track **teams**: members are records (not users); each team has a board grouping
tasks by assignee, and I update their statuses myself.

## Information architecture (already decided — design within this)

Left **sidebar navigation** (replacing the current top tabs):
- Day (today's tasks + collapsed strip of this week's tasks for context)
- Week (this week's weekly tasks + per-day daily tasks)
- Month (this month's monthly tasks + per-week weekly tasks)
- All tasks (flat list of every open task: state, due date, quick actions)
- Attention (overdue tasks + "slipped" tasks whose period passed undone; bulk
  "reschedule to today")
- Teams (team list → member-grouped board)
- Search (text + status/priority/horizon/team filters)

A slide-in **detail panel** edits one task: title, notes, due date, priority,
recurrence editor, subtask list with add, delete.

## Components to design (one at a time, in this order)

1. **Design tokens** — CSS custom properties: color palette (light AND dark via
   `prefers-color-scheme`), spacing scale, type scale, radii, shadows. Calm,
   focused, generous whitespace; a planning tool, not a dashboard.
2. **App shell** — sidebar nav (icons + labels, active state, collapsed variant)
   + content header (view title, period navigation ‹ today ›, quick-add input).
3. **Task row** — the workhorse. Status control (checkbox + 4-state affordance),
   title, priority badge, due-date chip (distinct overdue state), progress chip
   (e.g. 3/5), recurrence indicator, team badge, hover actions (done, reschedule
   to today, delete). Compact but breathable; readable at 30+ rows.
4. **All-tasks listing** — task rows with grouping/sorting affordances (overdue
   first, then due date, then priority) and an empty state.
5. **Task detail panel** — slide-in editor; form layout for the fields above;
   subtask section with inline add.
6. **Attention view** — overdue + slipped sections, multi-select, bulk action bar.
7. **Team board** — member-grouped columns/sections with per-member quick-add.
8. **Quick-add** — single input, Enter to create in current view's horizon+period.

## Hard constraints

- Plain CSS only (CSS variables encouraged). No Tailwind, no component libraries,
  no icon fonts — inline SVG or unicode glyphs are fine.
- Must look right in BOTH light and dark (`prefers-color-scheme`).
- Native controls where possible (`<input type="date">`, `<select>`).
- Keyboard-first: quick-add is the primary creation path; visible focus states.
- Each deliverable = ONE self-contained HTML file (inline CSS, realistic sample
  data, both themes testable) that I can open directly in a browser.
- Desktop-first (it runs on my machine); don't burn effort below ~1000px width.

## Current pain points to fix

- Everything is cramped: rows, panels, and controls sit too close; no visual
  hierarchy between a view's primary list and its context strips.
- Top tabs don't scale and hide where I am; move to sidebar (done above).
- Status/priority/due information doesn't scan — I have to read each row.
- The detail panel feels like a form dump, not an editor.

Start with deliverable 1 (design tokens) and show me the palette applied to a
miniature preview (a sample task row in both themes) before moving on.
