// Task detail slide-in panel (design handoff "05 Task Detail").
// Slides in from the right, 452px, editorial body (borderless title/notes),
// hairline property rows, a recurrence toggle, and an inline subtask list.
// Autosaves on blur/change — no save buttons. Deletes (task + subtask) never
// confirm(): they act immediately and hand off to an undo toast.
//
// Prop contract is fixed — every view renders this the same way:
//   {selectedId && <TaskDetail id={selectedId} onClose={...} onChanged={...} />}
// so open/close is driven by the parent conditionally mounting us; the exit
// slide-out is played locally (see requestClose) before we actually call
// onClose and let the parent unmount us.
import { useCallback, useEffect, useState } from "react";
import type {
  TaskDetail as TaskDetailData,
  Horizon,
  Priority,
  Recurrence,
  RecurrenceFreq,
  Status,
  TaskView,
  TeamBoardResponse,
  UpdateTaskInput,
} from "../api";
import { api, ApiError, horizonRank } from "../api";
import {
  currentMonth,
  currentWeek,
  formatDayLabel,
  formatMonthLabel,
  formatWeekLabel,
  today,
  WEEKDAY_LABELS,
} from "../period";
import { notifyTasksChanged } from "../App";
import { useAuth } from "../auth/AuthContext";
import { isCompletedStatus } from "./CompletedFold";
import StatusControl from "./StatusControl";
import { showToast } from "./Toast";
import "../styles/task-detail.css";

const HORIZONS: Horizon[] = ["daily", "weekly", "monthly"];
// backlog is never offered as a subtask horizon (HORIZONS above excludes it);
// the key only exists to satisfy Record<Horizon, string> (docs/DESIGN_V7_BACKLOG.md).
const HORIZON_NAME: Record<Horizon, string> = { daily: "Daily", weekly: "Weekly", monthly: "Monthly", backlog: "Backlog" };
const STATUS_LABEL: Record<Status, string> = {
  todo: "To do",
  in_progress: "in-progress", // wire value is in_progress; display label is "in-progress" (docs/DESIGN_V2_UI.md)
  done: "Done",
  cancelled: "Cancelled",
};
const PRIORITY_OPTIONS: { value: Priority; label: string; dotColor?: string }[] = [
  { value: "", label: "None" },
  { value: "low", label: "Low", dotColor: "var(--accent)" },
  { value: "medium", label: "Med", dotColor: "var(--warn)" },
  { value: "high", label: "High", dotColor: "var(--overdue)" },
];
const PRESETS: { value: RecurrenceFreq; label: string }[] = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "weekdays", label: "Weekdays" },
];
// freq -> horizon of the tasks it spawns (mirrors server/recur.go
// recurrenceHorizon). The task's horizon row is read-only here, so a preset
// whose implied horizon doesn't match the task's horizon would always 400 —
// only offer presets that are actually compatible.
const PRESET_HORIZON: Record<RecurrenceFreq, Horizon> = {
  daily: "daily",
  weekdays: "daily",
  weekly: "weekly",
  monthly: "monthly",
};
const DEFAULT_WEEKDAYS = [1, 2, 3, 4, 5]; // Mon-Fri, ISO Mon=1

function defaultPeriodFor(h: Horizon): string {
  if (h === "daily") return today();
  if (h === "weekly") return currentWeek();
  return currentMonth();
}

function horizonPeriodLabel(h: Horizon, period: string): string {
  if (h === "backlog") return "Unscheduled";
  if (h === "monthly") return formatMonthLabel(period);
  if (h === "weekly") return formatWeekLabel(period);
  return formatDayLabel(period);
}

// Server-managed; never round-tripped back on a PATCH (see docs/DESIGN_V2_UI.md
// "Recurrence interval" — anchor is set/re-anchored server-side only).
function stripAnchor(r: Recurrence): Recurrence {
  return { freq: r.freq, interval: r.interval, weekdays: r.weekdays, dayOfMonth: r.dayOfMonth };
}

