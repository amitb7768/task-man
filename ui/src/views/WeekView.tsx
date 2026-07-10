import { useEffect, useState } from "react";
import type { TaskView, WeekViewResponse } from "../api";
import { api, ApiError } from "../api";
import { weekDates, formatShortDayLabel } from "../period";
import TaskRow from "../components/TaskRow";
import TaskDetail from "../components/TaskDetail";
import ContextStrip from "../components/ContextStrip";
import CompletedFold, { isCompletedStatus } from "../components/CompletedFold";
import "../styles/planning.css";

function activeOnly(tasks: TaskView[]): TaskView[] {
  return tasks.filter((t) => !isCompletedStatus(t.status));
}
function completedOnly(tasks: TaskView[]): TaskView[] {
  return tasks.filter((t) => isCompletedStatus(t.status));
}

// "3 open · 1 done" — section count per the shell mock's primaryCount.
function countLabel(tasks: TaskView[]): string {
  const open = tasks.filter((t) => t.status !== "done" && t.status !== "cancelled").length;
  const done = tasks.filter((t) => t.status === "done").length;
  return done > 0 ? `${open} open · ${done} done` : `${open} open`;
}

// week/reloadToken are owned by the app shell now: the header's period-nav
// and quick-add (design handoff "02 App Shell") drive them from above.
interface WeekViewProps {
  week: string;
  reloadToken: number;
}

export default function WeekView({ week, reloadToken }: WeekViewProps) {
  const [data, setData] = useState<WeekViewResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  function reload() {
    setError(null);
    api
      .weekView(week)
      .then(setData)
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  useEffect(reload, [week, reloadToken]);

  // "Active only" per day drives which day sections render at all — a day
  // whose tasks are all completed shouldn't leave a header with an empty
  // body (its tasks still land in the view-wide CompletedFold below).
  const days = data ? weekDates(week).filter((d) => activeOnly(data.days[d] ?? []).length > 0) : [];

  // ALL completed across the whole view collapse into ONE fold at the
  // bottom (flat pile — sub-period grouping isn't preserved inside it).
  const allCompleted = data
    ? [...completedOnly(data.tasks), ...weekDates(week).flatMap((d) => completedOnly(data.days[d] ?? []))]
    : [];

  return (
    <div className="pl-view">
      {error && <div className="error">{error}</div>}

      {data && (
        <section className="pl-section">
          <div className="pl-section-head">
            <span className="pl-dot accent" aria-hidden="true" />
            <span className="pl-label">Weekly tasks</span>
            <span className="pl-count">{countLabel(data.tasks)}</span>
            <span className="pl-rule" aria-hidden="true" />
          </div>
          {activeOnly(data.tasks).length === 0 ? (
            <div className="pl-empty">No weekly tasks.</div>
          ) : (
            <div className="task-list">
              {activeOnly(data.tasks).map((t) => (
                <TaskRow key={t.id} task={t} onOpen={setSelectedId} onChanged={reload} />
              ))}
            </div>
          )}
        </section>
      )}

      {/* Per-day subsections (mono day labels); empty (no active tasks) days are omitted. */}
      {data &&
        (days.length === 0 ? (
          <div className="pl-empty">No day-anchored tasks this week.</div>
        ) : (
          <div className="pl-subsections">
            {days.map((date) => {
              const dayTasks = activeOnly(data.days[date] ?? []);
              return (
                <section className="pl-section" key={date}>
                  <div className="pl-section-head">
                    <span className="pl-dot" aria-hidden="true" />
                    <span className="pl-label">{formatShortDayLabel(date)}</span>
                    <span className="pl-count">{dayTasks.length}</span>
                    <span className="pl-rule" aria-hidden="true" />
                  </div>
                  <div className="task-list">
                    {dayTasks.map((t) => (
                      <TaskRow key={t.id} task={t} onOpen={setSelectedId} onChanged={reload} />
                    ))}
                  </div>
                </section>
              );
            })}
          </div>
        ))}

      {data && (
        <ContextStrip
          label="This month"
          tasks={activeOnly(data.monthContext)}
          onOpen={setSelectedId}
          onChanged={reload}
        />
      )}

      {data && <CompletedFold tasks={allCompleted} onOpen={setSelectedId} onChanged={reload} />}

      {selectedId && (
        <TaskDetail id={selectedId} onClose={() => setSelectedId(null)} onChanged={reload} />
      )}
    </div>
  );
}
