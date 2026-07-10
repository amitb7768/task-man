// Shared "collapse completed in place" disclosure (docs/DESIGN_V5_COMPLETED_FOLD.md).
// Tucks done/cancelled tasks out of the active planning/team-board lists behind
// a one-click mono disclosure that matches the existing section-header anatomy
// (see ContextStrip / .pl-section-head). Renders nothing when there's nothing
// completed. Default render is a plain TaskRow list (keeps the dim + strike
// treatment); callers that need their own row chrome (e.g. TeamPage's
// checkbox-wrapped Row, for bulk-select) pass `render`.
import { Fragment, useState } from "react";
import type { ReactNode } from "react";
import type { Status, TaskView } from "../api";
import TaskRow from "./TaskRow";
import "../styles/completed-fold.css";

/** Active = todo | in_progress. Completed = done | cancelled. Single source
 * of truth for the split — every view partitions its task lists through this. */
export function isCompletedStatus(status: Status): boolean {
  return status === "done" || status === "cancelled";
}

export interface CompletedFoldProps {
  tasks: TaskView[];
  onOpen: (id: string) => void;
  onChanged: () => void;
  /** Optional custom row renderer (e.g. TeamPage's bulk-select-aware Row). */
  render?: (task: TaskView) => ReactNode;
}

export default function CompletedFold({ tasks, onOpen, onChanged, render }: CompletedFoldProps) {
  const [open, setOpen] = useState(false);

  if (tasks.length === 0) return null;

  return (
    <div className="completed-fold">
      <button
        type="button"
        className="completed-fold-toggle"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <span className="completed-fold-label">{tasks.length} completed</span>
        <span className={`completed-fold-arrow${open ? " open" : ""}`} aria-hidden="true">
          ▸
        </span>
      </button>
      {open && (
        <div className="completed-fold-body">
          {render ? (
            tasks.map((t) => <Fragment key={t.id}>{render(t)}</Fragment>)
          ) : (
            <div className="task-list">
              {tasks.map((t) => (
                <TaskRow key={t.id} task={t} onOpen={onOpen} onChanged={onChanged} />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
