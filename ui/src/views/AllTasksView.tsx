// "All tasks" — design handoff "04 All Tasks". Toolbar (Group by / Sort /
// open count) above a grouped listing built from the shared TaskRow
// primitive. Grouping/sorting are pure client-side derivations over the
// open-task set fetched once per reload; see docs/DESIGN_V2_UI.md for CSS
// ownership rules (this view's styles live in styles/all-tasks.css).
import { useEffect, useMemo, useState } from "react";
import type { Priority, Team, TaskView } from "../api";
import { api, ApiError } from "../api";
import { currentWeek, isOverdue, today, weekDates } from "../period";
import TaskRow from "../components/TaskRow";
import TaskDetail from "../components/TaskDetail";
import { notifyTasksChanged } from "../App";
import "../styles/all-tasks.css";

type GroupBy = "smart" | "priority" | "horizon";
type SortBy = "due" | "priority" | "title";

interface GroupDef {
  key: string;
  label: string;
  dot: string;
  labelColor: string;
  test: (t: TaskView) => boolean;
}

const GROUP_BY_OPTIONS: { value: GroupBy; label: string }[] = [
  { value: "smart", label: "Smart" },
  { value: "priority", label: "Priority" },
  { value: "horizon", label: "Horizon" },
];

// Smart-grouping bucket. "No date" == the task has no dueDate at all (period
// is mandatory on every task, so it can't stand in for "no date" — that
// bucket only means the operator never set a specific due day).
function smartBucket(t: TaskView, thisWeek: Set<string>): "overdue" | "today" | "week" | "later" | "none" {
  if (!t.dueDate) return "none";
  if (isOverdue(t.dueDate, t.status)) return "overdue";
  if (t.dueDate === today()) return "today";
  if (thisWeek.has(t.dueDate)) return "week";
  return "later";
}

function groupDefs(groupBy: GroupBy, thisWeek: Set<string>): GroupDef[] {
  if (groupBy === "smart") {
    return [
      { key: "overdue", label: "Overdue", dot: "var(--overdue)", labelColor: "var(--overdue)", test: (t) => smartBucket(t, thisWeek) === "overdue" },
      { key: "today", label: "Today", dot: "var(--accent)", labelColor: "var(--text-muted)", test: (t) => smartBucket(t, thisWeek) === "today" },
      { key: "week", label: "This week", dot: "var(--text-faint)", labelColor: "var(--text-muted)", test: (t) => smartBucket(t, thisWeek) === "week" },
      { key: "later", label: "Later", dot: "var(--text-faint)", labelColor: "var(--text-muted)", test: (t) => smartBucket(t, thisWeek) === "later" },
      { key: "none", label: "No date", dot: "var(--border-strong)", labelColor: "var(--text-faint)", test: (t) => smartBucket(t, thisWeek) === "none" },
    ];
  }
  if (groupBy === "priority") {
    return [
      { key: "high", label: "High", dot: "var(--overdue)", labelColor: "var(--text-muted)", test: (t) => t.priority === "high" },
      { key: "medium", label: "Medium", dot: "var(--warn)", labelColor: "var(--text-muted)", test: (t) => t.priority === "medium" },
      { key: "low", label: "Low", dot: "var(--accent)", labelColor: "var(--text-muted)", test: (t) => t.priority === "low" },
      { key: "none", label: "No priority", dot: "var(--border-strong)", labelColor: "var(--text-faint)", test: (t) => !t.priority },
    ];
  }
  return [
    { key: "daily", label: "Daily", dot: "var(--accent)", labelColor: "var(--text-muted)", test: (t) => t.horizon === "daily" },
    { key: "weekly", label: "Weekly", dot: "var(--accent)", labelColor: "var(--text-muted)", test: (t) => t.horizon === "weekly" },
    { key: "monthly", label: "Monthly", dot: "var(--accent)", labelColor: "var(--text-muted)", test: (t) => t.horizon === "monthly" },
  ];
}

const PRIO_RANK: Record<Priority, number> = { high: 0, medium: 1, low: 2, "": 3 };

// No-date tasks sort last within due-date ordering.
function compareDueDate(a: TaskView, b: TaskView): number {
  const ad = a.dueDate;
  const bd = b.dueDate;
  if (ad && bd) return ad < bd ? -1 : ad > bd ? 1 : 0;
  if (ad && !bd) return -1;
  if (!ad && bd) return 1;
  return 0;
}

