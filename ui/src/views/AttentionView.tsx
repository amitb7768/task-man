// "Attention" — design handoff "06 Attention". Two sections (Overdue /
// Slipped) of a bespoke 56px selectable row (NOT the shared TaskRow — the
// anatomy differs: checkbox + priority tick + title/reason + chip, no
// status control or hover actions). Multi-select drives a floating bulk bar
// that reschedules/completes/deletes immediately (no confirm()), each
// followed by an undo toast per docs/DESIGN_V2_UI.md "Undo wiring".
import { useEffect, useMemo, useState } from "react";
import type { AttentionViewResponse, Horizon, Priority, Status, TaskView, UpdateTaskInput } from "../api";
import { api, ApiError } from "../api";
import {
  currentMonth,
  currentWeek,
  formatCompactDate,
  formatMonthShort,
  formatWeekShort,
  isoWeekMonday,
  parseDate,
  toISOWeek,
  today,
} from "../period";
import { notifyTasksChanged } from "../App";
import { useAuth } from "../auth/AuthContext";
import TaskDetail from "../components/TaskDetail";
import { dismissToast, showToast } from "../components/Toast";
import "../styles/attention.css";

const PRIORITY_COLOR: Record<Priority, string> = {
  high: "var(--overdue)",
  medium: "var(--warn)",
  low: "var(--accent)",
  "": "transparent",
};

function daysBetween(earlier: string, later: string): number {
  return Math.round((parseDate(later).getTime() - parseDate(earlier).getTime()) / 86400000);
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

// Recurring instances are never eligible for backlog parking — they'd
// collide on the unique {seriesId, period:""} index and the series
// respawns them anyway (docs/DESIGN_V8_ATTENTION_REASSIGN.md). Same test
// v6 rollover's Move uses (views/TeamRollover.tsx isRecurring).
function isRecurring(t: TaskView): boolean {
  return !!(t.recurrence || t.seriesId);
}

// "Schedule for D" generalizes reschedule-to-today: dated tasks get D as
// their dueDate; undated tasks get the period containing D for their
// horizon, staying undated (docs/DESIGN_V8_ATTENTION_REASSIGN.md).
function periodOf(dateStr: string, horizon: Horizon): string {
  if (horizon === "daily") return dateStr;
  if (horizon === "weekly") return toISOWeek(dateStr);
  if (horizon === "monthly") return dateStr.slice(0, 7);
  return dateStr; // backlog tasks never surface in Attention
}

// Overdue = has a dueDate in the past. Reason/chip both derive from the same
// day count so they never disagree with each other.
function overdueCopy(t: TaskView): { reason: string; chip: string } {
  const days = daysBetween(t.dueDate!, today());
  return {
    reason: `Due ${formatCompactDate(t.dueDate!)} · ${days === 1 ? "1 day ago" : `${days} days ago`}`,
    chip: days === 1 ? "1 day overdue" : `${days} days overdue`,
  };
}

// Slipped = no dueDate, but the task's own horizon period has already
// passed without completion. Anchor label + "ago" distance both read off
// that period, scaled to the task's horizon (days / weeks / months).
function slippedCopy(t: TaskView): { reason: string; chip: string } {
  if (t.horizon === "daily") {
    const days = daysBetween(t.period, today());
    const chip = days === 1 ? "yesterday" : plural(days, "day") + " ago";
    return { reason: `Anchored ${formatCompactDate(t.period)} · slipped`, chip };
  }
  if (t.horizon === "weekly") {
    const weeksAgo = Math.round(
      (isoWeekMonday(currentWeek()).getTime() - isoWeekMonday(t.period).getTime()) / (7 * 86400000),
    );
    const chip = weeksAgo === 1 ? "last week" : plural(weeksAgo, "week") + " ago";
    return { reason: `Anchored ${formatWeekShort(t.period)} · slipped`, chip };
  }
  const [cy, cm] = currentMonth().split("-").map(Number);
  const [py, pm] = t.period.split("-").map(Number);
  const monthsAgo = cy * 12 + cm - (py * 12 + pm);
  const chip = monthsAgo === 1 ? "last month" : plural(monthsAgo, "month") + " ago";
  return { reason: `Anchored ${formatMonthShort(t.period)} · slipped`, chip };
}

function CheckGlyph() {
  return (
    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 12 10 18 20 6" />
    </svg>
  );
}

type SectionKey = "overdue" | "slipped";

function AttentionRow({
  task,
  section,
  selected,
  onToggle,
  onOpen,
}: {
  task: TaskView;
  section: SectionKey;
  selected: boolean;
  onToggle: (id: string) => void;
  onOpen: (id: string) => void;
}) {
  const { reason, chip } = section === "overdue" ? overdueCopy(task) : slippedCopy(task);
  return (
    <div className={`attn-row${selected ? " selected" : ""}`}>
      <button
        type="button"
        className={`attn-check${selected ? " checked" : ""}`}
        role="checkbox"
        aria-checked={selected}
        aria-label={selected ? "Deselect task" : "Select task"}
        onClick={() => onToggle(task.id)}
      >
        {selected && <CheckGlyph />}
      </button>
      <span className="attn-tick" style={{ background: PRIORITY_COLOR[task.priority] }} />
      <div
        className="attn-main"
        role="button"
        tabIndex={0}
        onClick={() => onOpen(task.id)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onOpen(task.id);
          }
        }}
      >
        <div className="attn-title">{task.title}</div>
        <div className="attn-reason">{reason}</div>
      </div>
      <span className={`attn-chip ${section}`}>{chip}</span>
    </div>
  );
}

