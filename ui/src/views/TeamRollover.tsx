// Rollover — ADMIN-only triage queue for open, undated, past-week team tasks
// (docs/DESIGN_V6_WEEK_ROLLOVER.md "Rollover"; design handoff "13 Rollover").
// Entered from the team board's rollover banner ("Review →"). Owns its own
// full-screen header (back-to-board + serif title + mono meta) and body —
// stale tasks grouped by weekOf oldest-first (server-sorted; a plain
// contiguous-run scan keeps grouping cheap), leading checkboxes with
// select-all per week group AND globally, recurring tasks (`recurrence` or
// `seriesId`) pilled and skipped by Move, a bulk bar (Move to this week /
// Mark done / Cancel) with a hint when the selection has any recurring
// tasks. Every bulk action is client-side Promise.all + snapshot-undo toast
// (the existing TeamPage/Attention convention) — reload() + notifyTasksChanged()
// after every action and after undo.
import { useEffect, useMemo, useState } from "react";
import type { Member, Status, TaskView } from "../api";
import { api, ApiError } from "../api";
import { currentWeek, formatWeekRangeUpper } from "../period";
import { notifyTasksChanged } from "../App";
import { dismissToast, showToast } from "../components/Toast";
import StatusControl from "../components/StatusControl";
import "../styles/team-rollover.css";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}
function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}
// Deterministic per-member avatar tone — duplicated per-file convention
// (ui/CLAUDE.md), identical copy to TeamPage.tsx/TeamHistory.tsx/TaskRow.tsx.
function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}
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
const UNASSIGNED_TONE = { bg: "var(--surface-2)", fg: "var(--text-faint)" };
const PRIORITY_COLOR: Record<string, string> = {
  high: "var(--overdue)",
  medium: "var(--warn)",
  low: "var(--accent)",
  "": "transparent",
};

// Their period is recurrence-owned; moving desyncs the anchor, so Move skips
// them (docs/DESIGN_V6_WEEK_ROLLOVER.md "Rollover").
function isRecurring(t: TaskView): boolean {
  return !!(t.recurrence || t.seriesId);
}

function CheckGlyph() {
  return (
    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 12 10 18 20 6" />
    </svg>
  );
}