function recurSummary(r: Recurrence): string {
  const n = r.interval ?? 1;
  switch (r.freq) {
    case "daily":
      return n === 1 ? "Repeats every day" : `Repeats every ${n} days`;
    case "weekly":
      return n === 1 ? "Repeats every week" : `Repeats every ${n} weeks`;
    case "monthly": {
      const suffix = r.dayOfMonth ? ` on day ${r.dayOfMonth}` : "";
      return (n === 1 ? "Repeats every month" : `Repeats every ${n} months`) + suffix;
    }
    case "weekdays": {
      const days = (r.weekdays ?? []).length
        ? r.weekdays!
            .slice()
            .sort((a, b) => a - b)
            .map((d) => WEEKDAY_LABELS[d - 1])
            .join(", ")
        : "no days selected";
      return n === 1 ? `Repeats weekly on ${days}` : `Repeats every ${n} weeks on ${days}`;
    }
    default:
      return "";
  }
}

interface TaskDetailProps {
  id: string;
  onClose: () => void;
  onChanged: () => void;
}

export default function TaskDetail({ id, onClose, onChanged }: TaskDetailProps) {
  const { user } = useAuth();
  // Task DELETE is ADMIN-only everywhere — including a user's own personal
  // tasks (docs/AUTH_FEATURES.md decision #5: "Task DELETE is ADMIN-only
  // (everywhere)"). Subtasks are tasks too (deleteSubtask calls the same
  // api.deleteTask), so the subtask remove affordance is gated alongside the
  // main delete button — leaving it visible for a USER would just 403.
  const isAdmin = user.systemRole === "ADMIN";
  const [currentId, setCurrentId] = useState(id);
  const [detail, setDetail] = useState<TaskDetailData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [titleDraft, setTitleDraft] = useState("");
  const [notesDraft, setNotesDraft] = useState("");
  const [subtaskTitle, setSubtaskTitle] = useState("");
  const [subtaskHorizon, setSubtaskHorizon] = useState<Horizon>("daily");
  const [saveState, setSaveState] = useState<"idle" | "saving">("idle");
  const [deletingTask, setDeletingTask] = useState(false);
  const [closing, setClosing] = useState(false);
  const [teamBoard, setTeamBoard] = useState<TeamBoardResponse | null>(null);

  useEffect(() => setCurrentId(id), [id]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .getTask(currentId)
      .then((d) => {
        if (cancelled) return;
        setDetail(d);
        setTitleDraft(d.title);
        setNotesDraft(d.notes ?? "");
        setSubtaskHorizon(d.horizon);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(e instanceof ApiError ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [currentId]);

  // Assignee row (team tasks only) — lazy-fetch the team board once the row
  // would render, same fetch-effect + cancelled-guard shape as the getTask
  // effect above. Reused for its member list + open-count derivation, same
  // as components/AssignPopover.tsx.
  useEffect(() => {
    const teamId = detail?.teamId;
    // Clear any prior team's board immediately — otherwise switching from a
    // team-A task to a team-B task would render team-A's members/counts
    // until team-B's fetch resolves.
    setTeamBoard(null);
    if (!teamId) {
      return;
    }
    let cancelled = false;
    api
      .teamBoard(teamId)
      .then((b) => {
        if (!cancelled) setTeamBoard(b);
      })
      .catch(() => {
        // Non-fatal — the assignee select just stays disabled; the rest of
        // the panel still works.
      });
    return () => {
      cancelled = true;
    };
  }, [detail?.teamId]);

  const requestClose = useCallback(() => {
    setClosing((already) => {
      if (already) return already;
      window.setTimeout(onClose, 260);
      return true;
    });
  }, [onClose]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") requestClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [requestClose]);

  function fail(e: unknown) {
    alert(e instanceof ApiError ? e.message : String(e));
  }

  // onSuccess fires only after the PATCH actually lands — used by the
  // assignee select to refetch teamBoard so "N open" counts don't go stale
  // (m4: cheapest fix is refetching inline once the patch resolves, rather
  // than plumbing a refresh counter through the fetch effect).
  async function patch(update: UpdateTaskInput, onSuccess?: () => void) {
    if (!detail) return;
    setSaveState("saving");
    try {
      const updated = await api.updateTask(detail.id, update);
      setDetail((d) => (d ? { ...updated, children: d.children } : d));
      notifyTasksChanged();
      onChanged();
      onSuccess?.();
    } catch (e) {
      fail(e);
    } finally {
      setSaveState("idle");
    }
  }

  async function reloadCurrent() {
    const d = await api.getTask(currentId);
    setDetail(d);
    notifyTasksChanged();
    onChanged();
  }

  function setFreq(freq: RecurrenceFreq) {
    if (!detail?.recurrence) return;
    const base = detail.recurrence;
    const next: Recurrence = { freq, interval: base.interval };
    if (freq === "weekdays") {
      const prior = base.freq === "weekdays" ? (base.weekdays ?? []) : [];
      // Never send an empty weekdays array — the server 400s on it. Default
      // to Mon-Fri when there's no prior selection to carry over.
      next.weekdays = prior.length > 0 ? prior : DEFAULT_WEEKDAYS;
    }
    if (freq === "monthly") next.dayOfMonth = base.freq === "monthly" ? base.dayOfMonth : undefined;
    patch({ recurrence: next });
  }

  function setInterval_(n: number) {
    if (!detail?.recurrence) return;
    const v = Math.max(1, Math.floor(n) || 1);
    patch({ recurrence: { ...stripAnchor(detail.recurrence), interval: v } });
  }

  function toggleWeekday(day: number) {
    if (!detail?.recurrence) return;
    const set = new Set(detail.recurrence.weekdays ?? []);
    if (set.has(day)) {
      // Never send an empty weekdays array — the server 400s on it. Ignore
      // the toggle rather than deselect the last remaining weekday.
      if (set.size === 1) return;
      set.delete(day);
    } else {
      set.add(day);
    }
    patch({
      recurrence: { ...stripAnchor(detail.recurrence), freq: "weekdays", weekdays: Array.from(set).sort((a, b) => a - b) },
    });
  }

  function setDayOfMonth(day: number | undefined) {
    if (!detail?.recurrence) return;
    patch({ recurrence: { ...stripAnchor(detail.recurrence), freq: "monthly", dayOfMonth: day } });
  }

  function toggleRecurOn() {
    if (detail?.recurrence) {
      patch({ recurrence: null });
    } else {
      patch({ recurrence: { freq: "weekly" } });
    }
  }

  async function toggleSubtask(child: TaskView) {
    const nextStatus: Status = child.status === "done" ? "todo" : "done";
    try {
      await api.updateTask(child.id, { status: nextStatus });
      await reloadCurrent();
    } catch (e) {
      fail(e);
    }
  }

  async function deleteSubtask(child: TaskView) {
    try {
      const { deleted } = await api.deleteTask(child.id);
      showToast(`Deleted "${child.title}"`, () => {
        api
          .restoreTasks(deleted)
          .then(() => reloadCurrent())
          .catch(fail);
      });
      await reloadCurrent();
    } catch (e) {
      fail(e);
    }
  }

  async function addSubtask() {
    if (!detail || !subtaskTitle.trim()) return;
    const period = subtaskHorizon === detail.horizon ? detail.period : defaultPeriodFor(subtaskHorizon);
    try {
      await api.createTask({
        title: subtaskTitle.trim(),
        horizon: subtaskHorizon,
        period,
        parentId: detail.id,
      });
      setSubtaskTitle("");
      await reloadCurrent();
    } catch (e) {
      fail(e);
    }
  }

  async function handleDeleteTask() {
    if (!detail || deletingTask) return;
    setDeletingTask(true);
    try {
      const { deleted } = await api.deleteTask(detail.id);
      notifyTasksChanged();
      onChanged();
      showToast(`Deleted "${detail.title}"`, () => {
        api
          .restoreTasks(deleted)
          .then(() => {
            onChanged();
            notifyTasksChanged();
          })
          .catch(fail);
      });
      requestClose();
    } catch (e) {
      setDeletingTask(false);
      fail(e);
    }
  }

  const allowedSubtaskHorizons = detail
    ? HORIZONS.filter((h) => horizonRank[h] <= horizonRank[detail.horizon])
    : [];

  // Same open-count derivation as AssignPopover's member list
  // (components/AssignPopover.tsx) — just sourced from this row's own
  // teamBoard fetch instead of the popover's per-pick one.
  const assigneeOptions = teamBoard
    ? teamBoard.members.map(({ member, tasks }) => ({
        id: member.id,
        name: member.name,
        open: tasks.filter((t) => !isCompletedStatus(t.status)).length,
      }))
    : [];

  const rec = detail?.recurrence;
  const done = detail?.progress.done ?? 0;
  const total = detail?.progress.total ?? 0;
  const pct = total ? Math.round((done / total) * 100) : 0;

  return (
    <>
      <div className={`td-backdrop${closing ? " closing" : ""}`} onClick={requestClose} />
      <aside
        className={`td-panel${closing ? " closing" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-label="Task detail"
      >
        <div className="td-header">
          {detail ? (
            <>
              <StatusControl status={detail.status} onChange={(s) => patch({ status: s })} disabled={deletingTask} />
              <span className="td-status-label">{STATUS_LABEL[detail.status]}</span>
            </>
          ) : (
            <span style={{ flex: 1 }} />
          )}
          {isAdmin && (
            <button
              type="button"
              className="td-icon-btn td-icon-danger"
              title="Delete task"
              disabled={!detail || deletingTask}
              onClick={handleDeleteTask}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="4 7 20 7" />
                <path d="M9 7V5h6v2" />
                <path d="M6 7l1 13h10l1-13" />
              </svg>
            </button>
          )}
          <button type="button" className="td-icon-btn" title="Close" onClick={requestClose}>
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round">
              <line x1="6" y1="6" x2="18" y2="18" />
              <line x1="18" y1="6" x2="6" y2="18" />
            </svg>
          </button>
        </div>

        <div className="td-scroll">
          {loading && <div className="empty" style={{ padding: "22px 24px" }}>Loading…</div>}
          {error && <div className="error" style={{ padding: "22px 24px" }}>{error}</div>}

          {detail && !loading && (
            <>
              {detail.parentId && (
                <button type="button" className="link td-back-link" onClick={() => setCurrentId(detail.parentId!)}>
                  ‹ parent task
                </button>
              )}

              <div className="td-titleblock">
                <input
                  className="td-title"
                  value={titleDraft}
                  placeholder="Task title"
                  onChange={(e) => setTitleDraft(e.target.value)}
                  onBlur={() => {
                    if (titleDraft.trim() && titleDraft !== detail.title) {
                      patch({ title: titleDraft.trim() });
                    } else {
                      setTitleDraft(detail.title);
                    }
                  }}
                />
                <textarea
                  className="td-notes"
                  rows={2}
                  value={notesDraft}
                  placeholder="Add notes…"
                  onChange={(e) => setNotesDraft(e.target.value)}
                  onBlur={() => {
                    if (notesDraft !== (detail.notes ?? "")) {
                      patch({ notes: notesDraft });
                    }
                  }}
                />
              </div>

              <div className="td-props">
                <div className="td-row">
                  <span className="td-row-label">Priority</span>
                  <div className="td-seg">
                    {PRIORITY_OPTIONS.map((opt) => (
                      <button
                        key={opt.value || "none"}
                        type="button"
                        className={`td-seg-btn${detail.priority === opt.value ? " active" : ""}`}
                        style={detail.priority === opt.value && opt.dotColor ? { color: opt.dotColor } : undefined}
                        onClick={() => patch({ priority: opt.value })}
                      >
                        {opt.dotColor && <span className="td-seg-dot" style={{ background: opt.dotColor }} />}
                        {opt.label}
                      </button>
                    ))}
                  </div>
                </div>

                {detail.horizon !== "backlog" && (
                  <div className="td-row">
                    <span className="td-row-label">Due date</span>
                    <input
                      type="date"
                      className="td-due-input"
                      value={detail.dueDate ?? ""}
                      onChange={(e) => patch({ dueDate: e.target.value })}
                    />
                    <button
                      type="button"
                      className="td-clear-btn"
                      disabled={!detail.dueDate}
                      onClick={() => patch({ dueDate: "" })}
                    >
                      clear
                    </button>
                  </div>
                )}

                {detail.teamId && (
                  <div className="td-row">
                    <span className="td-row-label">
                      Assignee{teamBoard ? ` · ${teamBoard.team.name}` : ""}
                    </span>
                    <select
                      className="td-assignee-select"
                      value={detail.assigneeId ?? ""}
                      disabled={!teamBoard}
                      onChange={(e) => {
                        const teamId = detail.teamId;
                        patch({ assigneeId: e.target.value || null }, () => {
                          // Refetch so "N open" counts reflect the new
                          // assignment instead of going stale (m4).
                          if (teamId) {
                            api
                              .teamBoard(teamId)
                              .then(setTeamBoard)
                              .catch(() => {});
                          }
                        });
                      }}
                    >
                      <option value="">Unassigned</option>
                      {/* teamBoard hasn't resolved (loading or failed) but the
                          task already has an assignee — render a placeholder
                          option so the select isn't blank; no name info is
                          available client-side without the board (m3). */}
                      {!teamBoard && detail.assigneeId && (
                        <option value={detail.assigneeId}>Assigned</option>
                      )}
                      {assigneeOptions.map((m) => (
                        <option key={m.id} value={m.id}>
                          {m.name} · {m.open} open
                        </option>
                      ))}
                    </select>
                  </div>
                )}

                <div className="td-row">
                  <span className="td-row-label">Horizon</span>
                  <span className="td-horizon-value">
                    <span className="td-horizon-name">{HORIZON_NAME[detail.horizon]}</span>
                    <span className="td-horizon-period">· {horizonPeriodLabel(detail.horizon, detail.period)}</span>
                  </span>
                </div>
              </div>

              {detail.horizon !== "backlog" && (
              <div className="td-recur">
                <div className="td-recur-head">
                  <span className="td-recur-label">Repeat</span>
                  <button
                    type="button"
                    className={`td-switch${rec ? " on" : ""}`}
                    title="Toggle recurrence"
                    onClick={toggleRecurOn}
                  >
                    <span className="td-switch-knob" />
                  </button>
                </div>

                {rec && (
                  <div className="td-recur-body">
                    <div className="td-presets">
                      {PRESETS.filter((p) => PRESET_HORIZON[p.value] === detail.horizon).map((p) => (
                        <button
                          key={p.value}
                          type="button"
                          className={`td-preset-btn${rec.freq === p.value ? " active" : ""}`}
                          onClick={() => setFreq(p.value)}
                        >
                          {p.label}
                        </button>
                      ))}
                    </div>

                    {rec.freq === "weekdays" && (
                      <div className="td-weekday-row">
                        {WEEKDAY_LABELS.map((label, i) => {
                          const day = i + 1;
                          const active = (rec.weekdays ?? []).includes(day);
                          return (
                            <button
                              key={day}
                              type="button"
                              className={`td-weekday-chip${active ? " active" : ""}`}
                              onClick={() => toggleWeekday(day)}
                            >
                              {label}
                            </button>
                          );
                        })}
                      </div>
                    )}

                    {rec.freq === "monthly" && (
                      <div className="td-interval-row">
                        <span>On day</span>
                        <input
                          type="number"
                          min={1}
                          max={31}
                          className="td-dom-input"
                          value={rec.dayOfMonth ?? ""}
                          onChange={(e) => setDayOfMonth(e.target.value ? Number(e.target.value) : undefined)}
                        />
                      </div>
                    )}

                    <div className="td-interval-row">
                      <span>Every</span>
                      <input
                        type="number"
                        min={1}
                        className="td-interval-input"
                        value={rec.interval ?? 1}
                        onChange={(e) => setInterval_(Number(e.target.value))}
                      />
                      <span>{{ daily: "day(s)", weekly: "week(s)", monthly: "month(s)", weekdays: "week(s)" }[rec.freq]}</span>
                    </div>

                    <div className="td-summary">↻ {recurSummary(rec)}</div>
                  </div>
                )}
              </div>
              )}

              <div className="td-subtasks">
                <div className="td-sub-head">
                  <span className="td-sub-head-title">Subtasks</span>
                  <span className="td-sub-progress-chip">
                    {done}/{total}
                  </span>
                  <div className="td-sub-progress-track">
                    <div className="td-sub-progress-fill" style={{ width: `${pct}%` }} />
                  </div>
                </div>

                <div className="td-sub-list">
                  {detail.children.map((child) => {
                    const isDone = child.status === "done";
                    return (
                      <div className="td-sub-row" key={child.id} onClick={() => setCurrentId(child.id)}>
                        <button
                          type="button"
                          className={`td-sub-check${isDone ? " done" : ""}`}
                          title={isDone ? "Mark not done" : "Mark done"}
                          onClick={(e) => {
                            e.stopPropagation();
                            toggleSubtask(child);
                          }}
                        >
                          {isDone && (
                            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
                              <polyline points="4 12 10 18 20 6" />
                            </svg>
                          )}
                        </button>
                        <span className={`td-sub-title${isDone ? " done" : ""}`}>{child.title}</span>
                        <span className="td-sub-horizon-pill">{child.horizon}</span>
                        {isAdmin && (
                          <button
                            type="button"
                            className="td-sub-delete"
                            title="Remove"
                            onClick={(e) => {
                              e.stopPropagation();
                              deleteSubtask(child);
                            }}
                          >
                            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round">
                              <line x1="6" y1="6" x2="18" y2="18" />
                              <line x1="18" y1="6" x2="6" y2="18" />
                            </svg>
                          </button>
                        )}
                      </div>
                    );
                  })}

                  <div className="td-sub-add">
                    <span className="td-sub-add-icon">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                        <line x1="12" y1="5" x2="12" y2="19" />
                        <line x1="5" y1="12" x2="19" y2="12" />
                      </svg>
                    </span>
                    <input
                      className="td-sub-add-input"
                      placeholder="Add a subtask…"
                      value={subtaskTitle}
                      onChange={(e) => setSubtaskTitle(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          addSubtask();
                        }
                      }}
                    />
                    <select
                      className="td-sub-add-select"
                      title="Subtask horizon"
                      value={subtaskHorizon}
                      onChange={(e) => setSubtaskHorizon(e.target.value as Horizon)}
                    >
                      {allowedSubtaskHorizons.map((h) => (
                        <option key={h} value={h}>
                          {HORIZON_NAME[h]}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>
              </div>
            </>
          )}
        </div>

        {detail && !loading && (
          <div className="td-footer">
            <span className="td-save-status">{saveState === "saving" ? "Saving…" : "Saved · just now"}</span>
            <span className="td-footer-hint">Changes save automatically</span>
          </div>
        )}
      </aside>
    </>
  );
}
