// Date-range task summary slide-over (docs/DESIGN_V9_NOTES_SUMMARY.md "UI" —
// SummaryPanel.tsx). Built on the same .td-backdrop/.td-panel shell as
// TaskDetail (../styles/task-detail.css, imported transitively wherever
// TaskDetail is already mounted); own chrome lives in styles/summary-panel.css
// under a .sp-* prefix. Read-only — no task mutation happens from here.
import { useEffect, useRef, useState } from "react";
import type { SummaryResponse, SummaryScope, SummaryTask } from "../api";
import { api, ApiError } from "../api";
import { formatCompactDate } from "../period";
import { showToast } from "./Toast";
import { toCSV, toMarkdown } from "./summaryFormat";
import "../styles/summary-panel.css";

interface SummaryPanelProps {
  scope: SummaryScope;
  scopeLabel: string;
  from: string;
  to: string;
  onClose: () => void;
}

const STATUS_LABEL: Record<string, string> = {
  todo: "To do",
  in_progress: "in-progress",
  done: "Done",
  cancelled: "Cancelled",
};

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

export default function SummaryPanel({ scope, scopeLabel, from: initialFrom, to: initialTo, onClose }: SummaryPanelProps) {
  const [from, setFrom] = useState(initialFrom);
  const [to, setTo] = useState(initialTo);
  const [data, setData] = useState<SummaryResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [closing, setClosing] = useState(false);
  const panelRef = useRef<HTMLElement | null>(null);

  const rangeError = from !== "" && to !== "" && from > to;

  // Auto-fetch on open and on every from/to change; `from > to` short-
  // circuits to an inline error with no fetch (docs/DESIGN_V9_NOTES_SUMMARY.md
  // "SummaryPanel.tsx"). scope is a fresh object literal from the mount
  // point every render — depend on its two primitive fields instead so this
  // effect doesn't refire on every unrelated parent re-render.
  useEffect(() => {
    if (rangeError || !from || !to) {
      setLoading(false);
      setError(null);
      setData(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .summary(from, to, scope)
      .then((d) => {
        if (cancelled) return;
        setData(d);
      })
      .catch((e) => {
        if (cancelled) return;
        setError(errMsg(e));
        setData(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [from, to, scope.teamId, scope.assigneeId]);

  // Focus the panel on mount so keyboard/AT users land inside the dialog
  // (fix #6).
  useEffect(() => {
    panelRef.current?.focus();
  }, []);

  function requestClose() {
    setClosing((already) => {
      if (already) return already;
      window.setTimeout(onClose, 260);
      return true;
    });
  }

  // Mirrors TaskDetail's Escape handling.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") requestClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function copyMarkdown() {
    if (!data) return;
    if (!navigator.clipboard) {
      showToast("Clipboard unavailable — use Download CSV");
      return;
    }
    try {
      await navigator.clipboard.writeText(toMarkdown(data, scopeLabel));
      showToast("Copied");
    } catch (e) {
      showToast(errMsg(e));
    }
  }

  // ponytail: blob downloads don't fire inside the Tauri webview —
  // Copy-as-Markdown is the Tauri path; documented, not fixed (contract
  // "Accepted consequences").
  function downloadCSV() {
    if (!data) return;
    const blob = new Blob([toCSV(data)], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `summary_${data.from}_${data.to}.csv`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  return (
    <>
      <div className={`td-backdrop${closing ? " closing" : ""}`} onClick={requestClose} />
      <aside
        ref={panelRef}
        tabIndex={-1}
        className={`td-panel${closing ? " closing" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-label="Summary"
      >
        <div className="td-header">
          <div className="sp-title-block">
            <span className="sp-title">Summary</span>
            <span className="sp-scope-label">{scopeLabel}</span>
          </div>
          <button type="button" className="td-icon-btn" title="Close" onClick={requestClose}>
            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round">
              <line x1="6" y1="6" x2="18" y2="18" />
              <line x1="18" y1="6" x2="6" y2="18" />
            </svg>
          </button>
        </div>

        <div className="sp-toolbar">
          <input
            type="date"
            className="sp-date-input"
            aria-label="From"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
          <span className="sp-range-sep">–</span>
          <input
            type="date"
            className="sp-date-input"
            aria-label="To"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
        </div>
        {rangeError && <div className="error sp-range-error">"From" must be on or before "to".</div>}

        <div className="td-scroll">
          {loading && (
            <div className="empty" style={{ padding: "22px 24px" }}>
              Loading…
            </div>
          )}
          {error && (
            <div className="error" style={{ padding: "22px 24px" }}>
              {error}
            </div>
          )}

          {data && !loading && !rangeError && (
            <>
              <SummarySection title="Completed" section="completed" tasks={data.completed} />
              <SummarySection title="Updated" section="updated" tasks={data.updated} />
              <SummarySection title="New" section="added" tasks={data.added} />
            </>
          )}
        </div>

        <div className="td-footer sp-footer">
          <button type="button" onClick={copyMarkdown} disabled={!data}>
            Copy as Markdown
          </button>
          <button type="button" onClick={downloadCSV} disabled={!data}>
            Download CSV
          </button>
        </div>
      </aside>
    </>
  );
}

function SummarySection({
  title,
  section,
  tasks,
}: {
  title: string;
  section: "completed" | "updated" | "added";
  tasks: SummaryTask[];
}) {
  return (
    <div className="sp-section">
      <div className="sp-section-head">
        <span className="sp-section-title">{title}</span>
        <span className="sp-section-count">{tasks.length}</span>
      </div>
      {tasks.length === 0 ? (
        <div className="empty sp-section-empty">Nothing in this range</div>
      ) : (
        <div className="sp-task-list">
          {tasks.map((t) => (
            <div className="sp-task" key={t.id}>
              <div className="sp-task-head">
                <span className="sp-task-title">{t.title}</span>
                <span className={`sp-pill sp-pill-${t.status}`}>{STATUS_LABEL[t.status] ?? t.status}</span>
              </div>
              <div className="sp-task-meta">
                {t.assigneeName && <span>{t.assigneeName}</span>}
                {t.dueDate && <span>Due {formatCompactDate(t.dueDate)}</span>}
                {t.overdue && <span className="sp-overdue">Overdue</span>}
                {section === "completed" && t.closedDate && <span>Closed {formatCompactDate(t.closedDate)}</span>}
              </div>
              {t.notes.length > 0 && (
                <div className="sp-notes">
                  {t.notes.map((n) => (
                    <div className={`sp-note${n.kind === "status" ? " sp-note-status" : ""}`} key={n.id}>
                      <span className="sp-note-date">{formatCompactDate(n.date)}</span>
                      <span className="sp-note-by">{n.byName || "—"}</span>
                      <span className="sp-note-text">{n.kind === "status" ? `${n.from} → ${n.to}` : n.text}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
