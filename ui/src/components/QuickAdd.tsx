// Primary task-creation path (design handoff "08 Quick-add"). A 56px field
// with a leading horizon+period badge, live inline token parsing, a
// "Will create" preview, and a clickable legend that inserts tokens.
//
// Embedded compactly (340px wide) in the app-shell header; the preview/legend
// render as an absolutely-positioned panel below the field so they never
// change the header's height.
import { useMemo, useRef, useState } from "react";
import type { Horizon, Priority, Recurrence } from "../api";
import { parseQuickAdd } from "./quickAddParser";

export interface QuickAddResult {
  title: string;
  priority?: Priority;
  dueDate?: string;
  assigneeName?: string;
  recurrence?: Recurrence;
  /** Effective horizon: the view's default, or the parsed #override. */
  horizon: Horizon;
}

export interface QuickAddProps {
  /** Default horizon for the current view (used unless a #override token is typed). */
  horizon: Horizon;
  /** "Daily" | "Weekly" | "Monthly" */
  horizonLabel: string;
  /** Mono period text next to the badge, e.g. "Today · Jul 8", "W28", "July 2026". */
  periodLabel: string;
  placeholder: string;
  onCreate: (result: QuickAddResult) => void | Promise<void>;
}

const HORIZON_LABEL: Record<Horizon, string> = { daily: "Daily", weekly: "Weekly", monthly: "Monthly" };

const LEGEND: { token: string; desc: string; kind: "prio" | "due" | "assignee" | "recur" | "horizon" }[] = [
  { token: "!high", desc: "priority", kind: "prio" },
  { token: "^fri", desc: "due date", kind: "due" },
  { token: "@Sam", desc: "assignee", kind: "assignee" },
  { token: "*weekly", desc: "repeat", kind: "recur" },
  { token: "#monthly", desc: "horizon", kind: "horizon" },
];

const TOK_COLOR: Record<(typeof LEGEND)[number]["kind"], string> = {
  prio: "var(--overdue)",
  due: "var(--text-muted)",
  assignee: "var(--accent)",
  recur: "var(--text-muted)",
  horizon: "var(--accent)",
};

export default function QuickAdd({ horizon, horizonLabel, periodLabel, placeholder, onCreate }: QuickAddProps) {
  const [text, setText] = useState("");
  const [focused, setFocused] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const parsed = useMemo(() => parseQuickAdd(text), [text]);
  const effHorizon = parsed.horizon ?? horizon;
  const hasParse = text.trim().length > 0;

  async function submit() {
    const p = parseQuickAdd(text);
    if (!p.title) return;
    setText("");
    await onCreate({
      title: p.title,
      priority: p.priority,
      dueDate: p.dueDate,
      assigneeName: p.assigneeName,
      recurrence: p.recurrence,
      horizon: p.horizon ?? horizon,
    });
    inputRef.current?.focus();
  }

  function insert(token: string) {
    setText((t) => (t.replace(/\s+$/, "") + " " + token + " ").replace(/^\s+/, ""));
    inputRef.current?.focus();
  }

  const chips: { label: string; className: string }[] = [];
  if (parsed.priority) chips.push({ label: `! ${parsed.priority}`, className: `qa-chip prio-${parsed.priority}` });
  if (parsed.dueLabel) chips.push({ label: `^ ${parsed.dueLabel}`, className: "qa-chip" });
  if (parsed.assigneeName) chips.push({ label: `@ ${parsed.assigneeName}`, className: "qa-chip accent" });
  if (parsed.recurrence) chips.push({ label: `↻ ${parsed.recurrence.freq}`, className: "qa-chip" });
  if (parsed.horizon) chips.push({ label: `# ${parsed.horizon}`, className: "qa-chip accent-strong" });

  const showPanel = focused || hasParse;

  return (
    <div className={`quick-add${text ? " has-text" : ""}`}>
      <div className="quick-add-field">
        <span className={`quick-add-badge${parsed.horizon ? " overridden" : ""}`}>
          <span className="quick-add-badge-dot" aria-hidden="true" />
          {HORIZON_LABEL[effHorizon] ?? horizonLabel}
        </span>
        <span className="quick-add-period">{periodLabel}</span>
        <input
          ref={inputRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              submit();
            } else if (e.key === "Escape") {
              setText("");
            }
          }}
          placeholder={placeholder}
        />
        <kbd>⏎</kbd>
      </div>

      {showPanel && (
        <div className="quick-add-panel">
          {hasParse && (
            <div className="quick-add-preview">
              <span className="quick-add-preview-label">Will create</span>
              <span className="quick-add-preview-title">{parsed.title || "…"}</span>
              {chips.map((c, i) => (
                <span key={i} className={c.className}>
                  {c.label}
                </span>
              ))}
            </div>
          )}
          <div className="quick-add-legend">
            {LEGEND.map((l) => (
              <button
                type="button"
                key={l.token}
                title="Click to insert"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => insert(l.token)}
              >
                <span className="tok" style={{ color: TOK_COLOR[l.kind] }}>
                  {l.token}
                </span>
                <span className="desc">{l.desc}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
