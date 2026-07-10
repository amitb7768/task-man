// Teams landing (design handoff "09 Teams Landing"). One card per team:
// serif name, overlapping avatar stack (+N overflow), mono meta
// (N members · N open), and a red overdue pill hidden at 0. Whole card is
// the click target. Inline "New team" form (Enter creates, Esc cancels).
// Dashed empty state when there are no teams yet.
//
// The `memberCount`/`openCount`/`overdueCount` fields on Team are additive
// and may not be live on every backend yet (docs/DESIGN_V3_TEAMS.md
// "Backend delta") — member count falls back to the real count derived from
// the member list we already fetch for the avatar stack; open/overdue counts
// (which aren't otherwise cheaply computable client-side) fall back to 0 so
// the badge/meta line renders instead of blocking on the server delta.
import { useEffect, useState } from "react";
import type { Member, Team } from "../api";
import { api, ApiError } from "../api";
import { useAuth } from "../auth/AuthContext";
import "../styles/teams-landing.css";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
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

// Deterministic per-member tone (docs/DESIGN_V3_TEAMS.md "Avatar tones").
// Identical copy lives in views/TeamPage.tsx and components/TaskRow.tsx —
// each view/component computes tones locally per the file-ownership split
// in the build contract, so the formula must stay byte-for-byte in sync.
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

const MAX_AVATARS = 4;

function PeopleGlyph() {
  return (
    <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="9" cy="8" r="3" />
      <path d="M3.5 19c0-3 2.5-5 5.5-5s5.5 2 5.5 5" />
      <path d="M16 6.2a3 3 0 0 1 0 5.6" />
      <path d="M17 14.2c2.2.5 3.8 2.3 3.8 4.8" />
    </svg>
  );
}

function TeamCard({ team, members, onOpen }: { team: Team; members: Member[]; onOpen: (id: string) => void }) {
  const shown = members.slice(0, MAX_AVATARS);
  const overflow = members.length - shown.length;
  const memberCount = team.memberCount ?? members.length;
  const openCount = team.openCount ?? 0;
  const overdueCount = team.overdueCount ?? 0;

  return (
    <div className="tl-card" onClick={() => onOpen(team.id)}>
      <div className="tl-card-top">
        <h2 className="tl-card-name">{team.name}</h2>
        {overdueCount > 0 && (
          <span className="tl-overdue-pill">
            <span className="tl-overdue-dot" aria-hidden="true" />
            {overdueCount} overdue
          </span>
        )}
      </div>
      <div className="tl-card-bottom">
        <div className="tl-avatars">
          {shown.map((m) => {
            const tone = AVATAR_TONES[toneIndex(m.id)];
            return (
              <span key={m.id} className="tl-avatar" style={{ background: tone.bg, color: tone.fg }}>
                {initials(m.name)}
              </span>
            );
          })}
          {overflow > 0 && <span className="tl-avatar tl-avatar-more">+{overflow}</span>}
        </div>
        <span className="tl-meta">
          {memberCount} {memberCount === 1 ? "member" : "members"} · {openCount} open
        </span>
      </div>
    </div>
  );
}

export default function TeamsLanding({ onOpenTeam }: { onOpenTeam: (id: string) => void }) {
  const { user } = useAuth();
  // Teams create/rename/delete is ADMIN-only (docs/AUTH_FEATURES.md "Endpoint
  // x role matrix") — hide the New-team affordance everywhere it appears on
  // this screen. GET /api/teams is already scoped server-side (USER sees
  // only their own teams), so no client-side list filtering is needed here.
  const isAdmin = user.systemRole === "ADMIN";
  const [teams, setTeams] = useState<Team[] | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");

  function reload() {
    setError(null);
    api
      .listTeams()
      .then(setTeams)
      .catch((e) => setError(errMsg(e)));
    api.listMembers().then(setMembers).catch(() => {});
  }

  useEffect(reload, []);

  function startCreating() {
    setCreating(true);
    setNewName("");
  }
  function cancelCreating() {
    setCreating(false);
    setNewName("");
  }

  async function confirmCreating() {
    const name = newName.trim();
    if (!name) {
      cancelCreating();
      return;
    }
    setCreating(false);
    setNewName("");
    try {
      const t = await api.createTeam(name);
      setTeams((prev) => [...(prev ?? []), t]);
    } catch (e) {
      setError(errMsg(e));
    }
  }

  const membersByTeam = new Map<string, Member[]>();
  for (const m of members) {
    for (const tid of m.teamIds ?? []) {
      const list = membersByTeam.get(tid);
      if (list) list.push(m);
      else membersByTeam.set(tid, [m]);
    }
  }

  return (
    <div className="teams-landing">
      {error && <div className="error">{error}</div>}

      {isAdmin && (
        <div className="tl-toolbar">
          <button type="button" className="tl-new-btn" onClick={startCreating}>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            New team
          </button>
        </div>
      )}

      {isAdmin && creating && (
        <div className="tl-new-form">
          <div className="tl-new-avatar" aria-hidden="true">
            +
          </div>
          <input
            autoFocus
            value={newName}
            placeholder="Team name…"
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                confirmCreating();
              } else if (e.key === "Escape") {
                cancelCreating();
              }
            }}
          />
          <button type="button" className="tl-new-cancel" onClick={cancelCreating}>
            Cancel
          </button>
          <button type="button" className="tl-new-confirm" onClick={confirmCreating}>
            Create
          </button>
        </div>
      )}

      {teams && teams.length > 0 && (
        <div className="tl-grid">
          {teams.map((t) => (
            <TeamCard key={t.id} team={t} members={membersByTeam.get(t.id) ?? []} onOpen={onOpenTeam} />
          ))}
        </div>
      )}

      {teams && teams.length === 0 && (
        <div className="tl-empty">
          <div className="tl-empty-icon">
            <PeopleGlyph />
          </div>
          <div className="tl-empty-title">No teams yet</div>
          <div className="tl-empty-sub">
            {isAdmin
              ? "Create a team to group tasks by the people you’re tracking. Members are records — they don’t need to log in."
              : "You’re not on a team yet. Ask an admin to add you to one."}
          </div>
          {isAdmin && (
            <button type="button" className="tl-new-btn" onClick={startCreating}>
              New team
            </button>
          )}
        </div>
      )}
    </div>
  );
}
