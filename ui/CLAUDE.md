# ui/ — React SPA

React 19 + TypeScript + Vite. **No router library** — navigation is hand-rolled:

- `src/App.tsx` — `NavKey` tab state (`day|week|month|all|attention|teams|search`)
  + conditional render. The only "nested route" is `teamPageId` state → `TeamPage`
  (persisted to localStorage). A new page = new state + render branch here, or a
  view-local state inside the parent view (like TeamPage's members panel).
- `src/views/` — one file per screen: DayView, WeekView, MonthView, AllTasksView,
  AttentionView, TeamsLanding, TeamPage (largest, ~1200 lines), SearchView.
- `src/components/` — TaskRow, TaskDetail (slide-over), QuickAdd, TaskComposer,
  CompletedFold, Toast.
- `src/api.ts` — the ONLY fetch layer: every endpoint call + all response types
  (`TaskView`, `TeamBoardResponse`, …). New endpoints get added here.
- `src/period.ts` — all date/week math (`today`, `currentWeek`, `toISOWeek`,
  `weekDates`, `addWeeks`, `isOverdue`, label formatters). Reuse these; never
  hand-roll date logic.

## Conventions

- `createdAt`/`completedAt` are full ISO datetimes — `.slice(0, 10)` before
  feeding `period.ts` helpers. `dueDate`/`period` are already date keys.
- "Completed" = `isCompletedStatus` (`done || cancelled`) from
  `CompletedFold.tsx`; both statuses fold into one collapsed "N completed"
  disclosure per list.
- Bulk actions (TeamPage/AttentionView): snapshot prior values →
  `Promise.all` of `api.updateTask`/`api.deleteTask` → `reload()` →
  `showToast(msg, undoFn)`. Undo re-PATCHes the snapshots. Toast is a module
  singleton (6 s auto-dismiss), `<ToastHost/>` mounted once in App.
- Small helpers (`AVATAR_TONES`, `initials`, …) are deliberately duplicated
  per-file — file-ownership convention; don't extract shared modules for them.
- The team composer forces `horizon:"daily", period:today()` on team tasks
  (deliberate deviation #3, `docs/DESIGN_V3_TEAMS.md`) — a team task's `period`
  is NOT a reliable week anchor; use `createdAt`/`dueDate`.
- No pagination pattern exists; every view fetches its full dataset in one call.

## Build

`npm run build` (tsc + vite) → `ui/dist`, served by the Go server fresh from
disk — rebuild + browser refresh, no server restart needed.
