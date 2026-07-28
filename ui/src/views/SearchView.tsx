import { useEffect, useMemo, useState } from "react";
import type { Horizon, Priority, Status, Team, TaskView } from "../api";
import { api, ApiError } from "../api";
import TaskRow from "../components/TaskRow";
import TaskDetail from "../components/TaskDetail";
import "../styles/planning.css";

export default function SearchView() {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<Status | "">("");
  const [priority, setPriority] = useState<Priority | "">("");
  const [horizon, setHorizon] = useState<Horizon | "">("");
  const [teamId, setTeamId] = useState("");
  const [teams, setTeams] = useState<Team[]>([]);
  const [results, setResults] = useState<TaskView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  useEffect(() => {
    api.listTeams().then(setTeams).catch(() => {});
  }, []);

  const teamNames = useMemo(() => {
    const m = new Map<string, string>();
    for (const t of teams) m.set(t.id, t.name);
    return m;
  }, [teams]);

  function runSearch() {
    setError(null);
    api
      .search({
        q: q || undefined,
        status: status || undefined,
        priority: priority || undefined,
        horizon: horizon || undefined,
        teamId: teamId || undefined,
      })
      .then((r) => setResults(r.tasks))
      .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }

  // Re-run whenever any filter changes; debounce the free-text field only.
  useEffect(() => {
    const handle = setTimeout(runSearch, q ? 250 : 0);
    return () => clearTimeout(handle);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, status, priority, horizon, teamId]);

  return (
    <div className="pl-view">
      <div className="pl-toolbar">
        <input
          type="text"
          className="pl-toolbar-input"
          placeholder="Search title & notes…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") runSearch();
          }}
        />
        <select value={status} onChange={(e) => setStatus(e.target.value as Status | "")}>
          <option value="">Any status</option>
          <option value="todo">Todo</option>
          <option value="in_progress">In progress</option>
          <option value="done">Done</option>
          <option value="cancelled">Cancelled</option>
        </select>
        <select value={priority} onChange={(e) => setPriority(e.target.value as Priority | "")}>
          <option value="">Any priority</option>
          <option value="low">Low</option>
          <option value="medium">Medium</option>
          <option value="high">High</option>
        </select>
        <select value={horizon} onChange={(e) => setHorizon(e.target.value as Horizon | "")}>
          <option value="">Any horizon</option>
          <option value="daily">Daily</option>
          <option value="weekly">Weekly</option>
          <option value="monthly">Monthly</option>
          {/* Opt-in only (docs/DESIGN_V7_BACKLOG.md): default search excludes
              backlog tasks server-side; picking this explicit value is the
              only way to surface them here. */}
          <option value="backlog">Backlog</option>
        </select>
        <select value={teamId} onChange={(e) => setTeamId(e.target.value)}>
          <option value="">Any team</option>
          {teams.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
      </div>

      {error && <div className="error">{error}</div>}

      {results && (
        <section className="pl-section">
          <div className="pl-section-head">
            <span className="pl-label">Results</span>
            <span className="pl-count">
              {results.length} result{results.length === 1 ? "" : "s"}
            </span>
            <span className="pl-rule" aria-hidden="true" />
          </div>
          {results.length === 0 ? (
            <div className="pl-empty">No matching tasks.</div>
          ) : (
            <div className="task-list">
              {results.map((t) => (
                <TaskRow
                  key={t.id}
                  task={t}
                  teamName={t.teamId ? teamNames.get(t.teamId) : undefined}
                  onOpen={setSelectedId}
                  onChanged={runSearch}
                />
              ))}
            </div>
          )}
        </section>
      )}

      {selectedId && (
        <TaskDetail id={selectedId} onClose={() => setSelectedId(null)} onChanged={runSearch} />
      )}
    </div>
  );
}