function Section({
  title,
  dot,
  labelColor,
  hint,
  section,
  tasks,
  selected,
  onToggle,
  onToggleAll,
  onOpen,
}: {
  title: string;
  dot: string;
  labelColor: string;
  hint: string;
  section: SectionKey;
  tasks: TaskView[];
  selected: Set<string>;
  onToggle: (id: string) => void;
  onToggleAll: (ids: string[]) => void;
  onOpen: (id: string) => void;
}) {
  if (tasks.length === 0) return null;
  const ids = tasks.map((t) => t.id);
  const allSelected = ids.every((id) => selected.has(id));

  return (
    <div className="attn-section">
      <div className="attn-section-header">
        <button
          type="button"
          className={`attn-check-btn${allSelected ? " checked" : ""}`}
          onClick={() => onToggleAll(ids)}
          aria-label={allSelected ? `Deselect all in ${title}` : `Select all in ${title}`}
        >
          {allSelected && <CheckGlyph />}
        </button>
        <span className="attn-section-dot" style={{ background: dot }} />
        <span className="attn-section-label" style={{ color: labelColor }}>
          {title}
        </span>
        <span className="attn-section-count">{tasks.length}</span>
        <span className="attn-section-rule" />
        <span className="attn-section-hint">{hint}</span>
      </div>
      <div className="attn-rows">
        {tasks.map((t) => (
          <AttentionRow
            key={t.id}
            task={t}
            section={section}
            selected={selected.has(t.id)}
            onToggle={onToggle}
            onOpen={onOpen}
          />
        ))}
      </div>
    </div>
  );
}

