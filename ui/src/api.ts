// Typed fetch wrapper + API surface matching docs/DESIGN.md "API contract"
// and (for auth/user-management) docs/AUTH_FEATURES.md "API surface".

// "backlog" (docs/DESIGN_V7_BACKLOG.md): the unstaffed personal planning pool —
// horizon "backlog" always pairs with period "" and no dueDate/recurrence/
// teamId/assigneeId (server-enforced; see validateTaskFields). ADMIN-only.
export type Horizon = "daily" | "weekly" | "monthly" | "backlog";
export type Status = "todo" | "in_progress" | "done" | "cancelled";
export type Priority = "" | "low" | "medium" | "high";
export type RecurrenceFreq = "daily" | "weekdays" | "weekly" | "monthly";
export type SystemRole = "ADMIN" | "USER";

export interface Recurrence {
  freq: RecurrenceFreq;
  interval?: number; // "every N periods"; ≥ 1, absent == 1
  /** Server-managed period this occurrence math counts from. Never client-settable. */
  readonly anchor?: string;
  weekdays?: number[]; // ISO Mon=1..Sun=7
  dayOfMonth?: number; // 1..31
}

// v9 daily-notes activity entry (docs/DESIGN_V9_NOTES_SUMMARY.md "A. Data
// model"). kind "note": text/by/byName/editedAt meaningful. kind "status":
// from/to meaningful (auto-logged server-side on every status change), never
// client-editable.
export interface ActivityEntry {
  id: string;
  kind: "note" | "status";
  date: string; // YYYY-MM-DD, machine-local "daily" bucket
  at: string; // ISO datetime — server instant of the write
  by?: string;
  byName?: string;
  text?: string; // kind=note only
  from?: string; // kind=status only
  to?: string; // kind=status only
  editedAt?: string;
}

export interface TaskView {
  id: string;
  title: string;
  notes?: string;
  horizon: Horizon;
  period: string;
  dueDate?: string;
  status: Status;
  priority: Priority;
  parentId?: string;
  teamId?: string;
  assigneeId?: string;
  recurrence?: Recurrence;
  seriesId?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  progress: { done: number; total: number };
  // Server-managed ISO week ("YYYY-Www") this task belongs to — present iff
  // the task is a team task (docs/DESIGN_V6_WEEK_ROLLOVER.md "Data model").
  // Drives board visibility (undated open + terminal tasks only show when
  // weekOf === the board's current week) and the rollover/history queries.
  weekOf?: string;
  // v9 (docs/DESIGN_V9_NOTES_SUMMARY.md): the daily-notes/status log. Present
  // on full-doc reads (GET /api/tasks/{id}); list/board/search endpoints
  // project it away server-side, so it's absent there — never assume presence.
  activity?: ActivityEntry[];
}

export interface TaskDetail extends TaskView {
  children: TaskView[];
}

export interface Team {
  id: string;
  name: string;
  createdAt: string;
  // Additive (docs/DESIGN_V3_TEAMS.md "Backend delta"): server-aggregated
  // counts for the teams landing page. Optional because the counting
  // backend delta may not be deployed on every environment yet — callers
  // must render a 0-fallback rather than assume presence.
  memberCount?: number;
  openCount?: number;
  overdueCount?: number;
}

export interface Member {
  id: string;
  name: string;
  email?: string;
  role?: string;
  teamIds?: string[];
  createdAt: string;
  // Auth additions (docs/AUTH_FEATURES.md "Data model changes" — member
  // records ARE user records; credential fields are optional). systemRole
  // is "required when passwordHash set" server-side, so its mere presence
  // here is the client-visible signal that login is enabled for this member
  // — the UI never sees passwordHash itself. disabled is only meaningful
  // (and only ever true) for login-enabled members.
  systemRole?: SystemRole;
  disabled?: boolean;
}

// GET /api/auth/me / POST /api/auth/login response shape
// (docs/AUTH_FEATURES.md "API surface").
export interface AuthUser {
  id: string;
  name: string;
  email: string;
  systemRole: SystemRole;
  mustChangePassword: boolean;
  teams: { id: string; name: string }[];
}

