// Team page — members & tasks (design handoff "10 Team Page"). Renders its
// own in-content header (back affordance, serif team name, status count
// chips, Members button), an avatar filter row (single-select), a
// Group-by-member toggle + quick-add toolbar, a flat (due-sorted, overdue
// pinned) or grouped (per-member sections + always-last Unassigned) task
// list with hover/selection-revealed multi-select and a bulk action bar
// (reuses the shared TaskRow anatomy — never mutates the store outside of
// the api.ts helpers so undo snapshots stay accurate), and a 452px Members
// slide-in panel (add/edit/remove member, team rename, gated delete).
//
// See docs/DESIGN_V3_TEAMS.md for the three deliberate mock deviations this
// view implements (no header theme toggle; Delete team gated on ANY tasks;
// quick-add silently forces horizon=daily/period=today for every task
// created here) and the binding interaction contract (avatar filter scoping,
// bulk-undo semantics, members panel mechanics).
import { Fragment, useEffect, useMemo, useState } from "react";
import type { Member, Status, SystemRole, TaskView, TeamBoardResponse } from "../api";
import { api, ApiError } from "../api";
import { formatDayBadgePeriod, isOverdue, today } from "../period";
import { notifyTasksChanged } from "../App";
import { useAuth } from "../auth/AuthContext";
import TaskRow from "../components/TaskRow";
import TaskDetail from "../components/TaskDetail";
import QuickAdd from "../components/QuickAdd";
import type { QuickAddResult } from "../components/QuickAdd";
import TaskComposer from "../components/TaskComposer";
import { dismissToast, showToast } from "../components/Toast";
import CompletedFold, { isCompletedStatus } from "../components/CompletedFold";
import "../styles/team-page.css";

const UNASSIGNED = "__unassigned";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

function firstName(name: string): string {
  return name.split(/\s+/)[0] ?? name;
}

// Active = todo | in_progress (docs/DESIGN_V5_COMPLETED_FOLD.md); delegates
// to CompletedFold's canonical isCompletedStatus so the split stays in sync
// with the fold everywhere it's used on this page.
function isOpenTask(t: TaskView): boolean {
  return !isCompletedStatus(t.status);
}

// due-date sorted (no due date last, then createdAt), closed tasks always
// last — docs/DESIGN_V3_TEAMS.md
function compareDue(a: TaskView, b: TaskView): number {
  const aOpen = isOpenTask(a);
  const bOpen = isOpenTask(b);
  if (aOpen !== bOpen) return aOpen ? -1 : 1;
  const ad = a.dueDate;
  const bd = b.dueDate;
  if (ad && bd) return ad < bd ? -1 : ad > bd ? 1 : 0;
  if (ad && !bd) return -1;
  if (!ad && bd) return 1;
  return a.createdAt < b.createdAt ? -1 : a.createdAt > b.createdAt ? 1 : 0;
}

// Deterministic per-member tone (docs/DESIGN_V3_TEAMS.md "Avatar tones").
// Identical copy lives in views/TeamsLanding.tsx and components/TaskRow.tsx.
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

function CheckGlyph() {
  return (
    <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="4 12 10 18 20 6" />
    </svg>
  );
}

