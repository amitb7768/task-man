# taskman v5 — collapse completed in place

User need: active planning views clutter up as done tasks pile in (Day/Week/Month
currently render done/cancelled inline, just dimmed). Keep todo + in-progress the
visible list; tuck completed away, one click from view. UI-ONLY — no backend/API change
(views already hold all tasks + status; header already computes open/done split).

## Behavior

- **Active** = status `todo` or `in_progress`. **Completed** = `done` or `cancelled`
  (both are inactive/closed).
- New shared presentational component `CompletedFold` (ui/src/components/):
  props `{ tasks: TaskView[], onOpen, onChanged, render? }`. Renders nothing when
  `tasks` is empty; otherwise a collapsed disclosure control — mono uppercase label
  `N completed ▸` (rotates to ▾ when open), styled like the existing section headers
  (muted, hairline). Expands to render the completed tasks (TaskRow, retaining their
  existing dim + strikethrough). Default COLLAPSED. Local `useState` per fold instance;
  NOT persisted (resets on navigation — YAGNI).
- Tokens only; the disclosure matches the planning views' section-header anatomy.

## Placement

- **DayView**: primary "Today's tasks" list renders ACTIVE only; a single `CompletedFold`
  at the bottom of that list for the day's completed. The "this week · for context" strip
  renders ACTIVE only (done filtered out — it's secondary context, no fold there).
- **WeekView**: weekly-tasks section + per-day sections render ACTIVE only; ALL completed
  from the whole view collect into ONE `CompletedFold` at the bottom of the view (a flat
  completed list — sub-period grouping is not preserved inside the fold, that's fine for a
  get-it-out-of-the-way pile). Month-context strip: active only.
- **MonthView**: monthly-tasks section + per-week sections render ACTIVE only; ONE
  `CompletedFold` at the bottom for all completed. Context: active only.
- **Team board (TeamPage)**:
  - Grouped mode: each member section (and Unassigned) renders active cards; that
    section's completed fold at its bottom (one fold per member — natural per-person review).
  - Flat mode: active list stays as today (due-sorted, closed-last already); completed
    collect into ONE `CompletedFold` at the bottom of the flat list.
  - Existing avatar-filter, bulk selection, and the header "todo/in-progress/done" status
    count chips are UNCHANGED. A completed task that's expanded in a fold can still be
    bulk-selected (it just isn't shown when the fold is collapsed).

## Untouched

- **All tasks** (already open-only), **Attention** (overdue/slipped open only) — no done
  shown, no change. **Search** — a query tool; keep showing all matches incl. done.
- Marking a task done in an active list moves it into the fold on the next render (data
  reload already happens via notifyTasksChanged/onChanged) — the satisfying check-it-off-
  and-it-drops-away behavior. No special handling needed.

## Verification

tsc + build clean; done tasks no longer sit inline in Day/Week/Month active lists;
CompletedFold hidden when zero completed; expand/collapse works; team board bulk
selection + status count chips still correct; All-tasks/Attention/Search unchanged.
Tokens-only; no index.css edits (component styles in a view/own stylesheet).
