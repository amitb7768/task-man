# Starting prompt for Claude — taskman Task Composer (deliverable 11)

Copy everything below the line into claude.ai (same project/flow as before — this adds
one component to the existing design system; bring the HTML mockup back to implement).

---

I'm overhauling task creation in **taskman**. You've already designed the app's full
system (tokens, app shell, task row, teams landing 09, team page 10). Design ONE new
component as a self-contained HTML+CSS mockup: **11 · Task Composer** — an expandable
quick-add that grows into a full creation card. It replaces the bare token-only
quick-add on the Team page first, but is ONE shared component that will later serve
the Day/Week/Month headers too.

## The problem

Creation today is a single input where power-user tokens (`!high ^fri @Name`) are the
ONLY way to set fields — undiscoverable, no description at all, and per-member inline
adds create title-only tasks. Research across Linear/Asana/ClickUp/Todoist says: pair a
fast title-only path with a fuller-fields path in the same control; expose properties
as a Linear-style pill row (icon+placeholder when empty, icon+value when set); keep
everything except title optional; never trap focus or lose a draft on Escape.

## Design system (reuse exactly — same tokens/typography as deliverables 01–10)

Same token set as the project (light `--bg #F7F4EE … --accent #455AC6` / dark
`--bg #161512 … --accent #8A97EE` — you have the full tables from 01). Newsreader for
serif, IBM Plex Sans for UI, IBM Plex Mono for meta. 4px spacing, radii 6/10/12–14/999,
shadows sm/md/lg. Existing pieces to reuse: 34px initials avatars + avatar chips (from
10), mono pill chips, native `<input type="date">`/`<select>`, recurrence preset chips +
"every N" interval input (from 05's recurrence editor), `⏎` kbd hints (from 08).

## Component spec — two states, one control

### Collapsed (rest state)
Visually the existing 56px quick-add bar (from 08): placeholder "Add a task for the
team…", `⏎` kbd hint. Enter still creates a title-only task instantly (fast path).
NEW: an expand affordance at the right edge of the field (chevron/sliders icon button)
— clicking it, or pressing Shift+Enter, expands the composer in place.

### Expanded (composer card)
The bar grows into a card (`--surface`, 12–14px radius, `--shadow-md`, anchored — the
list below shifts down; NOT a centered modal, no backdrop):
1. **Title input** — borderless, 15–16px, autofocus. If the user had typed tokens
   before expanding, they are parsed ONCE on expand: pills below prefill and the title
   is cleaned (no live token parsing while expanded — pills are the interface here).
2. **Description** — borderless textarea, 2–3 rows, muted placeholder "Add details…".
3. **Property pill row** (Linear-style, wraps): each pill = icon + label; empty state
   shows placeholder ("Assignee", "Due date", "Priority", "Repeat"), set state shows
   the value (avatar+name / "Fri, Jul 11" mono / priority dot+word / "Weekly ×2").
   Click behavior:
   - **Assignee** → in-place popover listing team members as avatar chips (the team
     page's chip anatomy) + "Unassigned"; pre-scoped to the current team; if a member
     filter is active on the page, that member is the pill's DEFAULT value.
   - **Due date** → native `<input type="date">` inline in a small popover + "Clear".
   - **Priority** → popover with none/low/medium/high rows (colored dots per system).
   - **Repeat** → popover with the recurrence presets (Daily/Weekly/Monthly/Weekdays),
     "every N" number input, weekday chips when Weekdays — same anatomy as 05.
   Only ONE popover open at a time; popovers are small cards (--shadow-md), never
   nested modals; Esc closes the popover first, then collapses the composer.
4. **Footer row**: left — "Create more" toggle (switch + label; when ON, after create
   the composer stays open and KEEPS assignee/priority/due/repeat as defaults for the
   next task, clearing only title/description). Right — mono kbd hint `⌘⏎` + primary
   **Create** button (accent).

### Behavior rules
- Only title is required. Create disabled (muted) while title is empty.
- Esc: closes any open popover; second Esc collapses the card — the draft is PRESERVED
  (re-expanding restores it). Nothing is ever lost silently.
- Cmd/Ctrl+Enter creates from anywhere in the card; plain Enter creates only from the
  title field when no popover is open.
- After create (create-more OFF): card collapses back to the rest bar, success is a
  brief inline confirmation (subtle — no toast needed for creation).
- Team context: created tasks default horizon daily + today (period is invisible
  here); if a Repeat is chosen, the horizon silently follows the recurrence
  (weekly repeat → weekly task) — no horizon UI in the composer.

## States to show in the mockup
Collapsed rest · collapsed with typed text · expanded empty · expanded fully filled
(assignee+due+priority+repeat set) · assignee popover open · create-more ON after one
create (fields carried over, title empty). Both themes (`prefers-color-scheme` +
`data-theme` override). Realistic sample data (reuse the team/members from mock 10).

## Hard constraints (unchanged)
Plain CSS, tokens only, native controls, inline SVG icons, visible focus states,
desktop-first, ONE self-contained HTML file.