export default function TeamRollover({
  teamId,
  teamName,
  memberById,
  onBack,
}: {
  teamId: string;
  teamName: string;
  memberById: Map<string, Member>;
  onBack: () => void;
}) {
  const [tasks, setTasks] = useState<TaskView[] | null>(null);
  const [serverWeek, setServerWeek] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);

  function reload() {
    setError(null);
    api
      .teamRollover(teamId)
      .then((d) => {
        setTasks(d.tasks);
        setServerWeek(d.week);
        setSelected((prev) => new Set([...prev].filter((id) => d.tasks.some((t) => t.id === id))));
      })
      .catch((e) => setError(errMsg(e)));
  }

  useEffect(() => {
    reload();
    return () => dismissToast();
    // reload() closes over the latest teamId via the component's own render
    // closure — depending only on teamId mirrors TeamPage's own effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [teamId]);

  function avatarFor(assigneeId?: string): { initials: string; bg: string; fg: string; title: string } {
    if (assigneeId) {
      const m = memberById.get(assigneeId);
      if (m) return { initials: initials(m.name), ...AVATAR_TONES[toneIndex(m.id)], title: m.name };
    }
    return { initials: "—", ...UNASSIGNED_TONE, title: "Unassigned" };
  }

  const taskById = useMemo(() => {
    const m = new Map<string, TaskView>();
    tasks?.forEach((t) => m.set(t.id, t));
    return m;
  }, [tasks]);

  // Server sorts weekOf asc then createdAt asc, so same-weekOf rows are
  // always contiguous — a plain scan groups them without re-sorting.
  const groups = useMemo(() => {
    const out: { week: string; range: string; rows: TaskView[] }[] = [];
    (tasks ?? []).forEach((t) => {
      const w = t.weekOf ?? "";
      const prev = out[out.length - 1];
      if (!prev || prev.week !== w) {
        out.push({ week: w, range: formatWeekRangeUpper(w), rows: [] });
      }
      out[out.length - 1].rows.push(t);
    });
    return out;
  }, [tasks]);

  function toggle(id: string) {
    // Starting a new selection (0 -> 1) dismisses a lingering undo toast so
    // the bulk bar takes over (existing Attention/TeamPage convention).
    if (selected.size === 0) dismissToast();
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  function toggleGroup(ids: string[]) {
    setSelected((prev) => {
      const allSel = ids.every((id) => prev.has(id));
      const next = new Set(prev);
      ids.forEach((id) => (allSel ? next.delete(id) : next.add(id)));
      return next;
    });
  }
  const allIds = useMemo(() => (tasks ?? []).map((t) => t.id), [tasks]);
  const allSelected = allIds.length > 0 && allIds.every((id) => selected.has(id));
  function toggleAll() {
    setSelected(allSelected ? new Set() : new Set(allIds));
  }

  function fail(e: unknown) {
    setError(errMsg(e));
  }

  async function setRowStatus(id: string, status: Status) {
    try {
      await api.updateTask(id, { status });
      reload();
      notifyTasksChanged();
    } catch (e) {
      fail(e);
    }
  }

  async function bulkMove() {
    if (busy) return;
    const ids = Array.from(selected);
    if (!ids.length) return;
    const found = ids.map((id) => taskById.get(id)).filter((t): t is TaskView => !!t);
    const moveTargets = found.filter((t) => !isRecurring(t));
    const skipped = found.length - moveTargets.length;
    const remembered = moveTargets.map((t) => ({ id: t.id, weekOf: t.weekOf ?? "" }));
    const target = serverWeek ?? currentWeek();
    setBusy(true);
    setSelected(new Set());
    try {
      await Promise.all(remembered.map((r) => api.updateTask(r.id, { weekOf: target })));
      reload();
      notifyTasksChanged();
      const msg = `${plural(remembered.length, "task")} moved to this week${
        skipped ? ` · ${plural(skipped, "recurring task")} skipped` : ""
      }`;
      showToast(msg, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { weekOf: r.weekOf })))
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

  async function bulkStatus(status: Status, verb: string) {
    if (busy) return;
    const ids = Array.from(selected);
    if (!ids.length) return;
    const remembered = ids
      .map((id) => {
        const t = taskById.get(id);
        return t ? { id, status: t.status, weekOf: t.weekOf ?? "" } : null;
      })
      .filter((r): r is { id: string; status: Status; weekOf: string } => r !== null);
    setBusy(true);
    setSelected(new Set());
    try {
      await Promise.all(remembered.map((r) => api.updateTask(r.id, { status })));
      reload();
      notifyTasksChanged();
      // Terminalizing bumps a team task's weekOf server-side (docs/DESIGN_V6_
      // WEEK_ROLLOVER.md), so undoing just the status would leave weekOf at
      // the current week and the task would never reappear in this rollover
      // queue. Restore weekOf alongside status — an explicit weekOf on a
      // non-terminal-bound PATCH is admin-issued and the server accepts it.
      showToast(`${plural(remembered.length, "task")} ${verb}`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { status: r.status, weekOf: r.weekOf })))
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

  const selCount = selected.size;
  const selHasRecurring = Array.from(selected).some((id) => {
    const t = taskById.get(id);
    return t ? isRecurring(t) : false;
  });
  const hasItems = (tasks?.length ?? 0) > 0;

  return (
    <div className="ro-page">
      <header className="ro-header">
        <button type="button" className="ro-back" onClick={onBack}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="15 5 8 12 15 19" />
          </svg>
          Board
        </button>
        <div className="ro-header-row">
          <div className="ro-header-left">
            <h1 className="ro-title">Rollover</h1>
            <span className="ro-team-name">{teamName}</span>
            {tasks && (
              <span className="ro-meta">
                {plural(tasks.length, "open task")} from {plural(groups.length, "past week")}
              </span>
            )}
          </div>
          {hasItems && (
            <button type="button" className="ro-select-all" onClick={toggleAll}>
              {allSelected ? "Clear selection" : "Select all"}
            </button>
          )}
        </div>
      </header>

      {error && (
        <div className="error" style={{ margin: "0 32px 12px" }}>
          {error}
        </div>
      )}

      <div className="ro-body">
        <div className="ro-body-inner">
          {hasItems &&
            groups.map((g) => {
              const ids = g.rows.map((t) => t.id);
              const groupAllSel = ids.every((id) => selected.has(id));
              return (
                <div className="ro-group" key={g.week}>
                  <div className="ro-group-head">
                    <button
                      type="button"
                      className={`ro-check${groupAllSel ? " checked" : ""}`}
                      title="Select week"
                      onClick={() => toggleGroup(ids)}
                    >
                      {groupAllSel && <CheckGlyph />}
                    </button>
                    <span className="ro-group-label">{g.week}</span>
                    <span className="ro-group-range">{g.range}</span>
                    <span className="ro-rule" aria-hidden="true" />
                  </div>
                  <div className="ro-rows">
                    {g.rows.map((t) => {
                      const av = avatarFor(t.assigneeId);
                      const checked = selected.has(t.id);
                      const dim = t.status === "done" || t.status === "cancelled";
                      return (
                        <div
                          key={t.id}
                          className={`ro-row${checked ? " checked" : ""}`}
                          role="checkbox"
                          aria-checked={checked}
                          tabIndex={0}
                          onClick={() => toggle(t.id)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              toggle(t.id);
                            }
                          }}
                        >
                          <button
                            type="button"
                            className={`ro-check${checked ? " checked" : ""}`}
                            title="Select"
                            onClick={(e) => {
                              e.stopPropagation();
                              toggle(t.id);
                            }}
                          >
                            {checked && <CheckGlyph />}
                          </button>
                          <StatusControl status={t.status} onChange={(s) => setRowStatus(t.id, s)} />
                          <div className="ro-tick" style={{ background: PRIORITY_COLOR[t.priority] }} />
                          <div className={`ro-row-title${dim ? " dim" : ""}`}>{t.title}</div>
                          {isRecurring(t) && (
                            <span className="ro-recur-pill" title="Recurring — skipped by Move">
                              ↻ recurring
                            </span>
                          )}
                          <span className="ro-avatar" title={av.title} style={{ background: av.bg, color: av.fg }}>
                            {av.initials}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
            })}

          {tasks && !hasItems && (
            <div className="ro-empty">
              <div className="ro-empty-icon">
                <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="4 12 10 18 20 6" />
                </svg>
              </div>
              <div className="ro-empty-title">Nothing to roll over — all caught up</div>
              <div className="ro-empty-sub">All undated open tasks are inside the current week.</div>
              <button type="button" className="ro-empty-back" onClick={onBack}>
                ← Back to board
              </button>
            </div>
          )}
        </div>
      </div>

      <div className={`ro-bulkbar-wrap${selCount > 0 ? " visible" : ""}`} aria-hidden={selCount === 0}>
        {selHasRecurring && (
          <div className="ro-recur-hint">
            <span aria-hidden="true">↻</span> Recurring tasks stay put — Move skips them
          </div>
        )}
        <div className="ro-bulkbar">
          <span className="ro-bulk-count">{selCount} selected</span>
          <button type="button" className="ro-bulk-clear" title="Clear" onClick={() => setSelected(new Set())}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <line x1="6" y1="6" x2="18" y2="18" />
              <line x1="18" y1="6" x2="6" y2="18" />
            </svg>
          </button>
          <div className="ro-bulk-divider" />
          <button type="button" className="ro-bulk-move" disabled={busy} onClick={bulkMove}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
              <line x1="5" y1="12" x2="19" y2="12" />
              <polyline points="13 6 19 12 13 18" />
            </svg>
            Move to this week
          </button>
          <button type="button" className="ro-bulk-done" disabled={busy} onClick={() => bulkStatus("done", "marked done")}>
            Mark done
          </button>
          <button type="button" className="ro-bulk-cancel" disabled={busy} onClick={() => bulkStatus("cancelled", "cancelled")}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