// ---------------------------------------------------------------------------
// Row: hover/selection-revealed checkbox wrapping the shared TaskRow.
// ---------------------------------------------------------------------------
function Row({
  task,
  checked,
  anySelected,
  onToggle,
  assigneeAvatar,
  onOpen,
  onChanged,
}: {
  task: TaskView;
  checked: boolean;
  anySelected: boolean;
  onToggle: () => void;
  assigneeAvatar?: { initials: string; tone: number };
  onOpen: (id: string) => void;
  onChanged: () => void;
}) {
  return (
    <div className={`tp-row${checked ? " tp-row-checked" : ""}${anySelected ? " tp-row-any" : ""}`}>
      <button type="button" className="tp-row-check" title="Select" onClick={(e) => { e.stopPropagation(); onToggle(); }}>
        {checked && <CheckGlyph />}
      </button>
      <div className="tp-row-body">
        <TaskRow task={task} onOpen={onOpen} onChanged={onChanged} assigneeAvatar={assigneeAvatar} noDateChip />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Grouped-mode member section: avatar + name + count + hairline, rows, and
// an inline "Add for ‹name›…" quick-add (bare icon + input via CSS).
// ---------------------------------------------------------------------------
function GroupSection({
  name,
  memberInitials,
  tone,
  tasks,
  addPlaceholder,
  onAdd,
  selected,
  anySelected,
  onToggle,
  avatarFor,
  onOpen,
  onChanged,
  disabled,
}: {
  name: string;
  memberInitials: string;
  tone: { bg: string; fg: string };
  tasks: TaskView[];
  addPlaceholder: string;
  onAdd: (result: QuickAddResult) => void | Promise<void>;
  selected: Set<string>;
  anySelected: boolean;
  onToggle: (id: string) => void;
  avatarFor: (assigneeId?: string) => { initials: string; tone: number } | undefined;
  onOpen: (id: string) => void;
  onChanged: () => void;
  /** Dims the whole section — the member's login is disabled (docs/AUTH_FEATURES.md "Disabled users dimmed on boards"). */
  disabled?: boolean;
}) {
  // Active-only in the section's main row list; that section's completed
  // pile tucks into its own CompletedFold at the bottom (one fold per
  // member — docs/DESIGN_V5_COMPLETED_FOLD.md). compareDue's closed-last
  // branch is inert here (activeTasks never contains a closed task) but is
  // left as-is since compareDue is shared with flat mode's sort too.
  const activeTasks = useMemo(() => tasks.filter((t) => !isCompletedStatus(t.status)).sort(compareDue), [tasks]);
  const completedTasks = useMemo(() => tasks.filter((t) => isCompletedStatus(t.status)), [tasks]);
  const openCount = tasks.filter(isOpenTask).length;

  function renderRow(t: TaskView) {
    return (
      <Row
        task={t}
        checked={selected.has(t.id)}
        anySelected={anySelected}
        onToggle={() => onToggle(t.id)}
        assigneeAvatar={avatarFor(t.assigneeId)}
        onOpen={onOpen}
        onChanged={onChanged}
      />
    );
  }

  return (
    <div className={`tp-group${disabled ? " tp-group-disabled" : ""}`}>
      <div className="tp-group-head">
        <span className="tp-group-avatar" style={{ background: tone.bg, color: tone.fg }}>
          {memberInitials}
        </span>
        <span className="tp-group-name">{name}</span>
        {disabled && <span className="tp-mp-disabled-tag">Disabled</span>}
        <span className="tp-group-count">{openCount}</span>
        <span className="tp-rule" aria-hidden="true" />
      </div>
      <div className="tp-rows">{activeTasks.map((t) => <Fragment key={t.id}>{renderRow(t)}</Fragment>)}</div>
      {activeTasks.length === 0 && completedTasks.length === 0 && <div className="tp-group-empty">No tasks</div>}
      <CompletedFold tasks={completedTasks} onOpen={onOpen} onChanged={onChanged} render={renderRow} />
      <div className="tp-group-add">
        <QuickAdd horizon="daily" horizonLabel="Daily" periodLabel={formatDayBadgePeriod(today())} placeholder={addPlaceholder} onCreate={onAdd} />
      </div>
    </div>
  );
}

export default function TeamPage({ teamId, onBack }: { teamId: string; onBack: () => void }) {
  const { user } = useAuth();
  // Teams/members management + task DELETE are ADMIN-only everywhere
  // (docs/AUTH_FEATURES.md "Endpoint x role matrix", decision #5) — USER gets
  // a read-only members roster and no delete affordances on this page. The
  // server enforces the actual boundary; this only hides the affordances.
  const isAdmin = user.systemRole === "ADMIN";
  const [board, setBoard] = useState<TeamBoardResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
  const [filterId, setFilterId] = useState<string | null>(null);
  const [grouped, setGrouped] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const [membersOpen, setMembersOpen] = useState(false);
  const [membersClosing, setMembersClosing] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState("");
  const [editEmail, setEditEmail] = useState("");
  const [editRole, setEditRole] = useState("");
  const [addName, setAddName] = useState("");
  const [addEmail, setAddEmail] = useState("");
  const [addRole, setAddRole] = useState("");
  const [renameDraft, setRenameDraft] = useState("");

  // ---- Admin user-management: enable-login / reset-password / disable
  // (docs/AUTH_FEATURES.md "UI changes" — "Admin: members panel gains Enable
  // login..., Reset password..., Disable toggle..., systemRole badge"). ----
  const [enablingId, setEnablingId] = useState<string | null>(null);
  const [enableRole, setEnableRole] = useState<SystemRole>("USER");
  const [credReveal, setCredReveal] = useState<{ memberId: string; password: string; kind: "enabled" | "reset" } | null>(null);
  const [copied, setCopied] = useState(false);
  const [memberBusy, setMemberBusy] = useState<string | null>(null);

  function reload() {
    setError(null);
    api
      .teamBoard(teamId)
      .then((b) => {
        setBoard(b);
        setSelected((prev) => {
          const ids = new Set([...b.members.flatMap((m) => m.tasks), ...b.unassigned].map((t) => t.id));
          return new Set([...prev].filter((id) => ids.has(id)));
        });
      })
      .catch((e) => {
        if (e instanceof ApiError && e.status === 404) {
          onBack();
          return;
        }
        setError(errMsg(e));
      });
  }

  useEffect(() => {
    reload();
    // reload() closes over the latest teamId/onBack via the component's own
    // render closure; depending only on teamId is deliberate — onBack is a
    // fresh inline closure from the parent every render, and depending on it
    // would refire this fetch on every unrelated parent re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [teamId]);

  useEffect(() => {
    if (board) setRenameDraft(board.team.name);
  }, [board]);

  // Reset view-local UI state (filter/grouping/selection/panels) whenever the
  // team itself changes, so switching teams never carries over stale state.
  // The cleanup also dismisses any lingering undo toast on teamId change or
  // unmount, so a stale undo never fires against a previously-viewed team.
  useEffect(() => {
    setFilterId(null);
    setGrouped(false);
    setSelected(new Set());
    setMembersOpen(false);
    setEditingId(null);
    return () => dismissToast();
  }, [teamId]);

  function handleChanged() {
    reload();
    notifyTasksChanged();
  }

  function fail(e: unknown) {
    setError(errMsg(e));
  }

  const memberById = useMemo(() => {
    const m = new Map<string, Member>();
    board?.members.forEach(({ member }) => m.set(member.id, member));
    return m;
  }, [board]);

  const taskById = useMemo(() => {
    const m = new Map<string, TaskView>();
    board?.members.forEach(({ tasks }) => tasks.forEach((t) => m.set(t.id, t)));
    board?.unassigned.forEach((t) => m.set(t.id, t));
    return m;
  }, [board]);

  const allTasks = useMemo(
    () => (board ? [...board.members.flatMap((m) => m.tasks), ...board.unassigned] : []),
    [board],
  );

  function avatarFor(assigneeId?: string): { initials: string; tone: number } | undefined {
    if (!assigneeId) return undefined;
    const m = memberById.get(assigneeId);
    if (!m) return undefined;
    return { initials: initials(m.name), tone: toneIndex(m.id) };
  }

  const visibleTasks = useMemo(
    () =>
      allTasks.filter((t) => {
        if (!filterId) return true;
        if (filterId === UNASSIGNED) return !t.assigneeId;
        return t.assigneeId === filterId;
      }),
    [allTasks, filterId],
  );

  // ---- Status chips: unfiltered by the avatar selection ----
  const countTodo = allTasks.filter((t) => t.status === "todo").length;
  const countProg = allTasks.filter((t) => t.status === "in_progress").length;
  const countDone = allTasks.filter((t) => t.status === "done").length;

  // ---- Avatar filter chips ----
  const filterChips = useMemo(() => {
    if (!board) return [];
    const chips = board.members.map(({ member, tasks }) => ({
      id: member.id,
      name: firstName(member.name),
      avatarInitials: initials(member.name),
      tone: AVATAR_TONES[toneIndex(member.id)],
      open: tasks.filter(isOpenTask).length,
      overdue: tasks.filter((t) => isOverdue(t.dueDate, t.status)).length,
      // Disabled users dimmed on boards (docs/AUTH_FEATURES.md "UI changes").
      disabled: !!member.disabled,
    }));
    chips.push({
      id: UNASSIGNED,
      name: "Unassigned",
      avatarInitials: "—",
      tone: UNASSIGNED_TONE,
      open: board.unassigned.filter(isOpenTask).length,
      overdue: board.unassigned.filter((t) => isOverdue(t.dueDate, t.status)).length,
      disabled: false,
    });
    return chips;
  }, [board]);

  // ---- Flat mode: active list stays as before (due-sorted, overdue
  // pinned); completed tasks (still scoped by the avatar filter) collect
  // into ONE CompletedFold at the bottom of the flat list
  // (docs/DESIGN_V5_COMPLETED_FOLD.md). ----
  const visibleActive = useMemo(() => visibleTasks.filter(isOpenTask), [visibleTasks]);
  const visibleCompleted = useMemo(() => visibleTasks.filter((t) => !isOpenTask(t)), [visibleTasks]);
  const overdueRows = useMemo(
    () => visibleActive.filter((t) => isOverdue(t.dueDate, t.status)).sort(compareDue),
    [visibleActive],
  );
  const scheduledRows = useMemo(
    () => visibleActive.filter((t) => !isOverdue(t.dueDate, t.status)).sort(compareDue),
    [visibleActive],
  );

  // ---- Grouped mode: filter scopes which sections render (binding
  // interaction contract — a member filter shows only that member's
  // section; the Unassigned filter shows only the Unassigned section; no
  // filter shows every member plus Unassigned, always last). ----
  const memberGroups =
    filterId && filterId !== UNASSIGNED
      ? (board?.members.filter((m) => m.member.id === filterId) ?? [])
      : filterId === UNASSIGNED
        ? []
        : (board?.members ?? []);
  const showUnassignedGroup = !filterId || filterId === UNASSIGNED;

  const openLabel = `${visibleTasks.filter(isOpenTask).length} open${filterId ? " · filtered" : ""}`;

  function toggleSelect(id: string) {
    // Starting a new selection (0 -> 1) dismisses a lingering undo toast so
    // the bulk bar takes over — README "10", binding.
    if (selected.size === 0) dismissToast();
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  function clearSelection() {
    setSelected(new Set());
  }

  // Exact full-name match wins; otherwise a prefix match, but only if it's
  // unique — two members sharing a prefix must not resolve ambiguously.
  function resolveAssignee(name: string | undefined): string | undefined {
    if (!name || !board) return undefined;
    const n = name.toLowerCase();
    const exact = board.members.find(({ member }) => member.name.toLowerCase() === n);
    if (exact) return exact.member.id;
    const prefixHits = board.members.filter(({ member }) => member.name.toLowerCase().startsWith(n));
    return prefixHits.length === 1 ? prefixHits[0].member.id : undefined;
  }

  // Team-page quick-add silently forces horizon=daily/period=today for
  // every task it creates (deliberate deviation #3, docs/DESIGN_V3_TEAMS.md)
  // — horizon is de-emphasized here regardless of any #override token.
  async function createOnTeam(result: QuickAddResult, assigneeId: string | undefined) {
    try {
      await api.createTask({
        title: result.title,
        horizon: "daily",
        period: today(),
        priority: result.priority,
        dueDate: result.dueDate,
        recurrence: result.recurrence,
        teamId,
        assigneeId,
      });
      reload();
      notifyTasksChanged();
    } catch (e) {
      fail(e);
    }
  }

  function makeGroupAdd(memberId: string | null) {
    return async (result: QuickAddResult) => {
      const overridden = resolveAssignee(result.assigneeName);
      await createOnTeam(result, overridden ?? memberId ?? undefined);
    };
  }

  // ---- Bulk actions — every one undoable: snapshot prior values, PATCH
  // back on undo; delete restores via POST /api/tasks/restore. ----
  async function bulkReassign(memberId: string) {
    const ids = Array.from(selected);
    if (!ids.length) return;
    const remembered = ids.map((id) => ({ id, assigneeId: taskById.get(id)?.assigneeId ?? null }));
    setSelected(new Set());
    try {
      await Promise.all(ids.map((id) => api.updateTask(id, { assigneeId: memberId })));
      reload();
      notifyTasksChanged();
      const name = memberById.get(memberId) ? firstName(memberById.get(memberId)!.name) : "member";
      showToast(`${plural(ids.length, "task")} reassigned to ${name}`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { assigneeId: r.assigneeId })))
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    }
  }

  async function bulkUnassign() {
    const ids = Array.from(selected);
    if (!ids.length) return;
    const remembered = ids.map((id) => ({ id, assigneeId: taskById.get(id)?.assigneeId ?? null }));
    setSelected(new Set());
    try {
      await Promise.all(ids.map((id) => api.updateTask(id, { assigneeId: null })));
      reload();
      notifyTasksChanged();
      showToast(`${plural(ids.length, "task")} unassigned`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { assigneeId: r.assigneeId })))
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    }
  }

  async function bulkSetDue(dueDate: string) {
    const ids = Array.from(selected);
    if (!ids.length) return;
    // Clearing dueDate over the wire = "" (docs/DESIGN_V2_UI.md), so the
    // remembered snapshot uses the same convention for the undo PATCH.
    const remembered = ids.map((id) => ({ id, dueDate: taskById.get(id)?.dueDate ?? "" }));
    setSelected(new Set());
    try {
      await Promise.all(ids.map((id) => api.updateTask(id, { dueDate })));
      reload();
      notifyTasksChanged();
      showToast(`${plural(ids.length, "task")} rescheduled`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { dueDate: r.dueDate })))
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    }
  }

  async function bulkMarkDone() {
    const ids = Array.from(selected);
    if (!ids.length) return;
    const remembered = ids
      .map((id) => {
        const t = taskById.get(id);
        return t ? { id, status: t.status as Status } : null;
      })
      .filter((r): r is { id: string; status: Status } => r !== null);
    setSelected(new Set());
    try {
      await Promise.all(remembered.map((r) => api.updateTask(r.id, { status: "done" })));
      reload();
      notifyTasksChanged();
      showToast(`${plural(remembered.length, "task")} marked done`, () => {
        Promise.all(remembered.map((r) => api.updateTask(r.id, { status: r.status })))
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    }
  }

  async function bulkDelete() {
    const ids = Array.from(selected);
    if (!ids.length) return;
    setSelected(new Set());
    try {
      const results = await Promise.all(ids.map((id) => api.deleteTask(id)));
      const deleted = results.flatMap((r) => r.deleted);
      reload();
      notifyTasksChanged();
      showToast(`${plural(ids.length, "task")} deleted`, () => {
        api
          .restoreTasks(deleted)
          .then(() => {
            reload();
            notifyTasksChanged();
          })
          .catch(fail);
      });
    } catch (e) {
      fail(e);
    }
  }

  // ---- Members panel ----
  function openMembers() {
    setMembersOpen(true);
    setMembersClosing(false);
  }
  function requestCloseMembers() {
    setMembersClosing(true);
    window.setTimeout(() => {
      setMembersOpen(false);
      setMembersClosing(false);
    }, 260);
  }
  useEffect(() => {
    if (!membersOpen) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") requestCloseMembers();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [membersOpen]);

  function startEdit(m: Member) {
    setEditingId(m.id);
    setEditName(m.name);
    setEditEmail(m.email ?? "");
    setEditRole(m.role ?? "");
  }
  function cancelEdit() {
    setEditingId(null);
  }
  async function saveEdit() {
    if (!editingId) return;
    const name = editName.trim();
    if (!name) return;
    // Member PATCH is a merge-unmarshal over the existing doc (server/store.go
    // PatchMember): omitted keys are left alone, but a present string key —
    // even "" — overwrites, so email/role are always sent to support clearing.
    const patch = { name, email: editEmail.trim(), role: editRole.trim() };
    try {
      await api.updateMember(editingId, patch);
      setEditingId(null);
      reload();
    } catch (e) {
      fail(e);
    }
  }
  async function removeMember(id: string) {
    if (!confirm("Remove this member? Their tasks will be unassigned.")) return;
    try {
      await api.deleteMember(id);
      if (editingId === id) setEditingId(null);
      if (filterId === id) setFilterId(null);
      reload();
    } catch (e) {
      fail(e);
    }
  }

  // ---- Admin user-management: enable-login / reset-password / disable
  // (docs/AUTH_FEATURES.md "API surface"). A member's login is enabled iff
  // systemRole is present (server sets it "required when passwordHash set" —
  // see api.ts's Member comment); that's the client-visible signal used
  // below to switch between "Enable login" and "Reset password"/"Disable".
  function startEnableLogin(m: Member) {
    setCredReveal(null);
    setEnablingId(m.id);
    setEnableRole("USER");
  }
  function cancelEnableLogin() {
    setEnablingId(null);
  }
  async function confirmEnableLogin(m: Member) {
    if (!m.email) return; // guarded in the UI — enable-login requires an email
    setMemberBusy(m.id);
    try {
      const { tempPassword } = await api.enableLogin(m.id, enableRole);
      setEnablingId(null);
      setCredReveal({ memberId: m.id, password: tempPassword, kind: "enabled" });
      reload();
    } catch (e) {
      fail(e);
    } finally {
      setMemberBusy(null);
    }
  }
  async function handleResetPassword(m: Member) {
    if (!confirm(`Reset ${m.name}'s password? This signs them out everywhere and issues a new temporary password.`)) return;
    setCredReveal(null);
    setMemberBusy(m.id);
    try {
      const { tempPassword } = await api.resetPassword(m.id);
      setCredReveal({ memberId: m.id, password: tempPassword, kind: "reset" });
    } catch (e) {
      fail(e);
    } finally {
      setMemberBusy(null);
    }
  }
  async function handleToggleDisabled(m: Member) {
    const next = !m.disabled;
    if (next && !confirm(`Disable ${m.name}? This signs them out immediately and blocks login until re-enabled.`)) return;
    setMemberBusy(m.id);
    try {
      await api.updateMember(m.id, { disabled: next });
      reload();
    } catch (e) {
      fail(e);
    } finally {
      setMemberBusy(null);
    }
  }
  async function copyTempPassword(password: string) {
    try {
      await navigator.clipboard.writeText(password);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API unavailable/denied — the reveal box still shows the
      // plaintext password so the admin can select-and-copy manually.
    }
  }

  async function submitAddMember() {
    const name = addName.trim();
    if (!name) return;
    const input = {
      name,
      email: addEmail.trim() || undefined,
      role: addRole.trim() || undefined,
      teamIds: [teamId],
    };
    try {
      await api.createMember(input);
      setAddName("");
      setAddEmail("");
      setAddRole("");
      reload();
    } catch (e) {
      fail(e);
    }
  }
  async function submitRename() {
    const name = renameDraft.trim();
    if (!name) return;
    try {
      await api.updateTeam(teamId, name);
      reload();
    } catch (e) {
      fail(e);
    }
  }
  const hasAnyTasks = allTasks.length > 0;
  const deleteHint = hasAnyTasks ? "Move or delete this team's tasks first" : "This can't be undone";
  async function submitDeleteTeam() {
    if (hasAnyTasks) return;
    if (!confirm("Delete this team? This can't be undone.")) return;
    try {
      await api.deleteTeam(teamId);
      onBack();
    } catch (e) {
      fail(e);
    }
  }

  const anySelected = selected.size > 0;

  return (
    <div className="team-page">
      <header className="tp-header">
        <button type="button" className="tp-back" onClick={onBack}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <polyline points="15 5 8 12 15 19" />
          </svg>
          Teams
        </button>
        <div className="tp-header-row">
          <div className="tp-header-left">
            <h1 className="tp-team-name">{board?.team.name ?? "…"}</h1>
            <div className="tp-status-chips">
              <span className="tp-chip tp-chip-todo">
                <span className="tp-chip-dot" aria-hidden="true" />
                {countTodo} to do
              </span>
              <span className="tp-chip tp-chip-prog">
                <span className="tp-chip-dot" aria-hidden="true" />
                {countProg} in progress
              </span>
              <span className="tp-chip tp-chip-done">
                <span className="tp-chip-dot" aria-hidden="true" />
                {countDone} done
              </span>
            </div>
          </div>
          <button type="button" className="tp-members-btn" onClick={openMembers}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="9" cy="8" r="3" />
              <path d="M3.5 19c0-3 2.5-5 5.5-5s5.5 2 5.5 5" />
              <path d="M16 6.2a3 3 0 0 1 0 5.6" />
              <path d="M17 14.2c2.2.5 3.8 2.3 3.8 4.8" />
            </svg>
            Members
          </button>
        </div>
      </header>

      {error && <div className="error" style={{ margin: "0 32px 12px" }}>{error}</div>}

      <div className="tp-filter-row">
        {filterChips.map((c) => (
          <button
            key={c.id}
            type="button"
            className={`tp-chip-filter${filterId === c.id ? " active" : ""}${c.disabled ? " disabled" : ""}`}
            onClick={() => setFilterId((f) => (f === c.id ? null : c.id))}
          >
            <span className="tp-chip-filter-avatar" style={{ background: c.tone.bg, color: c.tone.fg }}>
              {c.avatarInitials}
              {c.overdue > 0 && (
                <span className="tp-chip-overdue-badge" style={{ borderColor: filterId === c.id ? "var(--accent-soft)" : "var(--surface)" }}>
                  {c.overdue}
                </span>
              )}
            </span>
            <span className="tp-chip-filter-name">{c.name}</span>
            <span className="tp-chip-filter-count">{c.open}</span>
          </button>
        ))}
      </div>

      <div className="tp-toolbar">
        <div className="tp-toolbar-left">
          <button type="button" className={`tp-group-toggle${grouped ? " active" : ""}`} onClick={() => setGrouped((g) => !g)}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="6" cy="7" r="2.4" />
              <line x1="11" y1="7" x2="20" y2="7" />
              <circle cx="6" cy="16" r="2.4" />
              <line x1="11" y1="16" x2="20" y2="16" />
            </svg>
            Group by member
          </button>
          <span className="tp-open-label">{openLabel}</span>
        </div>
        <TaskComposer
          context="team"
          teamId={teamId}
          members={board?.members.map(({ member }) => member) ?? []}
          defaultAssigneeId={filterId && filterId !== UNASSIGNED ? filterId : undefined}
          onCreated={reload}
        />
      </div>

      <div className="tp-list-scroll">
        <div className="tp-list-inner">
          {!grouped && (
            <>
              {overdueRows.length > 0 && (
                <div className="tp-section">
                  <div className="tp-section-head overdue">
                    <span className="tp-dot" aria-hidden="true" />
                    <span className="tp-section-label">Overdue</span>
                    <span className="tp-count">{overdueRows.length}</span>
                    <span className="tp-rule" aria-hidden="true" />
                  </div>
                  <div className="tp-rows">
                    {overdueRows.map((t) => (
                      <Row
                        key={t.id}
                        task={t}
                        checked={selected.has(t.id)}
                        anySelected={anySelected}
                        onToggle={() => toggleSelect(t.id)}
                        assigneeAvatar={avatarFor(t.assigneeId)}
                        onOpen={setSelectedTaskId}
                        onChanged={handleChanged}
                      />
                    ))}
                  </div>
                </div>
              )}
              <div className="tp-rows">
                {scheduledRows.map((t) => (
                  <Row
                    key={t.id}
                    task={t}
                    checked={selected.has(t.id)}
                    anySelected={anySelected}
                    onToggle={() => toggleSelect(t.id)}
                    assigneeAvatar={avatarFor(t.assigneeId)}
                    onOpen={setSelectedTaskId}
                    onChanged={handleChanged}
                  />
                ))}
              </div>
              {overdueRows.length === 0 && scheduledRows.length === 0 && visibleCompleted.length === 0 && (
                <div className="tp-list-empty">No tasks here yet.</div>
              )}
              <CompletedFold
                tasks={visibleCompleted}
                onOpen={setSelectedTaskId}
                onChanged={handleChanged}
                render={(t) => (
                  <Row
                    task={t}
                    checked={selected.has(t.id)}
                    anySelected={anySelected}
                    onToggle={() => toggleSelect(t.id)}
                    assigneeAvatar={avatarFor(t.assigneeId)}
                    onOpen={setSelectedTaskId}
                    onChanged={handleChanged}
                  />
                )}
              />
            </>
          )}

          {grouped && (
            <>
              {memberGroups.map(({ member, tasks }) => (
                <GroupSection
                  key={member.id}
                  name={member.name}
                  memberInitials={initials(member.name)}
                  tone={AVATAR_TONES[toneIndex(member.id)]}
                  tasks={tasks}
                  addPlaceholder={`Add for ${firstName(member.name)}…`}
                  onAdd={makeGroupAdd(member.id)}
                  selected={selected}
                  anySelected={anySelected}
                  onToggle={toggleSelect}
                  avatarFor={avatarFor}
                  onOpen={setSelectedTaskId}
                  onChanged={handleChanged}
                  disabled={member.disabled}
                />
              ))}
              {showUnassignedGroup && (
                <GroupSection
                  key="unassigned"
                  name="Unassigned"
                  memberInitials="—"
                  tone={UNASSIGNED_TONE}
                  tasks={board?.unassigned ?? []}
                  addPlaceholder="Add unassigned task…"
                  onAdd={makeGroupAdd(null)}
                  selected={selected}
                  anySelected={anySelected}
                  onToggle={toggleSelect}
                  avatarFor={avatarFor}
                  onOpen={setSelectedTaskId}
                  onChanged={handleChanged}
                />
              )}
            </>
          )}
        </div>
      </div>

      <div className={`tp-bulkbar${anySelected ? " visible" : ""}`} aria-hidden={!anySelected}>
        <span className="tp-bulk-count">{selected.size} selected</span>
        <button type="button" className="tp-bulk-clear" title="Clear" onClick={clearSelection}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <line x1="6" y1="6" x2="18" y2="18" />
            <line x1="18" y1="6" x2="6" y2="18" />
          </svg>
        </button>
        <div className="tp-bulk-divider" />
        <label className="tp-bulk-reassign">
          <span>Reassign</span>
          <select
            defaultValue=""
            onChange={(e) => {
              const v = e.target.value;
              if (v) bulkReassign(v);
              e.target.value = "";
            }}
          >
            <option value="" disabled>
              to…
            </option>
            {board?.members.map(({ member }) => (
              <option key={member.id} value={member.id}>
                {member.name}
              </option>
            ))}
          </select>
        </label>
        <button type="button" className="tp-bulk-unassign" onClick={bulkUnassign}>
          Unassign
        </button>
        <label className="tp-bulk-due">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
            <rect x="3" y="5" width="18" height="16" rx="2" />
            <line x1="3" y1="9.5" x2="21" y2="9.5" />
          </svg>
          <input
            type="date"
            onChange={(e) => {
              const v = e.target.value;
              if (v) bulkSetDue(v);
              e.target.value = "";
            }}
          />
        </label>
        <button type="button" className="tp-bulk-done" onClick={bulkMarkDone}>
          Mark done
        </button>
        {isAdmin && (
          <button type="button" className="tp-bulk-delete" title="Delete" onClick={bulkDelete}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="4 7 20 7" />
              <path d="M9 7V5h6v2" />
              <path d="M6 7l1 13h10l1-13" />
            </svg>
          </button>
        )}
      </div>

      {membersOpen && (
        <>
          <div className={`tp-members-backdrop${membersClosing ? " closing" : ""}`} onClick={requestCloseMembers} />
          <aside className={`tp-members-panel${membersClosing ? " closing" : ""}`} role="dialog" aria-modal="true" aria-label="Members">
            <div className="tp-mp-header">
              <span className="tp-mp-title">Members</span>
              <span className="tp-mp-count">{board?.members.length ?? 0}</span>
              <button type="button" className="tp-mp-close" title="Close" onClick={requestCloseMembers}>
                <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round">
                  <line x1="6" y1="6" x2="18" y2="18" />
                  <line x1="18" y1="6" x2="6" y2="18" />
                </svg>
              </button>
            </div>

            <div className="tp-mp-scroll">
              {!isAdmin && <div className="tp-mp-readonly-note">Read-only roster — only admins can manage members.</div>}
              <div className="tp-mp-list">
                {board?.members.map(({ member }) => {
                  const tone = AVATAR_TONES[toneIndex(member.id)];
                  if (isAdmin && editingId === member.id) {
                    return (
                      <div className="tp-mp-row-editing" key={member.id}>
                        <input value={editName} placeholder="Name" onChange={(e) => setEditName(e.target.value)} />
                        <input value={editEmail} placeholder="Email" onChange={(e) => setEditEmail(e.target.value)} />
                        <input value={editRole} placeholder="Role" onChange={(e) => setEditRole(e.target.value)} />
                        <div className="tp-mp-row-actions">
                          <button type="button" onClick={cancelEdit}>
                            Cancel
                          </button>
                          <button type="button" className="primary" onClick={saveEdit}>
                            Save
                          </button>
                        </div>
                      </div>
                    );
                  }

                  // Login-enabled iff systemRole is present (see api.ts's
                  // Member comment — server only sets it alongside a
                  // passwordHash). Drives which admin affordance shows below.
                  const loginEnabled = member.systemRole != null;
                  const isRevealing = isAdmin && credReveal?.memberId === member.id;
                  const isEnabling = isAdmin && enablingId === member.id;
                  const rowBusy = memberBusy === member.id;

                  return (
                    <div className={`tp-mp-member${member.disabled ? " tp-mp-member-disabled" : ""}`} key={member.id}>
                      <div className="tp-mp-row">
                        <span className="tp-mp-avatar" style={{ background: tone.bg, color: tone.fg }}>
                          {initials(member.name)}
                        </span>
                        <div className="tp-mp-id">
                          <div className="tp-mp-name">
                            {member.name}
                            {loginEnabled && (
                              <span className={`tp-mp-role-badge${member.systemRole === "ADMIN" ? " admin" : ""}`}>
                                {member.systemRole}
                              </span>
                            )}
                            {member.disabled && <span className="tp-mp-disabled-tag">Disabled</span>}
                          </div>
                          <div className="tp-mp-sub">
                            {member.role || "—"} <span className="tp-mp-email">· {member.email || "no email"}</span>
                          </div>
                        </div>
                        {isAdmin && (
                          <>
                            <button type="button" className="tp-mp-icon-btn" title="Edit" onClick={() => startEdit(member)}>
                              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                                <path d="M4 20h4L18.5 9.5a2 2 0 0 0-3-3L5 17z" />
                              </svg>
                            </button>
                            <button type="button" className="tp-mp-icon-btn tp-mp-danger" title="Remove" onClick={() => removeMember(member.id)}>
                              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                                <polyline points="4 7 20 7" />
                                <path d="M9 7V5h6v2" />
                                <path d="M6 7l1 13h10l1-13" />
                              </svg>
                            </button>
                          </>
                        )}
                      </div>

                      {isRevealing && credReveal && (
                        <div className="tp-mp-reveal">
                          <div className="tp-mp-reveal-label">
                            {credReveal.kind === "enabled" ? "Login enabled — temporary password" : "New temporary password"}
                          </div>
                          <div className="tp-mp-reveal-row">
                            <code className="tp-mp-reveal-pass">{credReveal.password}</code>
                            <button type="button" onClick={() => copyTempPassword(credReveal.password)}>
                              {copied ? "Copied" : "Copy"}
                            </button>
                          </div>
                          <div className="tp-mp-reveal-hint">Won&rsquo;t be shown again.</div>
                          <button type="button" className="tp-mp-reveal-done" onClick={() => setCredReveal(null)}>
                            Done
                          </button>
                        </div>
                      )}

                      {isAdmin && !isRevealing && isEnabling && (
                        <div className="tp-mp-enable-form">
                          <select value={enableRole} onChange={(e) => setEnableRole(e.target.value as SystemRole)}>
                            <option value="USER">USER</option>
                            <option value="ADMIN">ADMIN</option>
                          </select>
                          <button type="button" className="primary" disabled={rowBusy} onClick={() => confirmEnableLogin(member)}>
                            Confirm
                          </button>
                          <button type="button" onClick={cancelEnableLogin} disabled={rowBusy}>
                            Cancel
                          </button>
                        </div>
                      )}

                      {isAdmin && !isRevealing && !isEnabling && (
                        <div className="tp-mp-cred-actions">
                          {!loginEnabled ? (
                            member.email ? (
                              <button type="button" className="tp-mp-link-btn" onClick={() => startEnableLogin(member)}>
                                Enable login
                              </button>
                            ) : (
                              <span className="tp-mp-cred-hint">
                                Add an email to enable login —{" "}
                                <button type="button" className="link" onClick={() => startEdit(member)}>
                                  add one
                                </button>
                              </span>
                            )
                          ) : (
                            <>
                              <button type="button" className="tp-mp-link-btn" disabled={rowBusy} onClick={() => handleResetPassword(member)}>
                                Reset password
                              </button>
                              <label className="tp-mp-disable-toggle">
                                <input
                                  type="checkbox"
                                  checked={!member.disabled}
                                  disabled={rowBusy}
                                  onChange={() => handleToggleDisabled(member)}
                                />
                                <span>{member.disabled ? "Disabled" : "Enabled"}</span>
                              </label>
                            </>
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}
                {(!board || board.members.length === 0) && <div className="empty">No members yet.</div>}
              </div>

              {isAdmin && (
                <div className="tp-mp-add">
                  <div className="tp-mp-add-label">Add member</div>
                  <div className="tp-mp-add-form">
                    <input placeholder="Name" value={addName} onChange={(e) => setAddName(e.target.value)} />
                    <input placeholder="Email" type="email" value={addEmail} onChange={(e) => setAddEmail(e.target.value)} />
                    <div className="tp-mp-add-row2">
                      <input placeholder="Role" value={addRole} onChange={(e) => setAddRole(e.target.value)} />
                      <button type="button" onClick={submitAddMember}>
                        Add
                      </button>
                    </div>
                  </div>
                </div>
              )}
            </div>

            {isAdmin && (
              <div className="tp-mp-footer">
                <div className="tp-mp-rename">
                  <input value={renameDraft} onChange={(e) => setRenameDraft(e.target.value)} />
                  <button type="button" onClick={submitRename}>
                    Rename
                  </button>
                </div>
                <button type="button" className="tp-mp-delete" disabled={hasAnyTasks} title={deleteHint} onClick={submitDeleteTeam}>
                  Delete team
                </button>
                <div className="tp-mp-delete-hint">{deleteHint}</div>
              </div>
            )}
          </aside>
        </>
      )}

      {selectedTaskId && <TaskDetail id={selectedTaskId} onClose={() => setSelectedTaskId(null)} onChanged={handleChanged} />}
    </div>
  );
}
