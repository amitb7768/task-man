// Task Composer (design handoff "11 Task Composer") — the shared expandable
// quick-add: a 56px fast-path bar that grows in place into a full creation
// card (title, notes, and a Linear-style property pill row: assignee / due
// date / priority / repeat). Originally wired on the Team page toolbar only
// (v3.1); v3.2 (docs/DESIGN_V31_COMPOSER.md "Personal rollout") adds a
// `context` variant and reuses the SAME component for the App header's
// Day/Week/Month quick-add, replacing QuickAdd there. QuickAdd itself is
// untouched and keeps serving TeamPage's per-member inline "Add for ‹name›…"
// rows (still a real consumer — not deleted).
//
// context="team" (v3.1, unchanged): teamId + members required; Assignee pill
// shown; horizon/period always daily+today unless a repeat is set (no
// viewed-period concept on the team page).
// context="personal" (v3.2): no Assignee pill, no teamId/assigneeId ever
// sent; horizon/period default to the ACTIVE TAB's own horizon + CURRENTLY
// VIEWED period (`horizon`/`periods` props, parity with the old App
// handleQuickAdd derivation from dayDate/weekPeriod/monthPeriod); #override
// tokens are honored (recurrence coupling still wins); expansion is a
// header-anchored absolute overlay (does not push the shell) instead of the
// team page's in-flow push.
//
// State-machine notes (docs/DESIGN_V31_COMPOSER.md "Behavior contract"):
//  - Collapsed Enter is a pure fast path: parseQuickAdd(title) runs exactly
//    like the legacy QuickAdd and creates directly — it never reads the
//    pending pill draft ("token parsing UNCHANGED").
//  - Expanding runs parseQuickAdd ONCE, merging into whatever pills already
//    exist (a freshly parsed token wins over an existing draft value, which
//    wins over the page's filtered-member default — same merge direction as
//    createFromDraft, so Enter vs Shift+Enter on the same typed text can't
//    disagree) to prefill pills + clean the title. Title/notes never
//    re-parse after that — pills are the only property interface once
//    expanded. Re-expanding after a draft-preserving collapse re-runs the
//    same parse, but since the title has already had its tokens stripped
//    this is a no-op merge — safe to call unconditionally.
//  - Esc closes an open popover first; a second Esc collapses the card
//    without clearing any field (re-expand restores everything, per above).
//  - Exactly one popover open at a time; a document-level mousedown closes
//    it on an outside click, ignoring clicks on a pill trigger or inside the
//    open popover itself (so toggling the SAME pill still closes it instead
//    of flicker-reopening — the trigger's own onClick already toggles).
import { useEffect, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import type { Horizon, Member, Priority, Recurrence, RecurrenceFreq } from "../api";
import { api, ApiError } from "../api";
import { currentMonth, currentWeek, formatShortDayLabel, today, WEEKDAY_LABELS } from "../period";
import { notifyTasksChanged } from "../App";
import { parseQuickAdd } from "./quickAddParser";
import "../styles/composer.css";

interface TaskComposerTeamProps {
  context: "team";
  /** Team this composer creates tasks into. */
  teamId: string;
  /** Team-scoped members shown in the assignee popover. */
  members: Member[];
  /** The page's active member filter — the assignee pill's default on
   *  expand. Pass undefined when no filter (or the Unassigned filter) is
   *  active. */
  defaultAssigneeId?: string;
  /** Called after every successful create so the caller can refresh its own
   *  data (the composer itself calls notifyTasksChanged()). */
  onCreated: () => void;
}

// v3.2 personal rollout (docs/DESIGN_V31_COMPOSER.md "v3.2") — the App-header
// variant used on the Day/Week/Month tabs.
interface TaskComposerPersonalProps {
  context: "personal";
  /** Active tab's default horizon (Day→daily, Week→weekly, Month→monthly) —
   *  used unless a #override token or recurrence coupling (which still wins)
   *  produces a different one. */
  horizon: Horizon;
  /** CURRENTLY VIEWED period for every horizon, keyed by horizon —
   *  {daily: dayDate, weekly: weekPeriod, monthly: monthPeriod}. An
   *  overridden horizon (via #token or recurrence) resolves its period from
   *  ITS OWN entry here, not from `horizon`'s — exact parity with the old
   *  App handleQuickAdd derivation. */
  periods: Record<Horizon, string>;
  /** Collapsed-bar horizon+period badge text, e.g. "Today · Jul 8", "W28",
   *  "July 2026" — pre-formatted by the caller so period-label formatting
   *  stays single-sourced in period.ts / App's metaByTab. */
  periodLabel: string;
  /** Collapsed-bar placeholder (varies per tab). */
  placeholder: string;
  /** Called after every successful create so the caller can refresh its own
   *  data (the composer itself calls notifyTasksChanged()). */
  onCreated: () => void;
}

export type TaskComposerProps = TaskComposerTeamProps | TaskComposerPersonalProps;

type PopoverName = "assignee" | "due" | "priority" | "repeat" | null;

interface RepeatDraft {
  freq: RecurrenceFreq;
  interval: number;
  weekdays: number[];
}

// freq -> horizon of the tasks it spawns (mirrors server/recur.go
// recurrenceHorizon and quickAddParser.ts's private RECUR_HORIZON — the
// composer keeps its own copy since it drives horizon/period entirely
// client-side and has no horizon UI of its own).
const RECUR_HORIZON: Record<RecurrenceFreq, Horizon> = {
  daily: "daily",
  weekdays: "daily",
  weekly: "weekly",
  monthly: "monthly",
};
// Never send an empty weekdays array — the server 400s on it (mirrors
// TaskDetail.tsx's DEFAULT_WEEKDAYS).
const DEFAULT_WEEKDAYS = [1, 2, 3, 4, 5]; // Mon-Fri, ISO Mon=1

// Personal-context collapsed-bar horizon badge label — identical copy to
// App.tsx's own HORIZON_LABEL (small per-file duplication, same established
// pattern as toneIndex/AVATAR_TONES above).
const HORIZON_LABEL: Record<Horizon, string> = { daily: "Daily", weekly: "Weekly", monthly: "Monthly" };

const PRIORITY_OPTIONS: { value: Priority; label: string; dot?: string }[] = [
  { value: "", label: "None" },
  { value: "low", label: "Low", dot: "var(--accent)" },
  { value: "medium", label: "Medium", dot: "var(--warn)" },
  { value: "high", label: "High", dot: "var(--overdue)" },
];
// Pixel spec ("11 Task Composer" mock, showInterval): the "every N" input is
// shown for daily/weekly/monthly but deliberately hidden for weekdays (only
// the weekday chips apply there) — a simplification vs. TaskDetail's fuller
// editor, kept as authored since the mock is unambiguous on this point.
const PRESETS: { value: RecurrenceFreq; label: string }[] = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "weekdays", label: "Weekdays" },
];
const INTERVAL_UNIT: Record<RecurrenceFreq, string> = {
  daily: "days",
  weekly: "weeks",
  monthly: "months",
  weekdays: "weeks",
};

