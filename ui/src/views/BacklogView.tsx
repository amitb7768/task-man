// Backlog — the unstaffed personal planning pool (docs/DESIGN_V7_BACKLOG.md).
// ADMIN-only tab (App.tsx hides the nav item for USER; the server 403s the
// fetch regardless). Structural anatomy borrows from two siblings: the
// priority-grouped sections (mono headers + hairline rules, newest first,
// empty groups omitted) copy views/AllTasksView.tsx; the row shape (leading
// checkbox + StatusControl + priority tick + title, no due chip/avatar — a
// backlog task never has either) and the bulk bar follow views/TeamRollover.tsx,
// this view's closest structural sibling. TaskRow itself isn't reused for the
// active rows: its built-in hover actions (mark done / reschedule-to-today /
// delete) are the wrong set for a parked task — there's no dueDate/period to
// reschedule and no team-board place to send it back to, only Assign…
// (staff it onto a team) or delete — so rows are custom-rendered inline,
// TeamRollover-style, with an Assign…/delete hover cluster instead.
// CompletedFold at the bottom (default TaskRow
// rendering, ui/CLAUDE.md convention) still tucks away done/cancelled parked
// items exactly as elsewhere.
import { useEffect, useMemo, useState } from "react";
import type { Priority, Status, Team, TaskView } from "../api";
import { api, ApiError } from "../api";
import { today } from "../period";
import { notifyTasksChanged } from "../App";
import StatusControl from "../components/StatusControl";
import TaskComposer from "../components/TaskComposer";
import TaskDetail from "../components/TaskDetail";
import AssignPopover from "../components/AssignPopover";
import { dismissToast, showToast } from "../components/Toast";
import CompletedFold, { isCompletedStatus } from "../components/CompletedFold";
import "../styles/backlog.css";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}
function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

const PRIORITY_COLOR: Record<Priority, string> = {
  high: "var(--overdue)",
  medium: "var(--warn)",
  low: "var(--accent)",
  "": "transparent",
};

type PrioKey = "high" | "medium" | "low" | "none";
const PRIORITY_GROUPS: { key: PrioKey; label: string; dot: string }[] = [
  { key: "high", label: "High", dot: "var(--overdue)" },
  { key: "medium", label: "Medium", dot: "var(--warn)" },
  { key: "low", label: "Low", dot: "var(--accent)" },
  { key: "none", label: "No priority", dot: "var(--border-strong)" },
];
function prioKey(p: Priority): PrioKey {
  return p === "high" || p === "medium" || p === "low" ? p : "none";
}

function CheckGlyph() {
  return (
    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 12 10 18 20 6" />
    </svg>
  );
}
function TrashGlyph() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 7 20 7" />
      <path d="M9 7V5h6v2" />
      <path d="M6 7l1 13h10l1-13" />
    </svg>
  );
}
function ClearGlyph() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      <line x1="6" y1="6" x2="18" y2="18" />
      <line x1="18" y1="6" x2="6" y2="18" />
    </svg>
  );
}

// Restore payload for un-assigning a task back to the pool is constant — a
// freshly-assigned backlog task had no team/assignee/dueDate to snapshot
// (docs/DESIGN_V7_BACKLOG.md "Assign popover"). assigneeId is cleared in the
// SAME patch as teamId (assigneeId requires teamId server-side, so clearing
// one without the other 400s) — the exact mechanism bulkReassign's undo
// already relies on in views/TeamPage.tsx.
const UNASSIGN_PATCH = { teamId: null, assigneeId: null, horizon: "backlog", period: "", dueDate: "" } as const;