export interface CreateTaskInput {
  title: string;
  notes?: string;
  horizon: Horizon;
  period: string;
  dueDate?: string;
  status?: Status;
  priority?: Priority;
  parentId?: string;
  teamId?: string;
  assigneeId?: string;
  recurrence?: Recurrence;
}

// Partial update: fields set to null explicitly clear that field server-side;
// fields omitted are left unchanged. (Not spelled out in DESIGN.md beyond
// "partial update, any mutable field" — this is the standard REST convention
// and what this UI relies on for clearing dueDate/parentId/recurrence/etc.)
export interface UpdateTaskInput {
  title?: string;
  notes?: string | null;
  horizon?: Horizon;
  period?: string;
  dueDate?: string | null;
  status?: Status;
  priority?: Priority;
  parentId?: string | null;
  teamId?: string | null;
  assigneeId?: string | null;
  recurrence?: Recurrence | null;
  // ADMIN-only (403 otherwise); this is the rollover "move to this week"
  // primitive and its undo (docs/DESIGN_V6_WEEK_ROLLOVER.md "Rollover").
  // Never client-cleared — always a valid ISO week key.
  weekOf?: string;
}

export interface DayViewResponse {
  tasks: TaskView[];
  weekContext: TaskView[];
}

export interface WeekViewResponse {
  tasks: TaskView[];
  days: Record<string, TaskView[]>;
  monthContext: TaskView[];
}

export interface MonthViewResponse {
  tasks: TaskView[];
  weeks: Record<string, TaskView[]>;
}

export interface AttentionViewResponse {
  overdue: TaskView[];
  slipped: TaskView[];
}

export interface TeamBoardResponse {
  team: Team;
  members: { member: Member; tasks: TaskView[] }[];
  unassigned: TaskView[];
  // v6 week-scoping (docs/DESIGN_V6_WEEK_ROLLOVER.md "Board visibility"):
  // `week` is the board's current ISO week (server's currentWeek()); the
  // response is already filtered server-side to this week's working set —
  // never re-filter client-side. `staleOpen` counts open+undated+past-week
  // tasks (rollover-eligible); the board's ADMIN-only banner shows iff > 0.
  week: string;
  staleOpen: number;
}

// GET /api/teams/{id}/rollover response (ADMIN-only — docs/DESIGN_V6_WEEK_ROLLOVER.md
// "Rollover"). tasks are open, undated, past-week, sorted weekOf asc then
// createdAt asc (oldest week first).
export interface RolloverResponse {
  week: string;
  tasks: TaskView[];
}

// GET /api/teams/{id}/history response (docs/DESIGN_V6_WEEK_ROLLOVER.md
// "History"). tasks are done/cancelled, past weeks, createdAt desc; hasMore
// drives the "Load more" pagination (first pagination in the codebase).
export interface HistoryResponse {
  tasks: TaskView[];
  hasMore: boolean;
}

export interface SearchParams {
  q?: string;
  status?: Status | "open";
  priority?: Priority;
  horizon?: Horizon;
  teamId?: string;
  assigneeId?: string;
  overdue?: boolean;
}

// v9 date-range summary (docs/DESIGN_V9_NOTES_SUMMARY.md "B. Endpoint —
// summary"). No teamId -> "me" scope (caller's personal + assigned-to-me).
export interface SummaryScope {
  teamId?: string;
  assigneeId?: string;
}

export interface SummaryTask {
  id: string;
  title: string;
  status: Status;
  priority: Priority;
  horizon: Horizon;
  dueDate?: string;
  teamId?: string;
  teamName?: string;
  assigneeId?: string;
  assigneeName?: string;
  createdAt: string;
  closedDate?: string;
  overdue: boolean;
  notes: ActivityEntry[];
}