// Deterministic per-member avatar tone — identical copy to
// views/TeamPage.tsx / views/TeamsLanding.tsx / components/TaskRow.tsx
// (docs/DESIGN_V3_TEAMS.md "Avatar tones": small per-file duplication is the
// established pattern for this tiny hash, not a shared util).
function toneIndex(id: string): number {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return h % 4;
}
const AVATAR_TONES: { bg: string; fg: string }[] = [
  { bg: "var(--accent-soft)", fg: "var(--accent)" },
  { bg: "var(--warn-soft)", fg: "var(--warn)" },
  { bg: "var(--done-soft)", fg: "var(--done)" },
  { bg: "var(--surface-3)", fg: "var(--text-muted)" },
];
function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

// Exact full-name match wins; else a unique prefix match — identical
// semantics to TeamPage.tsx's private resolveAssignee, duplicated here so
// the composer stays self-contained for future non-team callers.
function resolveAssigneeName(name: string | undefined, members: Member[]): string | undefined {
  if (!name) return undefined;
  const n = name.toLowerCase();
  const exact = members.find((m) => m.name.toLowerCase() === n);
  if (exact) return exact.id;
  const prefixHits = members.filter((m) => m.name.toLowerCase().startsWith(n));
  return prefixHits.length === 1 ? prefixHits[0].id : undefined;
}

function repeatLabel(r: RepeatDraft | null): string {
  if (!r) return "Repeat";
  const base = r.freq === "weekdays" ? "Weekdays" : r.freq[0].toUpperCase() + r.freq.slice(1);
  return r.interval > 1 ? `${base} ×${r.interval}` : base;
}

// Resolves the effective horizon + period a create() call should use.
//  - Team context (`personal` undefined): unchanged v3.1 rule — a repeat
//    freq implies its horizon (else "daily"); period is always "now"
//    (today/currentWeek/currentMonth), since the team page has no
//    viewed-period state of its own.
//  - Personal context: a repeat freq still wins first (same precedence as
//    team); else a parsed/stored #override token wins; else the active
//    tab's own horizon. Whatever horizon that produces, its period is THAT
//    horizon's own currently-viewed period from `periods` — exact parity
//    with the old App handleQuickAdd derivation (an override never falls
//    back to a fresh today()/currentWeek()/currentMonth() call, it always
//    uses the tab's own current period state).
function horizonAndPeriod(
  freq: RecurrenceFreq | undefined,
  personal?: { horizonToken?: Horizon; viewHorizon: Horizon; periods: Record<Horizon, string> },
): { horizon: Horizon; period: string } {
  if (personal) {
    const horizon: Horizon = freq ? RECUR_HORIZON[freq] : (personal.horizonToken ?? personal.viewHorizon);
    return { horizon, period: personal.periods[horizon] };
  }
  const horizon: Horizon = freq ? RECUR_HORIZON[freq] : "daily";
  const period = horizon === "daily" ? today() : horizon === "weekly" ? currentWeek() : currentMonth();
  return { horizon, period };
}

