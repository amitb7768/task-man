// The workhorse row (design handoff "03 Task row" / TaskRow.dc.html).
// 48px, status control + priority tick + title + meta cluster, with a
// hover action cluster (done / reschedule-to-today / delete) that fades in
// over a gradient. Props are the source of truth — display is derived
// straight from `task` every render; the only local state is the ephemeral
// delete-collapse animation and an in-flight guard.
import { useState } from "react";
import type { Status, TaskView } from "../api";
import { api, ApiError } from "../api";
import { addDays, formatCompactDate, isOverdue, today } from "../period";
import { notifyTasksChanged } from "../App";
import { useAuth } from "../auth/AuthContext";
import StatusControl from "./StatusControl";
import { showToast } from "./Toast";

const PRIORITY_COLOR: Record<string, string> = {
  high: "var(--overdue)",
  medium: "var(--warn)",
  low: "var(--accent)",
  "": "transparent",
};

const PRIORITY_TITLE: Record<string, string> = {
  high: "High priority",
  medium: "Medium priority",
  low: "Low priority",
  "": "No priority",
};

const RECUR_LABEL: Record<string, string> = {
  daily: "Repeats daily",
  weekdays: "Repeats on selected weekdays",
  weekly: "Repeats weekly",
  monthly: "Repeats monthly",
};

// Deterministic avatar tone palette (docs/DESIGN_V3_TEAMS.md "Avatar tones").
// Kept in sync with the identical copy in views/TeamsLanding.tsx and
// views/TeamPage.tsx — those views compute the tone index (hash of member
// id) and pass it in via `assigneeAvatar`; this is purely a rendering
// concern local to the row, so no shared module is introduced across the
// view/component ownership boundary.
const AVATAR_TONES: { bg: string; fg: string }[] = [
  { bg: "var(--accent-soft)", fg: "var(--accent)" },
  { bg: "var(--warn-soft)", fg: "var(--warn)" },
  { bg: "var(--done-soft)", fg: "var(--done)" },
  { bg: "var(--surface-3)", fg: "var(--text-muted)" },
];

// The due chip renders ONLY from dueDate — no fallback to task.period —
// so undated tasks simply show no chip (docs/DESIGN_V3_TEAMS.md; mock §10).
function dueChipLabel(task: TaskView): string | null {
  if (task.status === "cancelled") return "Cancelled";
  if (task.status === "done") {
    if (task.completedAt) return `Done · ${formatCompactDate(task.completedAt.slice(0, 10))}`;
    return "Done";
  }
  if (task.dueDate) {
    if (task.dueDate === today()) return "Today";
    if (task.dueDate === addDays(today(), 1)) return "Tomorrow";
    return formatCompactDate(task.dueDate);
  }
  return null;
}

export interface TaskRowProps {
  task: TaskView;
  /** Optional — resolved team name for the meta-cluster team badge. Omitted call sites just don't show it. */
  teamName?: string;
  /** Optional — small assignee avatar in the meta cluster (docs/DESIGN_V3_TEAMS.md). Omitted call sites just don't show it. */
  assigneeAvatar?: { initials: string; tone: number };
  /** Optional — when dueDate is absent, render a faint "No date" chip instead of no chip (README "10"). */
  noDateChip?: boolean;
  onOpen: (id: string) => void;
  onChanged: () => void;
}

export default function TaskRow({ task, teamName, assigneeAvatar, noDateChip, onOpen, onChanged }: TaskRowProps) {
  const { user } = useAuth();
  const isAdmin = user.systemRole === "ADMIN";
  const [deleting, setDeleting] = useState(false);
  const [busy, setBusy] = useState(false);

  const dim = task.status === "done" || task.status === "cancelled";
  const overdue = !dim && isOverdue(task.dueDate, task.status);
  const dueLabel = dueChipLabel(task);

  function fail(e: unknown) {
    alert(e instanceof ApiError ? e.message : String(e));
  }

  async function setStatus(status: Status) {
    if (busy) return;
    setBusy(true);
    try {
      await api.updateTask(task.id, { status });
      onChanged();
      notifyTasksChanged();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function markDone() {
    if (busy || task.status === "done") return;
    await setStatus("done");
  }

  async function rescheduleToday() {
    if (busy) return;
    setBusy(true);
    try {
      await api.reschedule([task.id]);
      onChanged();
      notifyTasksChanged();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete() {
    if (busy || deleting) return;
    setBusy(true);
    try {
      const { deleted } = await api.deleteTask(task.id);
      setDeleting(true);
      notifyTasksChanged();
      showToast(`"${task.title}" deleted`, () => {
        api
          .restoreTasks(deleted)
          .then(() => {
            onChanged();
            notifyTasksChanged();
          })
          .catch(fail);
      });
      // Let the .18s collapse transition play before the parent's reload
      // actually removes this row from the list.
      window.setTimeout(() => onChanged(), 180);
    } catch (e) {
      fail(e);
      setBusy(false);
    }
  }

  return (
    <div className={`task-row${deleting ? " deleting" : ""}`} onClick={() => !deleting && onOpen(task.id)}>
      <StatusControl status={task.status} onChange={setStatus} disabled={busy} />

      <div
        className="priority-tick"
        title={PRIORITY_TITLE[task.priority]}
        style={{ background: PRIORITY_COLOR[task.priority] }}
      />

      <div className={`task-row-title${dim ? " dim" : ""}`}>{task.title}</div>

      <div className="task-row-meta">
        {task.recurrence && (
          <span className="task-row-recur" title={RECUR_LABEL[task.recurrence.freq] ?? "Recurring"}>
            ↻
          </span>
        )}
        {task.progress.total > 0 && (
          <span className="chip subtle" title="Subtasks">
            {task.progress.done}/{task.progress.total}
          </span>
        )}
        {teamName && (
          <span className="badge team-badge">
            <span className="badge-dot" aria-hidden="true" />
            {teamName}
          </span>
        )}
        {assigneeAvatar && (
          <span
            className="task-row-assignee"
            title={assigneeAvatar.initials}
            style={{
              width: 22,
              height: 22,
              flex: "none",
              borderRadius: "999px",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              fontSize: 10,
              fontWeight: 600,
              background: AVATAR_TONES[assigneeAvatar.tone % AVATAR_TONES.length].bg,
              color: AVATAR_TONES[assigneeAvatar.tone % AVATAR_TONES.length].fg,
            }}
          >
            {assigneeAvatar.initials}
          </span>
        )}
        {dueLabel ? (
          <span className={`chip due${overdue ? " overdue" : ""}`}>{dueLabel}</span>
        ) : (
          noDateChip && <span className="chip due nodate">No date</span>
        )}
      </div>

      <div className="task-row-actions" onClick={(e) => e.stopPropagation()}>
        <button type="button" className="act-done" title="Mark done" disabled={busy} onClick={markDone}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="4 12 10 18 20 6" />
          </svg>
        </button>
        <button type="button" className="act-today" title="Reschedule to today" disabled={busy} onClick={rescheduleToday}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="5" width="18" height="16" rx="2" />
            <line x1="3" y1="9.5" x2="21" y2="9.5" />
            <polyline points="9 15 12 12 12 18" />
            <line x1="12" y1="12" x2="15" y2="12" />
          </svg>
        </button>
        {isAdmin && (
          <button type="button" className="act-delete" title="Delete" disabled={busy} onClick={handleDelete}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 7 20 7" />
              <path d="M9 7V5h6v2" />
              <path d="M6 7l1 13h10l1-13" />
            </svg>
          </button>
        )}
      </div>
    </div>
  );
}