export interface SummaryResponse {
  from: string;
  to: string;
  teamId?: string;
  teamName?: string;
  assigneeId?: string;
  assigneeName?: string;
  completed: SummaryTask[];
  updated: SummaryTask[];
  added: SummaryTask[];
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

// ---------------------------------------------------------------------------
// Global 401 handler (docs/AUTH_FEATURES.md "UI changes": "401 mid-session
// (expired) -> redirect to login preserving nothing"). Any request that
// comes back 401 means the session is gone (expired, invalidated by a
// password change/reset/disable elsewhere, or never existed); ui/src/auth's
// AuthProvider subscribes here once at the root and flips the whole app back
// to the login screen — one place, not per-call handling, per
// docs/research-auth.md "Route hiding vs component hiding".
//
// 403 (role failure) is deliberately NOT routed through this — a 403 means
// the session is fine but the action isn't allowed, so it should surface as
// an inline error exactly like any other ApiError (existing `catch (e) {
// setError(errMsg(e)) }` patterns throughout the views already do this; no
// special-casing needed for 403 here).
// ---------------------------------------------------------------------------
type UnauthorizedListener = () => void;
const unauthorizedListeners = new Set<UnauthorizedListener>();

/** Subscribe to global 401s. Returns an unsubscribe function. */
export function onUnauthorized(cb: UnauthorizedListener): () => void {
  unauthorizedListeners.add(cb);
  return () => unauthorizedListeners.delete(cb);
}

interface RequestOpts extends RequestInit {
  // Auth-flow endpoints (login, change-password) expect 401 as a normal,
  // locally-handled outcome (bad credentials / wrong current password) —
  // routing those through the global handler would incorrectly bounce a
  // user who's mid-way through the forced change-password screen back to
  // an anonymous state on a typo. Everything else dispatches as usual.
  suppressAuthDispatch?: boolean;
}

async function request<T>(path: string, opts?: RequestOpts): Promise<T> {
  const res = await fetch(`/api${path}`, {
    ...opts,
    headers: { "Content-Type": "application/json", ...(opts?.headers ?? {}) },
  });
  if (!res.ok) {
    if (res.status === 401 && !opts?.suppressAuthDispatch) {
      unauthorizedListeners.forEach((l) => l());
    }
    let message = res.statusText || `HTTP ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) message = body.error;
    } catch {
      // non-JSON error body — keep statusText
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return text ? (JSON.parse(text) as T) : (undefined as T);
}

function qs(params: Record<string, string | undefined>): string {
  const parts: string[] = [];
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(v)}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

export const api = {
  createTask: (input: CreateTaskInput) =>
    request<TaskView>("/tasks", { method: "POST", body: JSON.stringify(input) }),
  getTask: (id: string) => request<TaskDetail>(`/tasks/${id}`),
  updateTask: (id: string, patch: UpdateTaskInput) =>
    request<TaskView>(`/tasks/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),
  // Returns the full JSON docs of the deleted subtree (task + cascaded
  // subtasks), so the caller can power an undo toast via restoreTasks.
  deleteTask: (id: string) =>
    request<{ deleted: TaskView[] }>(`/tasks/${id}`, { method: "DELETE" }),
  // Re-inserts docs with their original ids (as produced by deleteTask).
  // Exists solely to power undo toasts — no revalidation beyond JSON decode.
  restoreTasks: (tasks: TaskView[]) =>
    request<{ restored: number }>("/tasks/restore", {
      method: "POST",
      body: JSON.stringify({ tasks }),
    }),

  // v9 daily notes (docs/DESIGN_V9_NOTES_SUMMARY.md "A. Endpoints — notes").
  // date defaults to today-local server-side when omitted; backdating and
  // future dates are both allowed.
  addNote: (id: string, input: { text: string; date?: string }) =>
    request<ActivityEntry>(`/tasks/${id}/notes`, { method: "POST", body: JSON.stringify(input) }),
  editNote: (id: string, noteId: string, input: { text: string; date?: string }) =>
    request<ActivityEntry>(`/tasks/${id}/notes/${noteId}`, { method: "PUT", body: JSON.stringify(input) }),
  deleteNote: (id: string, noteId: string) =>
    request<void>(`/tasks/${id}/notes/${noteId}`, { method: "DELETE" }),

  // v9 date-range summary (docs/DESIGN_V9_NOTES_SUMMARY.md "B. Endpoint —
  // summary"). scope.teamId absent -> me-scope (personal + assigned-to-me).
  summary: (from: string, to: string, scope: SummaryScope) =>
    request<SummaryResponse>(`/summary${qs({ from, to, teamId: scope.teamId, assigneeId: scope.assigneeId })}`),

  dayView: (date: string) => request<DayViewResponse>(`/views/day${qs({ date })}`),
  weekView: (week: string) => request<WeekViewResponse>(`/views/week${qs({ week })}`),
  monthView: (month: string) => request<MonthViewResponse>(`/views/month${qs({ month })}`),
  attentionView: () => request<AttentionViewResponse>("/views/attention"),
  reschedule: (ids: string[]) =>
    request<{ updated: number }>("/tasks/reschedule", {
      method: "POST",
      body: JSON.stringify({ ids }),
    }),
  // ADMIN-only (403 for USER) — the caller's own backlog, createdAt desc
  // (docs/DESIGN_V7_BACKLOG.md "Endpoints").
  backlog: () => request<{ tasks: TaskView[] }>("/backlog"),
  search: (params: SearchParams) =>
    request<{ tasks: TaskView[] }>(
      `/search${qs({
        q: params.q,
        status: params.status,
        priority: params.priority,
        horizon: params.horizon,
        teamId: params.teamId,
        assigneeId: params.assigneeId,
        overdue: params.overdue ? "true" : undefined,
      })}`,
    ),

  listTeams: () => request<Team[]>("/teams"),
  createTeam: (name: string) =>
    request<Team>("/teams", { method: "POST", body: JSON.stringify({ name }) }),
  updateTeam: (id: string, name: string) =>
    request<Team>(`/teams/${id}`, { method: "PATCH", body: JSON.stringify({ name }) }),
  deleteTeam: (id: string) => request<void>(`/teams/${id}`, { method: "DELETE" }),
  teamBoard: (id: string) => request<TeamBoardResponse>(`/teams/${id}/board`),
  // ADMIN-only (server 403s otherwise — docs/DESIGN_V6_WEEK_ROLLOVER.md
  // "Endpoint x role matrix").
  teamRollover: (id: string) => request<RolloverResponse>(`/teams/${id}/rollover`),
  teamHistory: (id: string, offset: number, limit: number) =>
    request<HistoryResponse>(`/teams/${id}/history${qs({ offset: String(offset), limit: String(limit) })}`),

  listMembers: (teamId?: string) => request<Member[]>(`/members${qs({ teamId })}`),
  createMember: (input: { name: string; email?: string; role?: string; teamIds: string[] }) =>
    request<Member>("/members", { method: "POST", body: JSON.stringify(input) }),
  updateMember: (
    id: string,
    patch: Partial<{ name: string; email: string; role: string; teamIds: string[]; systemRole: SystemRole; disabled: boolean }>,
  ) => request<Member>(`/members/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),
  deleteMember: (id: string) => request<void>(`/members/${id}`, { method: "DELETE" }),

  // Admin user-management (docs/AUTH_FEATURES.md "API surface", ADMIN-only —
  // server enforces; UI hides per the endpoint x role matrix).
  enableLogin: (id: string, systemRole: SystemRole) =>
    request<{ tempPassword: string }>(`/members/${id}/enable-login`, {
      method: "POST",
      body: JSON.stringify({ systemRole }),
    }),
  resetPassword: (id: string) =>
    request<{ tempPassword: string }>(`/members/${id}/reset-password`, { method: "POST" }),

  // Auth (docs/AUTH_FEATURES.md "API surface").
  auth: {
    login: (email: string, password: string) =>
      request<{ user: AuthUser }>("/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password }),
        suppressAuthDispatch: true,
      }),
    logout: () => request<void>("/auth/logout", { method: "POST" }),
    me: () => request<{ user: AuthUser }>("/auth/me"),
    changePassword: (current: string, next: string) =>
      request<void>("/auth/change-password", {
        method: "POST",
        body: JSON.stringify({ current, new: next }),
        suppressAuthDispatch: true,
      }),
  },
};

// backlog ranks below every real horizon — it has no valid child horizon
// (TaskDetail's subtask-horizon picker naturally offers none for a backlog
// parent, which is correct: backlog tasks aren't a subtask target).
export const horizonRank: Record<Horizon, number> = { daily: 1, weekly: 2, monthly: 3, backlog: 0 };