function buildRecurrence(r: RepeatDraft | null): Recurrence | undefined {
  if (!r) return undefined;
  return {
    freq: r.freq,
    interval: r.interval,
    weekdays: r.freq === "weekdays" ? (r.weekdays.length ? r.weekdays : DEFAULT_WEEKDAYS) : undefined,
  };
}

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

// ---- inline icons (pixel spec "11 Task Composer") ----
function PlusIcon() {
  return (
    <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round">
      <line x1="12" y1="5" x2="12" y2="19" />
      <line x1="5" y1="12" x2="19" y2="12" />
    </svg>
  );
}
function CheckIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 12 10 18 20 6" />
    </svg>
  );
}
function ExpandIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <line x1="4" y1="8" x2="20" y2="8" />
      <line x1="4" y1="16" x2="20" y2="16" />
      <circle cx="9" cy="8" r="2.2" fill="var(--surface)" />
      <circle cx="15" cy="16" r="2.2" fill="var(--surface)" />
    </svg>
  );
}
function AssigneeIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="8" r="3.4" />
      <path d="M5 19c0-3.3 3-5.5 7-5.5s7 2.2 7 5.5" />
    </svg>
  );
}
function DueIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="5" width="18" height="16" rx="2" />
      <line x1="3" y1="9.5" x2="21" y2="9.5" />
      <line x1="8" y1="2.5" x2="8" y2="6" />
      <line x1="16" y1="2.5" x2="16" y2="6" />
    </svg>
  );
}
function PriorityIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <line x1="5" y1="22" x2="5" y2="3" />
      <path d="M5 4h11l-2 4 2 4H5" />
    </svg>
  );
}
function RepeatIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="17 2 21 6 17 10" />
      <path d="M3 12V10a4 4 0 0 1 4-4h14" />
      <polyline points="7 22 3 18 7 14" />
      <path d="M21 12v2a4 4 0 0 1-4 4H3" />
    </svg>
  );
}
// Same X glyph as TaskDetail's close button (components/TaskDetail.tsx
// ".td-icon-btn" close), sized down slightly (15 vs 17) for the composer's
// smaller header button.
function CloseIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round">
      <line x1="6" y1="6" x2="18" y2="18" />
      <line x1="18" y1="6" x2="6" y2="18" />
    </svg>
  );
}

