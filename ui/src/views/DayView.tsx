import { useEffect, useState } from "react";
import type { DayViewResponse, TaskView } from "../api";
import { api, ApiError } from "../api";
import { formatCompactDate, today } from "../period";
import TaskRow from "../components/TaskRow";
import TaskDetail from "../components/TaskDetail";
import ContextStrip from "../components/ContextStrip";
import CompletedFold, { isCompletedStatus } from "../components/CompletedFold";
import "../styles/planning.css";

// date/reloadToken are owned by the app shell now: the header's period-nav
// and quick-add (design handoff "02 App Shell") drive them from above.
interface DayViewProps {
  date: string;
  reloadToken: number;
}

// "5 open · 1 done" — section count per the shell mock's primaryCount.
function countLabel(tasks: TaskView[]): string {
  const open = tasks.filter((t) => t.status !== "done" && t.status !== "cancelled").length;
  const done = tasks.filter((t) => t.status === "done").length;
  return done > 0 ? `${open} open · ${done} done` : `${open} open`;
}

export default function DayView({ date, reloadToken }: DayViewProps) {
  const [data, setData] = useState<DayViewResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  function reload() {
    setError(null);
    api
      .dayView(date)
      .then(setData)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(reload, [date, reloadToken]);

  const label = date === today() ? "Today's tasks" : `Tasks for ${formatCompactDate(date)}`;

  const activeTasks = data ? data.tasks.filter((t) => !isCompletedStatus(t.status)) : [];
  const completedTasks = data ? data.tasks.filter((t) => isCompletedStatus(t.status)) : [];

  return (
    <div className="pl-view">
      {error && <div className="error">{error}</div>}

      {data && (
        <section className="pl-section">
          <div className="pl-section-head">
            <span className="pl-dot accent" aria-hidden="true" />
            <span className="pl-label">{label}</span>
            <span className="pl-count">{countLabel(data.tasks)}</span>
            <span className="pl-rule" aria-hidden="true" />
          </div>
          {activeTasks.length === 0 ? (
            <div className="pl-empty">Nothing planned for this day.</div>
          ) : (
            <div className="task-list">
              {activeTasks.map((t) => (
                <TaskRow key={t.id} task={t} onOpen={setSelectedId} onChanged={reload} />
              ))}
            </div>
          )}
          <CompletedFold tasks={completedTasks} onOpen={setSelectedId} onChanged={reload} />
        </section>
      )}

      {data && (
        <ContextStrip
          label="This week"
          tasks={data.weekContext.filter((t) => !isCompletedStatus(t.status))}
          onOpen={setSelectedId}
          onChanged={reload}
        />
      )}

      {selectedId && (
        <TaskDetail id={selectedId} onClose={() => setSelectedId(null)} onChanged={reload} />
      )}
    </div>
  );
}
