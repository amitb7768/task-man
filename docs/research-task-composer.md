# Task Composer Anatomy — Research for a New Creation Component

Grounds a redesign of taskman's composer (currently a 56px quick-add field with inline tokens `!high ^fri @Name *weekly` + separate title-only per-member rows — verdict: "sucks," no visible way to set description/due date/assignee/priority). Studied Linear, Asana, ClickUp, Todoist, Height, Monday.com. Context: single manager operator, no logins, team members are records — "assignee" always means "pick one of my people," never "who's logged in."

## 1. Composer form factor

**Convention: two-speed, not one widget.** Every tool pairs a fast title-only surface with a fuller-fields surface, escalating between them rather than forcing a choice up front.
- **Linear**: `C` → **modal** (title + property-pill row); `V` → **full-screen editor** with the same fields + description body. Same data, two chrome weights.
- **Asana**: inline add row (title-only, Enter creates) is the fast path; the created row opens a **side panel** for everything else — creation and full edit are separate surfaces, not one expanding form.
- **ClickUp**: inline `+ New Task` field for a bare task, or expand-before-submit for the full modal with all fields present at once.
- **Todoist**: single-line **quick add overlay** (`Q`), never expands to a modal — fields come from parsed tokens + small pickers docked to the same bar.
- **Monday.com**: pure inline — Enter commits a bare item; fields filled in after as row/column cells (spreadsheet model, no modal).
- UX literature consensus: modals earn their weight only for focused, attention-worthy decisions; simple/repeatable additions belong inline or in a low-chrome popover.

**Fit:** Linear's split (light popover, expands in place) suits taskman better than Asana's two-surface model — taskman has no "task detail" destination to route to.

## 2. Field anatomy and ordering

**Convention:** title is the only required field; everything else sits in **one property row under the title**, not a vertical labeled form.
- **Linear**: title → collapsed description → horizontal **pill buttons** (Status, Priority, Assignee, Labels, Due date). Empty = icon + placeholder text; set = icon + value. Click opens an in-place popover, no navigation away.
- **Asana**: opposite — **labeled fields** stacked vertically, always visible, because creation and detail share one surface. Scans well, adds friction to add.
- **ClickUp**: hybrid — name field, then icon-triggered fields (people/calendar/flag icons) in a row, expanding to a modal with description + tags below.
- **Empty states**: neutral placeholder + icon, never a required-looking red field. ClickUp's "make standard fields required" request (571+ votes, unshipped since 2020) shows mature tools deliberately keep assignee/priority/due-date optional.

**Fit:** Linear's pill-row directly answers the "sucks" complaint — it shows *you can* set assignee/priority/due date without turning quick-add into a form. Order: title → notes (collapsed, one click) → pill row.

## 3. Natural-language / token input

**Convention:** tokens and explicit controls are **the same state, two views**; the explicit control always wins on conflict.
- **Todoist**: `p1`, `@label`, `#project`, `+assignee`, date phrases parse live, shown as a highlighted span + preview line, and become real field values on submit. The date-picker icon quick-add also exposes can override a parsed date.
- **Linear**: no free-text grammar — same fast-entry idea via single-key shortcuts (`P` priority, `A` assignee, `L` label) fired while title has focus, popping the matching pill's dropdown.
- **Pattern to copy — "chips hydrate into controls":** a recognized token doesn't just tag text, it fills the *same* pill shown in the property row, so it can then be edited via normal dropdown. No divergent "parsed" vs "edited" state.

**Fit:** keep `!high ^fri @Name *weekly` but route each token into the same pill-row fields the UI also exposes as controls. Editing a pill after typing a token must visually clear/replace the token span — text and fields never disagree.

## 4. "Create more" flows

**Convention:** a persistent toggle that keeps the composer open post-submit and **carries selected fields forward**, since batch entry is the common case.
- **Linear**: **"Create more" toggle** in the footer; on submit, re-opens fresh with **assignee, labels, project, priority pre-filled** from the just-created issue — only title/description reset. `Cmd/Ctrl+Shift+Enter` does the same in one keystroke (also how sub-issues inherit parent team/priority/project).
- **Keyboard submission**: `Cmd/Ctrl+Enter` = create-and-close is the near-universal modifier-submit for a composer living in a text field; plain `Enter` is for single-line inline adds (Asana row, Monday cell, ClickUp bare task) with no multi-line field competing for Enter.

**Fit:** matches the real workflow — "three tasks for Priya, high priority, due Friday." Carry assignee + priority + due date forward on `Cmd/Ctrl+Enter` (title/notes/recurrence never carry over); show a small "creating for `<Name>` →" indicator during the streak.

## 5. Assignee selection UX in-team