export default function TaskComposer(props: TaskComposerProps) {
  const { onCreated } = props;
  const isPersonal = props.context === "personal";
  // Team-only fields, undefined/empty in personal context (never read there —
  // see the payload-building guards in fastCreate/createFromDraft/create).
  const teamId = props.context === "team" ? props.teamId : undefined;
  const members = props.context === "team" ? props.members : [];
  const defaultAssigneeId = props.context === "team" ? props.defaultAssigneeId : undefined;
  // Personal-only fields, undefined in team context.
  const viewHorizon = props.context === "personal" ? props.horizon : undefined;
  const viewPeriods = props.context === "personal" ? props.periods : undefined;
  const periodLabel = props.context === "personal" ? props.periodLabel : undefined;
  const barPlaceholder = props.context === "personal" ? props.placeholder : "Add a task for the team…";

  const [expanded, setExpanded] = useState(false);
  const [title, setTitle] = useState("");
  const [notes, setNotes] = useState("");
  const [assigneeId, setAssigneeId] = useState<string | undefined>(undefined);
  const [due, setDue] = useState("");
  const [priority, setPriority] = useState<Priority>("");
  const [repeat, setRepeat] = useState<RepeatDraft | null>(null);
  // Personal context only: an expand-time #override token, remembered for
  // the lifetime of the draft since the expanded card has "No horizon UI"
  // (docs/DESIGN_V31_COMPOSER.md) to re-surface or re-edit it — mirrors how
  // the other pills capture their expand-time parse into state once and
  // never re-parse the title afterward. EXCEPT across a tab switch: the
  // composer is a single instance shared by Day/Week/Month, so an override
  // captured under one tab's horizon is reset when the active tab's horizon
  // changes (see the effect below) — otherwise the collapsed badge shows
  // the new tab while Enter/createFromDraft would silently create into the
  // old tab's horizon.
  const [horizonOverride, setHorizonOverride] = useState<Horizon | undefined>(undefined);
  const [pop, setPop] = useState<PopoverName>(null);
  const [createMore, setCreateMore] = useState(false);
  const [creating, setCreating] = useState(false);
  const [justCreated, setJustCreated] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const titleRef = useRef<HTMLInputElement>(null);
  // Outside-mousedown collapse (usability fix, see the effect below) needs a
  // direct DOM handle on the expanded card to test "was this click outside
  // the card at all", not just "outside a pill/popover".
  const cardRef = useRef<HTMLDivElement>(null);
  const createdTimer = useRef<number | undefined>(undefined);
  // Synchronous double-submit guard (FIX: parity with legacy QuickAdd, which
  // cleared its text synchronously before awaiting onCreate). `creating`
  // state alone isn't enough: two calls in the same tick (e.g. Enter-key
  // auto-repeat) both read the stale pre-render value. A ref mutates
  // immediately, so the second call sees the guard.
  const inFlightRef = useRef(false);

  useEffect(() => () => window.clearTimeout(createdTimer.current), []);

  // Outside-mousedown handler for the expanded card (usability fix: the card
  // used to have no mouse way to close at all — no button, and this listener
  // only ever closed pill popovers, never the card itself). One handler,
  // gated on `expanded` (not `pop`) so it's live any time the card is open;
  // three cases per mousedown target:
  //   1. On a pill trigger, or inside the open popover panel itself — do
  //      nothing (their own onClick/row handlers already manage `pop`, so
  //      re-clicking the SAME pill still toggles it closed instead of
  //      flickering). Guarded first so it wins over the "inside card" case
  //      below.
  //   2. Inside the card but not on a pill/popover (title, notes, a pill
  //      row's own padding, the footer, ...) — ORIGINAL behavior, preserved
  //      as-is: closes an open popover only, the card stays open.
  //   3. Fully outside the card — closes an open popover first (so the
  //      first outside click never also collapses, same two-step feel as
  //      case 2); a SECOND outside click, with no popover left open,
  //      collapses the card with the draft preserved — identical semantics
  //      to the close button and the second Esc press (see collapse()).
  // The leading .composer-expand-btn/.composer-draft-chip guard is
  // defensive: those collapsed-bar elements never coexist with the expanded
  // card in the render tree (mutually exclusive on `expanded`), but this
  // keeps a mousedown on them from ever being able to re-toggle mid-render.
  useEffect(() => {
    if (!expanded) return;
    function onDocMouseDown(e: MouseEvent) {
      const target = e.target;
      if (!(target instanceof Element)) return;
      if (target.closest(".composer-expand-btn, .composer-draft-chip")) return;
      if (target.closest(".composer-pill, .composer-popover")) return;
      if (cardRef.current?.contains(target)) {
        if (pop) setPop(null);
        return;
      }
      if (pop) setPop(null);
      else collapse();
    }
    document.addEventListener("mousedown", onDocMouseDown);
    return () => document.removeEventListener("mousedown", onDocMouseDown);
  }, [expanded, pop]);

  useEffect(() => {
    if (expanded) titleRef.current?.focus();
  }, [expanded]);

  // FIX (MAJOR — stale horizonOverride across tab switches): personal
  // context is a single composer instance shared across Day/Week/Month;
  // reset any expand-time #override token whenever the active tab's horizon
  // prop changes, so a stale override from a previous tab can't silently
  // steer Enter/createFromDraft into the wrong horizon while the collapsed
  // badge already shows the new tab. Team context unaffected (viewHorizon
  // is always undefined there).
  useEffect(() => {
    if (isPersonal) setHorizonOverride(undefined);
  }, [viewHorizon]);

  function flashCreated() {
    setJustCreated(true);
    window.clearTimeout(createdTimer.current);
    createdTimer.current = window.setTimeout(() => setJustCreated(false), 2200);
  }

  function togPop(name: Exclude<PopoverName, null>) {
    setPop((p) => (p === name ? null : name));
  }

  // FIX (draft-loss footgun): number of non-title draft fields currently
  // set. Drives the collapsed-bar draft indicator chip and picks which
  // collapsed-Enter path onCollapsedKeyDown takes.
  function draftFieldCount(): number {
    let n = 0;
    if (assigneeId) n++;
    if (due) n++;
    if (priority) n++;
    if (repeat) n++;
    if (notes.trim()) n++;
    // Personal only: a bare #override with no other field set is still a
    // draft the collapsed fast path must not silently drop (same footgun
    // the other fields guard against) — routes collapsed Enter to
    // createFromDraft() instead of fastCreate() so horizonOverride is read.
    if (isPersonal && horizonOverride) n++;
    return n;
  }

  // Clears the entire draft — title, notes, every pill, and any open
  // popover. Shared by collapsed Esc (FIX: used to clear only the title,
  // orphaning pills that resurrected on next expand), create-with-draft,
  // and expanded create()'s create-more-off path.
  function clearDraft() {
    setTitle("");
    setNotes("");
    setAssigneeId(undefined);
    setDue("");
    setPriority("");
    setRepeat(null);
    setHorizonOverride(undefined);
    setPop(null);
  }

  // Runs the token parser exactly once per collapsed->expanded transition,
  // merging into whatever is already set: a freshly parsed token wins, then
  // the existing draft value, then the page's filtered-member default —
  // same direction as createFromDraft's merge (FIX: they used to disagree,
  // so Enter vs Shift+Enter on identical typed text produced opposite pill
  // values). (Personal context: no page filtered-member default, and
  // #override tokens merge into horizonOverride the same way.)
  function expand() {
    const parsed = parseQuickAdd(title);
    setTitle(parsed.title);
    setAssigneeId((a) => resolveAssigneeName(parsed.assigneeName, members) ?? a ?? defaultAssigneeId);
    setPriority((p) => parsed.priority || p);
    setDue((d) => parsed.dueDate || d);
    setRepeat((r) =>
      parsed.recurrence
        ? { freq: parsed.recurrence.freq, interval: 1, weekdays: parsed.recurrence.weekdays ?? DEFAULT_WEEKDAYS }
        : r,
    );
    if (isPersonal) setHorizonOverride((h) => parsed.horizon ?? h);
    setPop(null);
    setExpanded(true);
  }

  // Collapse the expanded card WITHOUT touching the draft — the single
  // shared semantics behind every mouse/keyboard way to close the card: the
  // close button, an outside click (see the effect above), and the second
  // Esc press in onCardKeyDown below (docs/DESIGN_V31_COMPOSER.md "Esc
  // collapses with draft preserved" — these are additive mouse affordances
  // with IDENTICAL semantics, not a new behavior). Re-expanding (expand(),
  // above) re-runs the same merge-only parse over the untouched draft, so
  // nothing set here is ever lost.
  function collapse() {
    setPop(null);
    setExpanded(false);
  }

  // Collapsed fast path — pure token parse + create, the same behavior as
  // the legacy QuickAdd this replaces; deliberately ignores any pending
  // pill draft. Only reached when the draft has no non-title fields set —
  // onCollapsedKeyDown routes a non-empty draft to createFromDraft()
  // instead (FIX: draft-loss footgun).
  function fastCreate() {
    if (inFlightRef.current) return;
    const parsed = parseQuickAdd(title);
    if (!parsed.title) return;
    // Team: ignore parsed.horizon (a bare #token) entirely — the team page
    // is always-daily (DESIGN_V3_TEAMS deviation #3), so horizon here comes
    // only from recurrence coupling, else defaults to daily+today.
    // Personal (v3.2): #override tokens ARE honored — recurrence coupling
    // still wins over them, same precedence as team.
    const { horizon, period } = horizonAndPeriod(
      parsed.recurrence?.freq,
      isPersonal ? { horizonToken: parsed.horizon, viewHorizon: viewHorizon!, periods: viewPeriods! } : undefined,
    );
    const assignee = isPersonal ? undefined : (resolveAssigneeName(parsed.assigneeName, members) ?? defaultAssigneeId);
    inFlightRef.current = true;
    setCreating(true);
    setError(null);
    api
      .createTask({
        title: parsed.title,
        horizon,
        period,
        dueDate: parsed.dueDate,
        priority: parsed.priority,
        teamId: isPersonal ? undefined : teamId,
        assigneeId: assignee,
        recurrence: parsed.recurrence ? { freq: parsed.recurrence.freq, weekdays: parsed.recurrence.weekdays } : undefined,
      })
      .then(() => {
        notifyTasksChanged();
        onCreated();
        setTitle("");
        flashCreated();
      })
      .catch((e) => setError(errMsg(e)))
      .finally(() => {
        inFlightRef.current = false;
        setCreating(false);
      });
  }

  // Draft-aware collapsed Enter (FIX: draft-loss footgun) — reached only
  // when the preserved draft has at least one non-title field set. Builds
  // the SAME payload shape as expanded create() (title/notes/pills)
  // instead of the title-only fast path, merging in freshly-typed title
  // tokens which OVERRIDE the corresponding draft field (@Name beats a
  // draft assignee, etc. — the token is newer user input than the draft).
  function createFromDraft() {
    if (inFlightRef.current) return;
    const parsed = parseQuickAdd(title);
    if (!parsed.title) return;
    const mergedAssignee = isPersonal ? undefined : (resolveAssigneeName(parsed.assigneeName, members) ?? assigneeId);
    const mergedPriority = parsed.priority || priority;
    const mergedDue = parsed.dueDate || due;
    const mergedRepeat: RepeatDraft | null = parsed.recurrence
      ? { freq: parsed.recurrence.freq, interval: 1, weekdays: parsed.recurrence.weekdays ?? DEFAULT_WEEKDAYS }
      : repeat;
    // Personal: a freshly-typed #token overrides the stored expand-time
    // horizonOverride, same "fresh token beats draft" rule as the other
    // merged fields above.
    const mergedHorizonToken = isPersonal ? (parsed.horizon ?? horizonOverride) : undefined;
    const { horizon, period } = horizonAndPeriod(
      mergedRepeat?.freq,
      isPersonal ? { horizonToken: mergedHorizonToken, viewHorizon: viewHorizon!, periods: viewPeriods! } : undefined,
    );
    inFlightRef.current = true;
    setCreating(true);
    setError(null);
    api
      .createTask({
        title: parsed.title,
        notes: notes.trim() || undefined,
        horizon,
        period,
        dueDate: mergedDue || undefined,
        priority: mergedPriority || undefined,
        teamId: isPersonal ? undefined : teamId,
        assigneeId: mergedAssignee,
        recurrence: buildRecurrence(mergedRepeat),
      })
      .then(() => {
        notifyTasksChanged();
        onCreated();
        clearDraft();
        flashCreated();
      })
      .catch((e) => setError(errMsg(e)))
      .finally(() => {
        inFlightRef.current = false;
        setCreating(false);
      });
  }

  // Expanded create — builds the task purely from pill state; the title is
  // taken literally (no token re-parsing while expanded).
  async function create() {
    if (!title.trim() || inFlightRef.current) return;
    inFlightRef.current = true;
    setCreating(true);
    setError(null);
    const { horizon, period } = horizonAndPeriod(
      repeat?.freq,
      isPersonal ? { horizonToken: horizonOverride, viewHorizon: viewHorizon!, periods: viewPeriods! } : undefined,
    );
    try {
      await api.createTask({
        title: title.trim(),
        notes: notes.trim() || undefined,
        horizon,
        period,
        dueDate: due || undefined,
        priority: priority || undefined,
        teamId: isPersonal ? undefined : teamId,
        assigneeId: isPersonal ? undefined : assigneeId,
        recurrence: buildRecurrence(repeat),
      });
      notifyTasksChanged();
      onCreated();
      flashCreated();
      if (createMore) {
        setTitle("");
        setNotes("");
        setPop(null);
        titleRef.current?.focus();
      } else {
        setExpanded(false);
        clearDraft();
      }
    } catch (e) {
      setError(errMsg(e));
    } finally {
      inFlightRef.current = false;
      setCreating(false);
    }
  }

  function onCollapsedKeyDown(e: ReactKeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      // FIX (draft-loss footgun): a preserved draft with any non-title
      // field set must be created WITH that draft, not silently dropped by
      // the title-token-only fast path.
      if (draftFieldCount() > 0) createFromDraft();
      else fastCreate();
    } else if (e.key === "Enter" && e.shiftKey) {
      e.preventDefault();
      expand();
    } else if (e.key === "Escape") {
      // FIX: collapsed Esc clears the whole draft, not just the title —
      // otherwise cleared pills silently resurrect on next expand.
      clearDraft();
    }
  }

  // Card-wide handler (docs/DESIGN_V31_COMPOSER.md): Cmd/Ctrl+Enter creates
  // from anywhere in the card; Escape closes an open popover first, then
  // collapses (draft preserved); plain Enter creates only from the title
  // field, and only when no popover is open.
  function onCardKeyDown(e: ReactKeyboardEvent<HTMLDivElement>) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      create();
      return;
    }
    if (e.key === "Escape") {
      if (pop) setPop(null);
      else collapse();
      return;
    }
    if (e.key === "Enter" && !e.shiftKey && !pop && e.target === titleRef.current) {
      e.preventDefault();
      create();
    }
  }

  const assignee = assigneeId ? members.find((m) => m.id === assigneeId) : undefined;
  const assigneeTone = assignee ? AVATAR_TONES[toneIndex(assignee.id)] : undefined;
  const priorityMeta = PRIORITY_OPTIONS.find((p) => p.value === priority);
  // FIX (draft-loss footgun): non-title fields preserved from an
  // Esc-collapse — drives the collapsed-bar draft indicator chip below.
  const fieldCount = draftFieldCount();

  if (!expanded) {
    return (
      <div className={`task-composer${isPersonal ? " personal" : ""}`}>
        <div className="composer-bar">
          {isPersonal && (
            // Repeat pill note (docs/DESIGN_V31_COMPOSER.md "v3.2"): the
            // collapsed badge shows the view's horizon+period exactly as the
            // old QuickAdd did (e.g. "Daily · Today", "Weekly · W28") —
            // driven live by props, so it follows the active tab/period
            // without the composer re-parsing typed text.
            <>
              <span className="composer-horizon-badge">
                <span className="composer-horizon-dot" aria-hidden="true" />
                {HORIZON_LABEL[viewHorizon!]}
              </span>
              <span className="composer-horizon-period">{periodLabel}</span>
            </>
          )}
          <PlusIcon />
          <input
            ref={titleRef}
            className="composer-bar-input"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={onCollapsedKeyDown}
            placeholder={barPlaceholder}
          />
          {justCreated && (
            <span className="composer-created">
              <CheckIcon />
              Task created
            </span>
          )}
          {!justCreated && fieldCount > 0 && (
            <button
              type="button"
              className="composer-draft-chip"
              onClick={expand}
              title="Draft has more fields set — click to expand"
            >
              {fieldCount} field{fieldCount === 1 ? "" : "s"} set
            </button>
          )}
          <kbd className="composer-kbd">⏎</kbd>
          <button type="button" className="composer-expand-btn" title="More options (⇧⏎)" onClick={expand}>
            <ExpandIcon />
          </button>
        </div>
        {error && (
          <div className="composer-error-row">
            <span className="error">{error}</span>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className={`task-composer expanded${isPersonal ? " personal" : ""}`}>
      <div className="composer-card" ref={cardRef} onKeyDown={onCardKeyDown}>
        <div className="composer-top">
          <div className="composer-top-row">
            <input
              ref={titleRef}
              className="composer-title-input"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Task title"
            />
            {/* Usability fix: the only prior way to close the expanded card
                was Esc while focus was inside it — no button, and
                click-outside only ever closed pill popovers. Click collapses
                with the draft preserved, identical semantics to Esc/outside-
                click (see collapse() above). */}
            <button
              type="button"
              className="composer-close-btn"
              title="Collapse (Esc)"
              aria-label="Collapse composer"
              onClick={collapse}
            >
              <CloseIcon />
            </button>
          </div>
          <textarea
            className="composer-notes-input"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="Add details…"
            rows={2}
          />
        </div>

        <div className="composer-pills">
          {/* Assignee — personal context hides this pill entirely (no
              teamId/assigneeId ever sent from that context). */}
          {!isPersonal && (
          <div className="composer-pill-wrap">
            <button
              type="button"
              className={`composer-pill${assignee ? " set" : ""}${pop === "assignee" ? " active" : ""}`}
              onClick={() => togPop("assignee")}
            >
              {assignee ? (
                <span className="composer-pill-avatar" style={{ background: assigneeTone?.bg, color: assigneeTone?.fg }}>
                  {initials(assignee.name)}
                </span>
              ) : (
                <AssigneeIcon />
              )}
              <span>{assignee ? assignee.name : "Assignee"}</span>
            </button>
            {pop === "assignee" && (
              <div className="composer-popover composer-popover-assignee">
                {members.map((m) => {
                  const tone = AVATAR_TONES[toneIndex(m.id)];
                  const active = assigneeId === m.id;
                  return (
                    <button
                      key={m.id}
                      type="button"
                      className={`composer-popover-row${active ? " active" : ""}`}
                      onClick={() => {
                        setAssigneeId(m.id);
                        setPop(null);
                      }}
                    >
                      <span className="composer-popover-avatar" style={{ background: tone.bg, color: tone.fg }}>
                        {initials(m.name)}
                      </span>
                      <span className="composer-popover-label">{m.name}</span>
                      {active && <span className="composer-popover-check">✓</span>}
                    </button>
                  );
                })}
                <button
                  type="button"
                  className={`composer-popover-row${!assigneeId ? " active" : ""}`}
                  onClick={() => {
                    setAssigneeId(undefined);
                    setPop(null);
                  }}
                >
                  <span className="composer-popover-avatar composer-popover-avatar-unassigned">—</span>
                  <span className="composer-popover-label">Unassigned</span>
                  {!assigneeId && <span className="composer-popover-check">✓</span>}
                </button>
              </div>
            )}
          </div>
          )}

          {/* Due date */}
          <div className="composer-pill-wrap">
            <button
              type="button"
              className={`composer-pill${due ? " set" : ""}${pop === "due" ? " active" : ""}`}
              onClick={() => togPop("due")}
            >
              <DueIcon />
              <span className={due ? "composer-pill-mono" : undefined}>{due ? formatShortDayLabel(due) : "Due date"}</span>
            </button>
            {pop === "due" && (
              <div className="composer-popover composer-popover-due">
                <input
                  type="date"
                  className="composer-date-input"
                  value={due}
                  onChange={(e) => {
                    setDue(e.target.value);
                    setPop(null);
                  }}
                />
                <div className="composer-due-actions">
                  <button
                    type="button"
                    className="composer-due-today"
                    onClick={() => {
                      setDue(today());
                      setPop(null);
                    }}
                  >
                    Today
                  </button>
                  <button
                    type="button"
                    className="composer-due-clear"
                    onClick={() => {
                      setDue("");
                      setPop(null);
                    }}
                  >
                    Clear
                  </button>
                </div>
              </div>
            )}
          </div>

          {/* Priority */}
          <div className="composer-pill-wrap">
            <button
              type="button"
              className={`composer-pill${priority ? " set" : ""}${pop === "priority" ? " active" : ""}`}
              onClick={() => togPop("priority")}
            >
              {priority ? <span className="composer-pill-dot" style={{ background: priorityMeta?.dot }} /> : <PriorityIcon />}
              <span>{priority ? priorityMeta?.label : "Priority"}</span>
            </button>
            {pop === "priority" && (
              <div className="composer-popover composer-popover-priority">
                {PRIORITY_OPTIONS.map((opt) => {
                  const active = priority === opt.value;
                  return (
                    <button
                      key={opt.value || "none"}
                      type="button"
                      className={`composer-popover-row${active ? " active" : ""}`}
                      onClick={() => {
                        setPriority(opt.value);
                        setPop(null);
                      }}
                    >
                      <span
                        className={`composer-popover-dot${opt.value ? "" : " none"}`}
                        style={opt.dot ? { background: opt.dot } : undefined}
                      />
                      <span className="composer-popover-label">{opt.label}</span>
                      {active && <span className="composer-popover-check">✓</span>}
                    </button>
                  );
                })}
              </div>
            )}
          </div>

          {/* Repeat */}
          <div className="composer-pill-wrap">
            <button
              type="button"
              className={`composer-pill${repeat ? " set" : ""}${pop === "repeat" ? " active" : ""}`}
              onClick={() => togPop("repeat")}
            >
              <RepeatIcon />
              <span>{repeatLabel(repeat)}</span>
            </button>
            {pop === "repeat" && (
              <div className="composer-popover composer-popover-repeat">
                <div className="composer-preset-row">
                  {PRESETS.map((p) => (
                    <button
                      key={p.value}
                      type="button"
                      className={`composer-preset-btn${repeat?.freq === p.value ? " active" : ""}`}
                      onClick={() =>
                        setRepeat((r) => ({
                          freq: p.value,
                          interval: r?.interval ?? 1,
                          weekdays: p.value === "weekdays" ? (r?.weekdays?.length ? r.weekdays : DEFAULT_WEEKDAYS) : (r?.weekdays ?? DEFAULT_WEEKDAYS),
                        }))
                      }
                    >
                      {p.label}
                    </button>
                  ))}
                </div>
                {repeat && repeat.freq !== "weekdays" && (
                  <div className="composer-interval-row">
                    <span>Every</span>
                    <input
                      type="number"
                      min={1}
                      className="composer-interval-input"
                      value={repeat.interval}
                      onChange={(e) =>
                        setRepeat((r) => (r ? { ...r, interval: Math.max(1, parseInt(e.target.value, 10) || 1) } : r))
                      }
                    />
                    <span>{INTERVAL_UNIT[repeat.freq]}</span>
                  </div>
                )}
                {repeat && repeat.freq === "weekdays" && (
                  <div className="composer-weekday-row">
                    {WEEKDAY_LABELS.map((label, i) => {
                      const day = i + 1;
                      const active = repeat.weekdays.includes(day);
                      return (
                        <button
                          key={day}
                          type="button"
                          title={label}
                          className={`composer-weekday-chip${active ? " active" : ""}`}
                          onClick={() =>
                            setRepeat((r) => {
                              if (!r) return r;
                              const set = new Set(r.weekdays);
                              if (set.has(day)) {
                                // Never leave weekdays empty — the server 400s on it.
                                if (set.size === 1) return r;
                                set.delete(day);
                              } else {
                                set.add(day);
                              }
                              return { ...r, weekdays: Array.from(set).sort((a, b) => a - b) };
                            })
                          }
                        >
                          {label[0]}
                        </button>
                      );
                    })}
                  </div>
                )}
                <div className="composer-repeat-footer">
                  <button
                    type="button"
                    className="composer-repeat-clear"
                    onClick={() => {
                      setRepeat(null);
                      setPop(null);
                    }}
                  >
                    Don't repeat
                  </button>
                  <button type="button" className="composer-repeat-done" onClick={() => setPop(null)}>
                    Done
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>

        {error && (
          <div className="composer-error-row">
            <span className="error">{error}</span>
          </div>
        )}

        <div className="composer-footer">
          <button
            type="button"
            className={`composer-createmore-btn${createMore ? " on" : ""}`}
            onClick={() => setCreateMore((v) => !v)}
          >
            <span className={`composer-switch${createMore ? " on" : ""}`}>
              <span className="composer-switch-knob" />
            </span>
            <span className="composer-createmore-label">Create more</span>
          </button>
          <div className="composer-footer-right">
            {justCreated && (
              <span className="composer-created">
                <CheckIcon />
                Task created
              </span>
            )}
            <kbd className="composer-kbd composer-kbd-lg">⌘⏎</kbd>
            <button type="button" className="composer-create-btn" disabled={!title.trim() || creating} onClick={create}>
              Create task
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