export default function AttentionView() {
  const { user } = useAuth();
  const isAdmin = user.systemRole === "ADMIN";
  const [data, setData] = useState<AttentionViewResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [scheduleDate, setScheduleDate] = useState("");

  function reload() {
    setError(null);
    api
      .attentionView()
      .then((d) => {
        setData(d);
        setSelected((prev) => {
          const ids = new Set([...d.overdue, ...d.slipped].map((t) => t.id));
          return new Set([...prev].filter((id) => ids.has(id)));
        });
      })
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(reload, []);

  const taskById = useMemo(() => {
    const m = new Map<string, TaskView>();
    data?.overdue.forEach((t) => m.set(t.id, t));
    data?.slipped.forEach((t) => m.set(t.id, t));
    return m;
  }, [data]);

  const allIds = useMemo(() => (data ? [...data.overdue, ...data.slipped].map((t) => t.id) : []), [data]);
  const allSelected = allIds.length > 0 && allIds.every((id) => selected.has(id));

  function toggle(id: string) {
    // Starting a new selection (0 -> 1) dismisses a lingering undo toast so
    // the bulk bar takes over — docs/DESIGN_V2_UI.md "Undo wiring".
    if (selected.size === 0) dismissToast();
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleSectionAll(ids: string[]) {
    setSelected((prev) => {
      const allSel = ids.every((id) => prev.has(id));
      const next = new Set(prev);
      ids.forEach((id) => (allSel ? next.delete(id) : next.add(id)));
      return next;
    });
  }

  function toggleSelectAll() {
    setSelected(allSelected ? new Set() : new Set(allIds));
  }

  function fail(e: unknown) {
    setError(e instanceof ApiError ? e.message : String(e));
  }

  async function runReschedule() {
    if (busy || selected.size === 0) return;
    const ids = Array.from(selected);
    // Clearing dueDate over the wire = send "" (docs/DESIGN_V2_UI.md "Status
    // quo reminders") — a JSON null on the server's plain string field would
    // be a no-op in its merge-unmarshal PATCH.
    const remembered = ids.map((id) => {
      const t = taskById.get(id)!;
      return { id, period: t.period, dueDate: t.dueDate ?? "" };
    });
    setBusy(true);
    try {
      await api.reschedule(ids);
      setSelected(new Set());
      reload();
      notifyTasksChanged();
      showToast(`${plural(ids.length, "task")} rescheduled to today`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { period: r.period, dueDate: r.dueDate })))
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function runScheduleFor() {
    if (busy || selected.size === 0 || !scheduleDate) return;
    const ids = Array.from(selected);
    const targetDate = scheduleDate;
    // Safe .map + filter-null shape (same as runMarkDone's `remembered`) —
    // taskById.get(id) can miss (stale selection), and a bare `!` here ran
    // outside any try/catch, so a miss became an unhandled promise
    // rejection instead of a caught, reported error.
    const found = ids
      .map((id) => {
        const t = taskById.get(id);
        return t ? { id, task: t } : null;
      })
      .filter((r): r is { id: string; task: TaskView } => r !== null);
    // Same undo shape as runReschedule — {id, period, dueDate} — since this
    // is the same PATCH family, just with an arbitrary date instead of today.
    const remembered = found.map(({ id, task: t }) => ({ id, period: t.period, dueDate: t.dueDate ?? "" }));
    setBusy(true);
    try {
      await Promise.all(
        found.map(({ id, task: t }) =>
          t.dueDate
            ? api.updateTask(id, { dueDate: targetDate, period: periodOf(targetDate, t.horizon) })
            : api.updateTask(id, { period: periodOf(targetDate, t.horizon) }),
        ),
      );
      setSelected(new Set());
      setScheduleDate("");
      reload();
      notifyTasksChanged();
      showToast(`${plural(found.length, "task")} scheduled for ${formatCompactDate(targetDate)}`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { period: r.period, dueDate: r.dueDate })))
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function runMarkDone() {
    if (busy || selected.size === 0) return;
    const ids = Array.from(selected);
    const remembered = ids
      .map((id) => {
        const t = taskById.get(id);
        return t ? { id, status: t.status as Status } : null;
      })
      .filter((r): r is { id: string; status: Status } => r !== null);
    setBusy(true);
    try {
      await Promise.all(remembered.map((r) => api.updateTask(r.id, { status: "done" })));
      setSelected(new Set());
      reload();
      notifyTasksChanged();
      showToast(`${plural(remembered.length, "task")} marked done`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { status: r.status })))
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function runDelete() {
    if (busy || selected.size === 0) return;
    const ids = Array.from(selected);
    setBusy(true);
    try {
      const results = await Promise.all(ids.map((id) => api.deleteTask(id)));
      const deleted = results.flatMap((r) => r.deleted);
      setSelected(new Set());
      reload();
      notifyTasksChanged();
      showToast(`${plural(ids.length, "task")} deleted`, () => {
        api
          .restoreTasks(deleted)
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  // Eligibility (client-side, docs/DESIGN_V8_ATTENTION_REASSIGN.md): NOT
  // recurring AND (personal OR actor is ADMIN) AND no subtasks — a parent
  // with subtasks 400s server-side ("backlog is coarser than child's
  // horizon"); progress.total is the direct-child count (server/CLAUDE.md).
  // Ineligible tasks are skipped and counted in the toast (v6 rollover-Move
  // precedent).
  async function runMoveToBacklog() {
    if (busy || selected.size === 0) return;
    const ids = Array.from(selected);
    const found = ids.map((id) => taskById.get(id)).filter((t): t is TaskView => !!t);
    const eligible = found.filter((t) => !isRecurring(t) && (!t.teamId || isAdmin) && t.progress.total === 0);
    const skipped = found.length - eligible.length;
    // Snapshot for undo: {id, horizon, period, dueDate, teamId, assigneeId,
    // weekOf} — but teamId/assigneeId/weekOf are only ever present on tasks
    // that HAD a teamId; a USER's restore PATCH must never carry a bare
    // weekOf key (403 per v6 rule), and personal tasks never had one. weekOf
    // itself is omitted (not sent as "") when falsy: an explicit "" would
    // fail parseISOWeek server-side and 400 the whole undo Promise.all batch
    // (fail-fast) if a team task's weekOf were ever absent.
    const remembered = eligible.map((t) => {
      const base = { id: t.id, horizon: t.horizon, period: t.period, dueDate: t.dueDate ?? "" };
      return t.teamId
        ? { ...base, teamId: t.teamId, assigneeId: t.assigneeId ?? null, ...(t.weekOf ? { weekOf: t.weekOf } : {}) }
        : base;
    });
    setBusy(true);
    try {
      await Promise.all(
        eligible.map((t) =>
          t.teamId
            ? api.updateTask(t.id, { teamId: null, assigneeId: null, horizon: "backlog", period: "", dueDate: "" })
            : api.updateTask(t.id, { horizon: "backlog", period: "", dueDate: "" }),
        ),
      );
      setSelected(new Set());
      reload();
      notifyTasksChanged();
      showToast(
        `Moved ${plural(eligible.length, "task")} to backlog${skipped ? ` · ${plural(skipped, "task")} skipped` : ""}`,
        () => {
          Promise.all(
            remembered.map((r) => {
              // Conditional restore PATCH: teamId/assigneeId/weekOf keys
              // only for tasks that had a teamId — combined into ONE PATCH
              // per task (never two calls).
              const patch: UpdateTaskInput = { horizon: r.horizon, period: r.period, dueDate: r.dueDate };
              if ("teamId" in r) {
                patch.teamId = r.teamId;
                patch.assigneeId = r.assigneeId;
                if ("weekOf" in r) patch.weekOf = r.weekOf;
              }
              return api.updateTask(r.id, patch);
            }),
          )
            .then(() => {
              reload();
              notifyTasksChanged();
            })
            .catch(fail);
        },
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  const hasItems = !!data && data.overdue.length + data.slipped.length > 0;
  const selCount = selected.size;

  return (
    <div className="attention-view">
      {error && <div className="error">{error}</div>}

      {hasItems && (
        <div className="attention-toolbar">
          <button type="button" className="attn-select-all" onClick={toggleSelectAll}>
            {allSelected ? "Clear selection" : "Select all"}
          </button>
        </div>
      )}

      {hasItems && data && (
        <div className="attention-sections">
          <Section
            title="Overdue"
            dot="var(--overdue)"
            labelColor="var(--overdue)"
            hint="Has a due date in the past"
            section="overdue"
            tasks={data.overdue}
            selected={selected}
            onToggle={toggle}
            onToggleAll={toggleSectionAll}
            onOpen={setSelectedId}
          />
          <Section
            title="Slipped"
            dot="var(--warn)"
            labelColor="var(--text-muted)"
            hint="Period passed, never completed"
            section="slipped"
            tasks={data.slipped}
            selected={selected}
            onToggle={toggle}
            onToggleAll={toggleSectionAll}
            onOpen={setSelectedId}
          />
        </div>
      )}

      {data && !hasItems && (
        <div className="attention-empty">
          <div className="attention-empty-icon">
            <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 12 10 18 20 6" />
            </svg>
          </div>
          <div className="attention-empty-title">Nothing needs attention</div>
          <div className="attention-empty-sub">No overdue or slipped tasks. Everything is anchored to a live period.</div>
        </div>
      )}

      {/* Hidden while the detail slide-over is open — the bulk bar's z-index
          (90) floats above TaskDetail (40/41), which would otherwise let it
          intercept clicks over the open panel. */}
      <div
        className={`attn-bulkbar${selCount > 0 && !selectedId ? " visible" : ""}`}
        aria-hidden={selCount === 0 || !!selectedId}
      >
        <span className="attn-bulk-count">{selCount} selected</span>
        <button type="button" className="attn-bulk-clear" title="Clear selection" onClick={() => setSelected(new Set())}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="6" y1="6" x2="18" y2="18" />
            <line x1="18" y1="6" x2="6" y2="18" />
          </svg>
        </button>
        <div className="attn-bulk-divider" />
        <div className="attn-bulk-schedule">
          <input
            type="date"
            className="attn-schedule-input"
            min={today()}
            value={scheduleDate}
            aria-label="Schedule for date"
            onChange={(e) => setScheduleDate(e.target.value)}
          />
          <button
            type="button"
            className="attn-bulk-schedule-apply"
            disabled={busy || !scheduleDate}
            onClick={runScheduleFor}
          >
            Apply
          </button>
        </div>
        <button type="button" className="attn-bulk-reschedule" disabled={busy} onClick={runReschedule}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="5" width="18" height="16" rx="2" />
            <line x1="3" y1="9.5" x2="21" y2="9.5" />
            <polyline points="9 15 12 12 12 18" />
            <line x1="12" y1="12" x2="15" y2="12" />
          </svg>
          Reschedule to today
        </button>
        <button type="button" className="attn-bulk-done" disabled={busy} onClick={runMarkDone}>
          Mark done
        </button>
        <button type="button" className="attn-bulk-backlog" disabled={busy} onClick={runMoveToBacklog}>
          Move to backlog
        </button>
        {isAdmin && (
          <button type="button" className="attn-bulk-delete" title="Delete" disabled={busy} onClick={runDelete}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 7 20 7" />
              <path d="M9 7V5h6v2" />
              <path d="M6 7l1 13h10l1-13" />
            </svg>
          </button>
        )}
      </div>

      {selectedId && <TaskDetail id={selectedId} onClose={() => setSelectedId(null)} onChanged={reload} />}
    </div>
  );
}
