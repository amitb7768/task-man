# Starting prompt for Claude — taskman Teams screens (v3)

Copy everything below the line into claude.ai (same flow as the first redesign:
iterate per deliverable, bring the HTML mockups back to be translated into the app).

---

I'm extending **taskman**, my local single-operator task manager, with a redesigned
Teams experience. You previously produced the app's full design system — these new
screens must reuse it exactly (tokens, components, and interaction rules below).
Design two screens as self-contained HTML+CSS mockups I can open in a browser.

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
- App shell: 244px sidebar (Plan/Review/Manage groups), main column with mono eyebrow +
  32px serif title header. New screens render in the main column; "Teams" is the active
  nav item.
- Task row (48px, --surface, 1px --border, 10px radius): 20px cycling status control
  (todo=empty square · in-progress=◐ on --warn-soft · done=✓ on --done · cancelled=✕) ·
  3px priority tick (high --overdue, med --warn, low --accent) · title · meta cluster
  (progress chip, due chip — --overdue text on --overdue-soft when overdue) · hover
  action cluster (done / reschedule / delete) over a surface gradient. Done/cancelled
  rows dim .6 + strikethrough.
- 34px initials avatars (token-tone backgrounds), mono pill chips, undo toast (dark
  pill, bottom-center), floating bulk bar (slides up when ≥1 selected — exists in the
  Attention view), slide-in detail panel (452px right).
- Quick-add: 56px field, Enter creates, inline tokens (`!high ^fri @Name`), "Will
  create" preview.

## The product problem

The current Teams view is a member-columns board — the filter is baked into the layout
(one column = one person), so there's no due-date urgency view across people, and it
gets confusing fast. Research across Asana/Linear/Jira/ClickUp says: default to a flat
sortable list; make person-scoping an explicit one-click filter; never hide unassigned
work. The manager (me) is the only user — members are records, they don't log in.

## Screen 1 — Teams landing

Route: clicking "Teams" in the sidebar. One card per team (grid or rows — your call):
team name (serif), member avatar stack, mono meta (member count · open tasks), and an
overdue count as a red badge (--overdue-soft pill, hidden at 0). Whole card clicks
through to Screen 2. "New team" affordance (inline input or minimal dialog — name
only). Empty state consistent with the app's existing dashed-panel style.

## Screen 2 — Team page (members & tasks)

Header: back-to-teams affordance + serif team name + status count chips
(todo / in-progress / done counts) + "Members" button (opens the members panel).

Below the header, in order:
1. **Avatar filter row**: one chip per member — 34px initials avatar + first name +
   open-task workload count; a red overdue count marker when that member has overdue
   tasks; an "Unassigned" chip at the end. Click = scope list to that person
   (selected state: --accent-soft bg, --accent border); click again = clear.
   Single-select.
2. **Toolbar**: "Group by member" toggle + quick-add (existing component; `@Name`
   assigns) + open count.
3. **Task list**:
   - Flat mode (default): due-date sorted, overdue pinned at top with a red group
     header (matches All-tasks' Overdue group treatment).
   - Grouped mode: a section per member (avatar + name + count + hairline rule, mono
     label) each with an inline "add for <name>…" row; **Unassigned section always
     last and always rendered**.
   - Rows reuse the task-row anatomy, plus an assignee avatar in the meta cluster.
     NO horizon/period chips on team rows — due date is the tracking primitive here.
   - Multi-select: leading checkboxes like the Attention view; selecting ≥1 slides up
     the bulk bar: "N selected" · Reassign to <member select> / Unassign · Set due
     date · Mark done · Delete (destructive style). All actions → undo toast.
4. **Members panel** (slide-in from right, same mechanics as the detail panel):
   member list (avatar, name, role muted, email faint) with edit/remove; add-member
   form (name, email, role); team rename + delete (danger, disabled with a hint while
   the team has tasks).

States to show in the mockup: flat default · grouped · one member filtered (1:1 mode) ·
bulk-selection with bar visible · members panel open. Sample data: 2 teams, 4-5 members,
~12 tasks with a few overdue and 2 unassigned.

## Hard constraints (unchanged from the first handoff)

Plain CSS only, tokens above via custom properties, both themes (`prefers-color-scheme`
+ `data-theme` override), native controls (`<select>`, `<input type="date">`), inline
SVG icons only, keyboard-visible focus states, desktop-first, each deliverable ONE
self-contained HTML file with realistic sample data.

Start with Screen 1, show it in both themes, then move to Screen 2 after I approve.
