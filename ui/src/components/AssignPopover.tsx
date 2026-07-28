// Assign popover (docs/DESIGN_V7_BACKLOG.md "UI") — the only new component
// this feature introduces. Anchored under its trigger (row hover "Assign…"
// or the bulk bar's "Assign…"), same one-at-a-time absolute-panel mechanic as
// TaskComposer's pill popovers (components/TaskComposer.tsx): team select ->
// member select (with open-task workload counts, the bandwidth signal) ->
// optional due date -> Assign. This component only resolves the picks and
// emits them; the caller (views/BacklogView.tsx) performs the actual
// PATCH(es) and owns the undo toast, same division of labor as every other
// bulk action in this app (ui/CLAUDE.md "bulk+undo pattern").
//
// Every trigger button that opens this popover MUST carry the "assign-trigger"
// class so the outside-click handler below ignores mousedowns on it — the
// caller's own onClick already toggles open/closed, so re-clicking the SAME
// trigger must not also fire this component's onClose and fight the toggle
// (identical reasoning to TaskComposer's .composer-pill exemption).
import { useEffect, useRef, useState } from "react";
import type { Team } from "../api";
import { api, ApiError } from "../api";
import { isCompletedStatus } from "./CompletedFold";
import "../styles/assign-popover.css";

export interface AssignPopoverProps {
  onAssign: (input: { teamId: string; assigneeId?: string; dueDate?: string }) => void;
  onClose: () => void;
  /** Opens the panel above the trigger instead of below — for the bulk bar,
   *  which sits fixed near the bottom of the viewport. Default "bottom". */
  placement?: "top" | "bottom";
}

interface MemberOption {
  id: string;
  name: string;
  open: number;
}

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

export default function AssignPopover({ onAssign, onClose, placement = "bottom" }: AssignPopoverProps) {
  const [teams, setTeams] = useState<Team[]>([]);
  const [teamId, setTeamId] = useState("");
  const [members, setMembers] = useState<MemberOption[]>([]);
  const [assigneeId, setAssigneeId] = useState("");
  const [dueDate, setDueDate] = useState("");
  const [loadingMembers, setLoadingMembers] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    api.listTeams().then(setTeams).catch((e) => setError(errMsg(e)));
  }, []);

  // Member workload counts — same derivation as TeamPage's avatar filter row
  // (views/TeamPage.tsx filterChips: `tasks.filter(isOpenTask).length`), just
  // sourced fresh per team-pick here rather than from an already-loaded board.
  useEffect(() => {
    if (!teamId) {
      setMembers([]);
      setAssigneeId("");
      return;
    }
    let cancelled = false;
    setLoadingMembers(true);
    setAssigneeId("");
    api
      .teamBoard(teamId)
      .then((b) => {
        if (cancelled) return;
        setMembers(
          b.members.map(({ member, tasks }) => ({
            id: member.id,
            name: member.name,
            open: tasks.filter((t) => !isCompletedStatus(t.status)).length,
          })),
        );
      })
      .catch((e) => {
        if (!cancelled) setError(errMsg(e));
      })
      .finally(() => {
        if (!cancelled) setLoadingMembers(false);
      });
    return () => {
      cancelled = true;
    };
  }, [teamId]);

  useEffect(() => {
    function onMouseDown(e: MouseEvent) {
      const target = e.target;
      if (!(target instanceof Element)) return;
      if (target.closest(".assign-trigger")) return;
      if (ref.current?.contains(target)) return;
      onClose();
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("mousedown", onMouseDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onMouseDown);
      document.removeEventListener("keydown", onKeyDown);
    };
    // onClose is a fresh inline closure from the caller every render;
    // depending only on mount keeps this listener from churning while open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className={`assign-popover assign-popover-${placement}`} ref={ref}>
      <label className="assign-popover-field">
        <span className="assign-popover-label">Team</span>
        <select value={teamId} onChange={(e) => setTeamId(e.target.value)}>
          <option value="">Select team…</option>
          {teams.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
      </label>

      <label className="assign-popover-field">
        <span className="assign-popover-label">Member</span>
        <select
          value={assigneeId}
          onChange={(e) => setAssigneeId(e.target.value)}
          disabled={!teamId || loadingMembers}
        >
          <option value="">Unassigned</option>
          {members.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name} · {m.open} open
            </option>
          ))}
        </select>
      </label>

      <label className="assign-popover-field">
        <span className="assign-popover-label">Due date</span>
        <input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} />
      </label>

      {error && <div className="error">{error}</div>}

      <button
        type="button"
        className="assign-popover-btn"
        disabled={!teamId}
        onClick={() => onAssign({ teamId, assigneeId: assigneeId || undefined, dueDate: dueDate || undefined })}
      >
        Assign
      </button>
    </div>
  );
}