export default function BacklogView() {
  const [tasks, setTasks] = useState<TaskView[] | null>(null);
  const [teams, setTeams] = useState<Team[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [assignTarget, setAssignTarget] = useState<string | "bulk" | null>(null);
  const [busy, setBusy] = useState(false);

  function reload() {
    setError(null);
    api
      .backlog()
      .then((r) => {
        setTasks(r.tasks);
        setSelected((prev) => new Set([...prev].filter((id) => r.tasks.some((t) => t.id === id))));
      })
      .catch((e) => setError(errMsg(e)));
  }

  useEffect(() => {
    reload();
    return () => dismissToast();
  }, []);
  useEffect(() => {
    api.listTeams().then(setTeams).catch(() => {});
  }, []);

  const teamNames = useMemo(() => {
    const m = new Map<string, string>();
    for (const t of teams) m.set(t.id, t.name);
    return m;
  }, [teams]);

  function handleChanged() {
    reload();
    notifyTasksChanged();
  }
  function fail(e: unknown) {
    setError(errMsg(e));
  }

  const activeTasks = useMemo(() => (tasks ?? []).filter((t) => !isCompletedStatus(t.status)), [tasks]);
  const completedTasks = useMemo(() => (tasks ?? []).filter((t) => isCompletedStatus(t.status)), [tasks]);

  // Server sorts createdAt desc (docs/DESIGN_V7_BACKLOG.md "Endpoints") — a
  // plain filter per group preserves that order, so "newest first within
  // each" falls out for free without a client-side sort.
  const groups = useMemo(
    () =>
      PRIORITY_GROUPS.map((g) => ({ ...g, tasks: activeTasks.filter((t) => prioKey(t.priority) === g.key) })).filter(
        (g) => g.tasks.length > 0,
      ),
    [activeTasks],
  );

  function toggle(id: string) {
    if (selected.size === 0) dismissToast();
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  function clearSelection() {
    setSelected(new Set());
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

  async function deleteOne(task: TaskView) {
    try {
      const { deleted } = await api.deleteTask(task.id);
      reload();
      notifyTasksChanged();
      showToast(`"${task.title}" deleted`, () => {
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
    }
  }

  async function bulkDelete() {
    if (busy) return;
    const ids = Array.from(selected);
    if (!ids.length) return;
    setBusy(true);
    setSelected(new Set());
    try {
      const results = await Promise.all(ids.map((id) => api.deleteTask(id)));
      const deleted = results.flatMap((r) => r.deleted);
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

  // Assign (single row's Assign… or the bulk bar's) — the popover only
  // resolves {teamId, assigneeId?, dueDate?}; this performs the actual flip
  // PATCH (contract: {teamId, horizon:"daily", period:today(), assigneeId?,
  // dueDate?}) and, on undo, re-parks every task with the constant
  // UNASSIGN_PATCH above.
  async function assignTasks(ids: string[], input: { teamId: string; assigneeId?: string; dueDate?: string }) {
    if (busy || !ids.length) return;
    setBusy(true);
    setAssignTarget(null);
    setSelected(new Set());
    try {
      await Promise.all(
        ids.map((id) =>
          api.updateTask(id, {
            teamId: input.teamId,
            horizon: "daily",
            period: today(),
            assigneeId: input.assigneeId,
            dueDate: input.dueDate,
          }),
        ),
      );
      reload();
      notifyTasksChanged();
      const teamName = teamNames.get(input.teamId) ?? "team";
      showToast(`Assigned ${plural(ids.length, "task")} to ${teamName}`, () => {
        Promise.all(ids.map((id) => api.updateTask(id, UNASSIGN_PATCH)))
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
  const hasAny = activeTasks.length > 0 || completedTasks.length > 0;

  return (
    <div className="bl-view">
      {error && <div className="error">{error}</div>}

      <div className="bl-toolbar">
        <TaskComposer context="backlog" onCreated={handleChanged} />
        {tasks && <span className="bl-count">{plural(activeTasks.length, "task")} parked</span>}
      </div>

      {tasks && !hasAny && (
        <div className="bl-empty">
          <div className="bl-empty-icon">
            <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 12 10 18 20 6" />
            </svg>
          </div>
          <div className="bl-empty-title">Nothing parked</div>
          <div className="bl-empty-sub">
            Add tasks here to plan later, then assign them to a team when you&rsquo;re ready.
          </div>
        </div>
      )}

      {groups.length > 0 && (
        <div className="bl-groups">
          {groups.map((g) => (
            <div className="bl-group" key={g.key}>
              <div className="bl-group-head">
                <span className="bl-group-dot" style={{ background: g.dot }} />
                <span className="bl-group-label">{g.label}</span>
                <span className="bl-group-count">{g.tasks.length}</span>
                <span className="bl-group-rule" />
              </div>
              <div className="bl-rows">
                {g.tasks.map((t) => {
                  const checked = selected.has(t.id);
                  return (
                    <div key={t.id} className={`bl-row${checked ? " checked" : ""}`}>
                      <button type="button" className={`bl-check${checked ? " checked" : ""}`} title="Select" onClick={() => toggle(t.id)}>
                        {checked && <CheckGlyph />}
                      </button>
                      <div className="bl-row-main" onClick={() => setSelectedId(t.id)}>
                        <StatusControl status={t.status} onChange={(s) => setRowStatus(t.id, s)} />
                        <div className="bl-tick" style={{ background: PRIORITY_COLOR[t.priority] }} />
                        <div className="bl-row-title">{t.title}</div>
                      </div>
                      <div className="bl-row-actions">
                        <div className="bl-assign-anchor">
                          <button
                            type="button"
                            className="assign-trigger bl-assign-trigger"
                            onClick={(e) => {
                              e.stopPropagation();
                              setAssignTarget((v) => (v === t.id ? null : t.id));
                            }}
                          >
                            Assign…
                          </button>
                          {assignTarget === t.id && (
                            <AssignPopover
                              onAssign={(input) => assignTasks([t.id], input)}
                              onClose={() => setAssignTarget(null)}
                            />
                          )}
                        </div>
                        <button
                          type="button"
                          className="bl-delete-trigger"
                          title="Delete"
                          onClick={(e) => {
                            e.stopPropagation();
                            deleteOne(t);
                          }}
                        >
                          <TrashGlyph />
                        </button>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      )}

      <CompletedFold tasks={completedTasks} onOpen={setSelectedId} onChanged={handleChanged} />

      <div className={`bl-bulkbar-wrap${selCount > 0 ? " visible" : ""}`} aria-hidden={selCount === 0}>
        <div className="bl-bulkbar">
          <span className="bl-bulk-count">{selCount} selected</span>
          <button type="button" className="bl-bulk-clear" title="Clear" onClick={clearSelection}>
            <ClearGlyph />
          </button>
          <div className="bl-bulk-divider" />
          <div className="bl-bulk-assign-anchor">
            <button
              type="button"
              className="assign-trigger bl-bulk-assign"
              disabled={busy}
              onClick={() => setAssignTarget((v) => (v === "bulk" ? null : "bulk"))}
            >
              Assign…
            </button>
            {assignTarget === "bulk" && (
              <AssignPopover
                placement="top"
                onAssign={(input) => assignTasks(Array.from(selected), input)}
                onClose={() => setAssignTarget(null)}
              />
            )}
          </div>
          <button type="button" className="bl-bulk-delete" disabled={busy} onClick={bulkDelete}>
            Delete
          </button>
        </div>
      </div>

      {selectedId && <TaskDetail id={selectedId} onClose={() => setSelectedId(null)} onChanged={handleChanged} />}
    </div>
  );
}
