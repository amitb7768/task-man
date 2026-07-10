// Collapsible "· for context" strip (design handoff "02 App Shell"): a mono
// uppercase toggle with a rotating ▸ arrow, then a hairline-indented TaskRow
// list. Rendered below the primary list — Day view shows "This week",
// Week view shows "This month".
import { useState } from "react";
import type { TaskView } from "../api";
import TaskRow from "./TaskRow";
import "../styles/planning.css";

interface ContextStripProps {
  /** e.g. "This week" — rendered as "This week · for context". */
  label: string;
  tasks: TaskView[];
  onOpen: (id: string) => void;
  onChanged: () => void;
}

export default function ContextStrip({ label, tasks, onOpen, onChanged }: ContextStripProps) {
  const [open, setOpen] = useState(true);
  if (tasks.length === 0) return null;
  return (
    <div className="context-strip">
      <button type="button" className="context-strip-toggle" onClick={() => setOpen((o) => !o)}>
        <span className={`context-strip-arrow${open ? " open" : ""}`} aria-hidden="true">
          ▸
        </span>
        <span className="context-strip-label">{label} · for context</span>
        <span className="context-strip-count">{tasks.length}</span>
      </button>
      {open && (
        <div className="context-strip-body">
          <div className="task-list">
            {tasks.map((t) => (
              <TaskRow key={t.id} task={t} onOpen={onOpen} onChanged={onChanged} />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