function sortTasks(tasks: TaskView[], sortBy: SortBy): TaskView[] {
  const arr = [...tasks];
  if (sortBy === "due") {
    arr.sort((a, b) => compareDueDate(a, b) || PRIO_RANK[a.priority] - PRIO_RANK[b.priority]);
  } else if (sortBy === "priority") {
    arr.sort((a, b) => PRIO_RANK[a.priority] - PRIO_RANK[b.priority] || compareDueDate(a, b));
  } else {
    arr.sort((a, b) => a.title.localeCompare(b.title));
  }
  return arr;
}

export default function AllTasksView() {
  const [tasks, setTasks] = useState<TaskView[] | null>(null);
  const [teams, setTeams] = useState<Team[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [groupBy, setGroupBy] = useState<GroupBy>("smart");
  const [sortBy, setSortBy] = useState<SortBy>("due");

  function reload() {
    setError(null);
    api
      .search({ status: "open" })
      .then((r) => setTasks(r.tasks))
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(reload, []);
  useEffect(() => {
    api.listTeams().then(setTeams).catch(() => {});
  }, []);

  function handleChanged() {
    reload();
    notifyTasksChanged();
  }

  const teamNames = useMemo(() => {
    const m = new Map<string, string>();
    for (const t of teams) m.set(t.id, t.name);
    return m;
  }, [teams]);

  // Computed once per mount — good enough for a single sitting; the app has
  // no live midnight-rollover requirement elsewhere either.
  const thisWeek = useMemo(() => new Set(weekDates(currentWeek())), []);

  const groups = useMemo(() => {
    if (!tasks) return [];
    return groupDefs(groupBy, thisWeek)
      .map((d) => {
        const filtered = tasks.filter(d.test);
        return { key: d.key, label: d.label, dot: d.dot, labelColor: d.labelColor, count: filtered.length, tasks: sortTasks(filtered, sortBy) };
      })
      .filter((g) => g.count > 0);
  }, [tasks, groupBy, sortBy, thisWeek]);

  const total = tasks?.length ?? 0;

  return (
    <div className="all-tasks-view">
      {error && <div className="error">{error}</div>}

      <div className="all-tasks-toolbar">
        <div className="all-tasks-toolbar-left">
          <span className="all-tasks-toolbar-label">Group by</span>
          <div className="segmented" role="tablist" aria-label="Group by">
            {GROUP_BY_OPTIONS.map((o) => (
              <button
                key={o.value}
                type="button"
                role="tab"
                aria-selected={groupBy === o.value}
                className={groupBy === o.value ? "active" : ""}
                onClick={() => setGroupBy(o.value)}
              >
                {o.label}
              </button>
            ))}
          </div>
        </div>
        <div className="all-tasks-toolbar-right">
          <label className="all-tasks-sort">
            <span className="all-tasks-toolbar-label">Sort</span>
            <select value={sortBy} onChange={(e) => setSortBy(e.target.value as SortBy)}>
              <option value="due">Due date</option>
              <option value="priority">Priority</option>
              <option value="title">Title</option>
            </select>
          </label>
          <span className="all-tasks-count">{total} open</span>
        </div>
      </div>

      {tasks && tasks.length === 0 && (
        <div className="all-tasks-empty">
          <div className="all-tasks-empty-icon">
            <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 12 10 18 20 6" />
            </svg>
          </div>
          <div className="all-tasks-empty-title">You&rsquo;re all clear</div>
          <div className="all-tasks-empty-sub">
            No open tasks across any horizon. Add one from the field above, or enjoy the whitespace.
          </div>
        </div>
      )}

      {tasks && tasks.length > 0 && (
        <div className="all-tasks-groups">
          {groups.map((g) => (
            <div className="all-tasks-group" key={g.key}>
              <div className="all-tasks-group-header">
                <span className="all-tasks-group-dot" style={{ background: g.dot }} />
                <span className="all-tasks-group-label" style={{ color: g.labelColor }}>
                  {g.label}
                </span>
                <span className="all-tasks-group-count">{g.count}</span>
                <span className="all-tasks-group-rule" />
              </div>
              <div className="task-list">
                {g.tasks.map((t) => (
                  <TaskRow
                    key={t.id}
                    task={t}
                    teamName={t.teamId ? teamNames.get(t.teamId) : undefined}
                    onOpen={setSelectedId}
                    onChanged={handleChanged}
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {selectedId && <TaskDetail id={selectedId} onClose={() => setSelectedId(null)} onChanged={handleChanged} />}
    </div>
  );
}