**Convention (thinnest-documented area):** avatar + name chip, not a text dropdown, in both the pill and the resulting row. Pickers are type-to-filter lists of initials avatars **pre-scoped to the current team**, not the whole org — the one broadly-confirmed pattern. "Assign to me" (Linear `I`) is irrelevant here since the operator never appears as an assignee. No tool documented a distinct "recent assignees" strip — at small team scale the full filtered list *is* the fast path.

**Fit:** reuse taskman's existing Teams-view avatar-chip component (with workload counts) as the assignee pill's picker instead of inventing a "recent" mechanism; default the pill to the currently-filtered team member when opened from a person-scoped view.

## 6. Anti-patterns

- **Required fields beyond title.** ClickUp users have asked 6+ years to make due-date/priority/assignee required at creation, and it's deliberately unshipped — forcing decisions at creation is what makes a composer feel like paperwork.
- **Focus traps without an escape.** Accessible-modal guidance is unanimous: Escape must close, focus must return to the trigger, tab order must be predictable.
- **Nested modals** (a date/assignee picker that itself blocks the parent form) — use in-place popovers off each pill instead of a second modal layer.
- **No draft preservation.** Linear's de facto standard: composers auto-save the moment typing starts, so Escape/navigate-away never silently discards a task.
- **Modal fatigue from over-use.** Reserve modal chrome for creation and destructive confirms only; keep status-cycle/reassign as inline row controls (taskman's task-row anatomy already does this correctly).

## Recommended composer shape

**Form factor:** one expandable popover anchored to the existing quick-add bar, not a centered modal. Title is the only thing visible at rest; typing/focus reveals the pill row beneath. No separate "full editor" surface — that's Asana's task-pane job, and taskman has no detail destination to justify duplicating it.

**Field layout, top to bottom:**
1. Title input (autofocus, placeholder "Add a task…").
2. Notes — "+ Add description" affordance under the title, expands to a plain textarea in place; never required.
3. Pill row: **Assignee** (avatar chip, defaults to filtered team member if opened from a person-scoped view) · **Priority** (empty = "No priority") · **Due date** (empty = "No date," native date input popover) · **Recurrence** (empty = "Doesn't repeat"). Icon+placeholder when empty, icon+value when set — reuse existing chip/avatar tokens.

**Token interplay:** `!high ^fri @Name *weekly` write directly into the same four pill fields (no separate parse layer). Recognized tokens render as a subtle inline highlight and simultaneously populate the pill; editing the pill afterward removes/updates the token span. Unrecognized fragments fail silently back to plain text — matches Todoist's live-preview forgiveness.

**Keyboard model:** `Enter` = create (title-only valid); `Cmd/Ctrl+Enter` = create and keep composer open ("create more"), carrying assignee + priority + due date forward, always resetting title/notes/recurrence; `Escape` discards only if title is empty, otherwise the draft persists in the still-open popover.

## Sources
[Linear Create issues](https://linear.app/docs/creating-issues) · [Linear Parent/sub-issues](https://linear.app/docs/parent-and-sub-issues) · [Linear Faster sub-issue creation](https://linear.app/changelog/2022-10-13-faster-sub-issue-creation) · [Linear New issue creation UI](https://linear.app/changelog/2021-02-25-new-issue-creation-ui) · [Linear Invisible details](https://medium.com/linear-app/invisible-details-2ca718b41a44)
[Asana Create & assign tasks](https://help.asana.com/s/article/how-to-create-and-assign-tasks?language=en_US) · [Asana Task pane redesign](https://asana.com/inside-asana/task-pane-redesign) · [Asana Forum field layout](https://forum.asana.com/t/task-pane-improve-layout-of-fields-labels-including-minimizing-wasted-space/70687)
[ClickUp Task fields/description](https://help.clickup.com/hc/en-us/articles/34958796358039-Task-fields-and-the-task-description) · [ClickUp feedback required fields](https://feedback.clickup.com/feature-requests/p/make-standard-fields-properties-due-date-priority-etc-required-when-creating-a-t)
[Todoist Dates and time](https://www.todoist.com/help/articles/introduction-to-dates-and-time-q7VobO) · [Todoist NL guide](https://calmevo.com/todoist-natural-language-input-guide/) · [Height Task forms](https://height.app/product/forms) · [Monday Basics of items](https://support.monday.com/hc/en-us/articles/115005319105-The-basics-of-items)
[LogRocket Modal best practices](https://blog.logrocket.com/ux-design/modal-ux-best-practices/) · [LogRocket Modal design patterns](https://blog.logrocket.com/ux-design/modal-ux-design-patterns-examples-best-practices/) · [Eleken Mastering Modal UX](https://www.eleken.co/blog-posts/modal-ux) · [UXPin Focus traps](https://www.uxpin.com/studio/blog/how-to-build-accessible-modals-with-focus-traps/) · [NN/g Deceptive patterns](https://www.nngroup.com/articles/deceptive-patterns/)
