import { useEffect, useState } from "react";
import type { MonthViewResponse, TaskView } from "../api";
import { api, ApiError } from "../api";
import { formatWeekLabel } from "../period";
import TaskRow from "../components/TaskRow";
import TaskDetail from "../components/TaskDetail";
import CompletedFold, { isCompletedStatus } from "../components/CompletedFold";
import "../styles/planning.css";

function activeOnly(tasks: TaskView[]): TaskView[] {
  return tasks.filter((t) => !isCompletedStatus(t.status));
}
function completedOnly(tasks: TaskView[]): TaskView[] {
  return tasks.filter((t) => isCompletedStatus(t.status));
}

// "4 open · 1 done" — section count per the shell mock's primaryCount.
function countLabel(tasks: TaskView[]): string {
  const open = tasks.filter((t) => t.status !== "done" && t.status !== "cancelled").length;
  const done = tasks.filter((t) => t.status === "done").length;
  return done > 0 ? `${open} open · ${done} done` : `${open} open`;
}

// month/reloadToken are owned by the app shell now: the header's period-nav
// and quick-add (design handoff "02 App Shell") drive them from above.
interface MonthViewProps {
  month: string;
  reloadToken: number;
}

export default function MonthView({ month, reloadToken }: MonthViewProps) {
  const [data, setData] = useState<MonthViewResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  function reload() {
    setError(null);
    api
      .monthView(month)
      .then(setData)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(reload, [month, reloadToken]);

  // "Active only" per week drives which week sections render at all — a
  // week whose tasks are all completed shouldn't leave a header with an
  // empty body (its tasks still land in the view-wide CompletedFold below).
  const weekKeys = data
    ? Object.keys(data.weeks)
        .sort()
        .filter((k) => activeOnly(data.weeks[k]).length > 0)
    : [];

  // ALL completed across the whole view collapse into ONE fold at the
  // bottom (flat pile — sub-period grouping isn't preserved inside it).
  const allCompleted = data
    ? [...completedOnly(data.tasks), ...Object.keys(data.weeks).flatMap((k) => completedOnly(data.weeks[k]))]
    : [];

  return (
    <div className="pl-view">
      {error && <div className="error">{error}</div>}

      {data && (
        <section className="pl-section">
          <div className="pl-section-head">
            <span className="pl-dot accent" aria-hidden="true" />
            <span className="pl-label">Monthly goals</span>
            <span className="pl-count">{countLabel(data.tasks)}</span>
            <span className="pl-rule" aria-hidden="true" />
          </div>
          {activeOnly(data.tasks).length === 0 ? (
            <div className="pl-empty">No monthly tasks.</div>
          ) : (
            <div className="task-list">
              {activeOnly(data.tasks).map((t) => (
                <TaskRow key={t.id} task={t} onOpen={setSelectedId} onChanged={reload} />
              ))}
            </div>
          )}
        </section>
      )}

      {/* Per-week subsections (mono week labels); empty (no active tasks) weeks are omitted. */}
      {data &&
        (weekKeys.length === 0 ? (
          <div className="pl-empty">No week-anchored tasks this month.</div>
        ) : (
          <div className="pl-subsections">
            {weekKeys.map((weekKey) => {
              const weekTasks = activeOnly(data.weeks[weekKey]);
              return (
                <section className="pl-section" key={weekKey}>
                  <div className="pl-section-head">
                    <span className="pl-dot" aria-hidden="true" />
                    <span className="pl-label">{formatWeekLabel(weekKey)}</span>
                    <span className="pl-count">{weekTasks.length}</span>
                    <span className="pl-rule" aria-hidden="true" />
                  </div>
                  <div className="task-list">
                    {weekTasks.map((t) => (
                      <TaskRow key={t.id} task={t} onOpen={setSelectedId} onChanged={reload} />
                    ))}
                  </div>
                </section>
              );
            })}
          </div>
        ))}

      {data && <CompletedFold tasks={allCompleted} onOpen={setSelectedId} onChanged={reload} />}

      {selectedId && (
        <TaskDetail id={selectedId} onClose={() => setSelectedId(null)} onChanged={reload} />
      )}
    </div>
  );
}
