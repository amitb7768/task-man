// History — read-only, paginated feed of a team's completed/cancelled tasks
// from past weeks (docs/DESIGN_V6_WEEK_ROLLOVER.md "History"; design handoff
// "14 History"). Entered via the completed fold's "View older →" footer
// link. Owns its own header (back-to-board + serif title + mono meta) and a
// flat newest-first list — createdAt desc per the contract, NOT regrouped by
// week; quiet mono separators just mark where the current run's weekOf
// changes, so "newest first" always holds even if some week's tasks aren't
// perfectly contiguous. Rows are read-only: no checkboxes, no hover actions
// — click opens the existing TaskDetail slide-over. "Load more" is
// offset/limit pagination (first pagination in the codebase).
import { useEffect, useState } from "react";
import type { Member, TaskView } from "../api";
import { api, ApiError } from "../api";
import { formatCompactDate, formatWeekRangeUpper } from "../period";
import { notifyTasksChanged } from "../App";
import TaskDetail from "../components/TaskDetail";
import "../styles/team-history.css";

const PAGE = 50;
const MAX_LIMIT = 200; // mirrors the server's documented cap

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}
// Deterministic per-member avatar tone — duplicated per-file convention
// (ui/CLAUDE.md), identical copy to TeamPage.tsx/TeamRollover.tsx/TaskRow.tsx.
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

export default function TeamHistory({
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
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);

  function fetchPage(offset: number, limit: number, replace: boolean) {
    setError(null);
    api
      .teamHistory(teamId, offset, limit)
      .then((d) => {
        setTasks((prev) => (replace || !prev ? d.tasks : [...prev, ...d.tasks]));
        setHasMore(d.hasMore);
      })
      .catch((e) => setError(errMsg(e)))
      .finally(() => setLoadingMore(false));
  }

  useEffect(() => {
    setTasks(null);
    setHasMore(false);
    fetchPage(0, PAGE, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [teamId]);

  function loadMore() {
    if (loadingMore || !tasks) return;
    setLoadingMore(true);
    fetchPage(tasks.length, PAGE, false);
  }

  // Re-fetch from the top, preserving however many pages were already
  // loaded (capped at the server's max limit), so closing the detail panel
  // after an edit doesn't collapse pagination back to page 1.
  function reload() {
    const limit = Math.min(MAX_LIMIT, Math.max(PAGE, tasks?.length ?? 0));
    fetchPage(0, limit, true);
  }

  function handleDetailChanged() {
    reload();
    notifyTasksChanged();
  }

  function avatarFor(assigneeId?: string): { initials: string; bg: string; fg: string; title: string } {
    if (assigneeId) {
      const m = memberById.get(assigneeId);
      if (m) return { initials: initials(m.name), ...AVATAR_TONES[toneIndex(m.id)], title: m.name };
    }
    return { initials: "—", ...UNASSIGNED_TONE, title: "Unassigned" };
  }

  // Groups are contiguous runs sharing the same weekOf, in the list's own
  // createdAt-desc order — a separator marks each run boundary rather than
  // reordering by week, so "newest first" (the contract's sort) always
  // holds even on the rare cross-week interleave.
  const groups: { key: string; label: string; rows: TaskView[] }[] = [];
  (tasks ?? []).forEach((t, i) => {
    const w = t.weekOf ?? "";
    const prev = groups[groups.length - 1];
    if (!prev || prev.key.split("#")[0] !== w) {
      groups.push({ key: `${w}#${i}`, label: w ? `${w} · ${formatWeekRangeUpper(w)}` : "Undated", rows: [] });
    }
    groups[groups.length - 1].rows.push(t);
  });

  const hasItems = (tasks?.length ?? 0) > 0;

  return (
    <div className="hi-page">
      <header className="hi-header">
        <button type="button" className="hi-back" onClick={onBack}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="15 5 8 12 15 19" />
          </svg>
          Board
        </button>
        <div className="hi-header-row">
          <h1 className="hi-title">History</h1>
          <span className="hi-team-name">{teamName}</span>
          <span className="hi-meta">completed &amp; cancelled · past weeks</span>
        </div>
      </header>

      {error && (
        <div className="error" style={{ margin: "0 32px 12px" }}>
          {error}
        </div>
      )}

      <div className="hi-body">
        <div className="hi-body-inner">
          {hasItems &&
            groups.map((g) => (
              <div className="hi-group" key={g.key}>
                <div className="hi-group-head">
                  <span className="hi-group-label">{g.label}</span>
                  <span className="hi-rule" aria-hidden="true" />
                </div>
                <div className="hi-rows">
                  {g.rows.map((t) => {
                    const done = t.status === "done";
                    const av = avatarFor(t.assigneeId);
                    const chip = done
                      ? `Done${t.completedAt ? ` · ${formatCompactDate(t.completedAt.slice(0, 10))}` : ""}`
                      : "Cancelled";
                    return (
                      <div key={t.id} className="hi-row" onClick={() => setSelectedTaskId(t.id)}>
                        <span
                          className="hi-status"
                          style={{
                            borderColor: done ? "var(--done)" : "var(--border-strong)",
                            background: done ? "var(--done)" : "var(--surface-2)",
                            color: done ? "var(--accent-text)" : "var(--text-faint)",
                          }}
                        >
                          {done ? "✓" : "✕"}
                        </span>
                        <span className="hi-tick" style={{ background: PRIORITY_COLOR[t.priority] }} />
                        <div className="hi-row-title">{t.title}</div>
                        <span className="hi-avatar" title={av.title} style={{ background: av.bg, color: av.fg }}>
                          {av.initials}
                        </span>
                        <span className={`hi-chip${done ? " done" : " cancelled"}`}>{chip}</span>
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}

          {hasItems && (
            <div className="hi-pager">
              {hasMore ? (
                <button type="button" className="hi-load-more" disabled={loadingMore} onClick={loadMore}>
                  {loadingMore ? "Loading…" : "Load more"}
                </button>
              ) : (
                <div className="hi-exhausted">
                  <span className="hi-exhausted-rule" aria-hidden="true" />
                  <span className="hi-exhausted-label">That&rsquo;s everything</span>
                  <span className="hi-exhausted-rule" aria-hidden="true" />
                </div>
              )}
            </div>
          )}

          {tasks && !hasItems && (
            <div className="hi-empty">
              <div className="hi-empty-icon">
                <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="12" cy="12" r="9" />
                  <polyline points="12 7 12 12 15 14" />
                </svg>
              </div>
              <div className="hi-empty-title">No history yet</div>
              <div className="hi-empty-sub">Completed and cancelled tasks will collect here as the team closes work.</div>
            </div>
          )}
        </div>
      </div>

      {selectedTaskId && (
        <TaskDetail id={selectedTaskId} onClose={() => setSelectedTaskId(null)} onChanged={handleDetailChanged} />
      )}
    </div>
  );
}
