// 20px rounded-square status control. Cycles todo → in_progress → done →
// cancelled → todo on click. Wire value stays "in_progress"; "in-progress"
// is a display-only label (per docs/DESIGN_V2_UI.md).
import type { Status } from "../api";

const CYCLE: Record<Status, Status> = {
  todo: "in_progress",
  in_progress: "done",
  done: "cancelled",
  cancelled: "todo",
};

interface StatusInfo {
  border: string;
  fill: string;
  glyph: string;
  glyphColor: string;
  hint: string;
}

const INFO: Record<Status, StatusInfo> = {
  todo: {
    border: "var(--border-strong)",
    fill: "transparent",
    glyph: "",
    glyphColor: "transparent",
    hint: "To do — click to start",
  },
  in_progress: {
    border: "var(--warn)",
    fill: "var(--warn-soft)",
    glyph: "◐",
    glyphColor: "var(--warn)",
    hint: "In progress — click to complete",
  },
  done: {
    border: "var(--done)",
    fill: "var(--done)",
    glyph: "✓",
    glyphColor: "var(--accent-text)",
    hint: "Done — click to cancel",
  },
  cancelled: {
    border: "var(--border-strong)",
    fill: "var(--surface-2)",
    glyph: "✕",
    glyphColor: "var(--text-faint)",
    hint: "Cancelled — click to reset",
  },
};

export interface StatusControlProps {
  status: Status;
  onChange: (next: Status) => void;
  disabled?: boolean;
}

export default function StatusControl({ status, onChange, disabled }: StatusControlProps) {
  const info = INFO[status] ?? INFO.todo;
  return (
    <button
      type="button"
      className="status-control"
      title={info.hint}
      disabled={disabled}
      onClick={(e) => {
        e.stopPropagation();
        onChange(CYCLE[status] ?? "todo");
      }}
      style={{
        borderColor: info.border,
        background: info.fill,
        color: info.glyphColor,
      }}
    >
      <span className="status-control-glyph" aria-hidden="true">
        {info.glyph}
      </span>
    </button>
  );
}
