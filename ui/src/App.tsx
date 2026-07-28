import type { ReactElement } from "react";
import { useCallback, useEffect, useLayoutEffect, useState } from "react";
import type { Horizon } from "./api";
import { api } from "./api";
import { useAuth } from "./auth/AuthContext";
import ChangePasswordPanel from "./auth/ChangePasswordPanel";
import {
  addDays,
  addMonths,
  addWeeks,
  currentMonth,
  currentWeek,
  formatDayBadgePeriod,
  formatDayEyebrow,
  formatMonthLabel,
  formatWeekBadgePeriod,
  formatWeekLabel,
  today,
} from "./period";
import TaskComposer from "./components/TaskComposer";
import ToastHost from "./components/Toast";
import DayView from "./views/DayView";
import WeekView from "./views/WeekView";
import MonthView from "./views/MonthView";
import AllTasksView from "./views/AllTasksView";
import AttentionView from "./views/AttentionView";
import TeamsLanding from "./views/TeamsLanding";
import TeamPage from "./views/TeamPage";
import SearchView from "./views/SearchView";
import BacklogView from "./views/BacklogView";

// ---------------------------------------------------------------------------
// Cross-cutting "something changed" signal. TaskRow and TaskComposer call
// notifyTasksChanged() after every mutation/create so the sidebar Attention
// badge stays fresh without prop-drilling a refresh callback through every
// view. See docs/DESIGN_V2_UI.md "Attention badge".
// ---------------------------------------------------------------------------
type ChangeListener = () => void;
const changeListeners = new Set<ChangeListener>();

export function notifyTasksChanged(): void {
  changeListeners.forEach((l) => l());
}

function useTasksChangedSubscription(cb: ChangeListener) {
  useEffect(() => {
    changeListeners.add(cb);
    return () => {
      changeListeners.delete(cb);
    };
  }, [cb]);
}

// ---------------------------------------------------------------------------
// Theme: System / Light / Dark, persisted to localStorage, stamped as
// data-theme on <html>. index.html carries a tiny inline script that applies
// the persisted choice before first paint (avoids a flash); this effect keeps
// it in sync thereafter.
// ---------------------------------------------------------------------------
type ThemeMode = "system" | "light" | "dark";
const THEME_KEY = "taskman-theme";

function readStoredTheme(): ThemeMode {
  try {
    const v = localStorage.getItem(THEME_KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    // localStorage unavailable — fall back to system
  }
  return "system";
}

function applyTheme(mode: ThemeMode) {
  const root = document.documentElement;
  if (mode === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", mode);
}

// ---------------------------------------------------------------------------
// Teams navigation state (docs/DESIGN_V3_TEAMS.md "Navigation state"): the
// Teams nav item shows TeamsLanding; selecting a team shows TeamPage. The
// selected team id persists to localStorage so returning to the Teams tab
// (from another tab, or a fresh session) reopens the last team's page;
// landing is reached only via the team page's back affordance, which clears
// the persisted selection.
// ---------------------------------------------------------------------------
const TEAM_KEY = "taskman-team";

function readStoredTeam(): string | null {
  try {
    return localStorage.getItem(TEAM_KEY);
  } catch {
    return null;
  }
}

const NAV_ICON_PATHS: Record<string, ReactElement> = {
  day: (
    <>
      <circle cx="12" cy="12" r="4.2" />
      <line x1="12" y1="2.5" x2="12" y2="5" />
      <line x1="12" y1="19" x2="12" y2="21.5" />
      <line x1="2.5" y1="12" x2="5" y2="12" />
      <line x1="19" y1="12" x2="21.5" y2="12" />
      <line x1="5.4" y1="5.4" x2="7" y2="7" />
      <line x1="17" y1="17" x2="18.6" y2="18.6" />
      <line x1="5.4" y1="18.6" x2="7" y2="17" />
      <line x1="17" y1="7" x2="18.6" y2="5.4" />
    </>
  ),
  week: (
    <>
      <rect x="3" y="5" width="18" height="15" rx="2" />
      <line x1="3" y1="9.5" x2="21" y2="9.5" />
      <line x1="9" y1="9.5" x2="9" y2="20" />
      <line x1="15" y1="9.5" x2="15" y2="20" />
    </>
  ),
  month: (
    <>
      <rect x="3" y="5" width="18" height="16" rx="2" />
      <line x1="3" y1="10" x2="21" y2="10" />
      <line x1="8" y1="2.5" x2="8" y2="6" />
      <line x1="16" y1="2.5" x2="16" y2="6" />
    </>
  ),
  all: (
    <>
      <line x1="8" y1="7" x2="20" y2="7" />
      <line x1="8" y1="12" x2="20" y2="12" />
      <line x1="8" y1="17" x2="20" y2="17" />
      <circle cx="4" cy="7" r="1" />
      <circle cx="4" cy="12" r="1" />
      <circle cx="4" cy="17" r="1" />
    </>
  ),
  attention: (
    <>
      <path d="M12 3.5 21 19H3z" />
      <line x1="12" y1="9.5" x2="12" y2="14" />
      <circle cx="12" cy="16.6" r="0.6" fill="currentColor" />
    </>
  ),
  teams: (
    <>
      <circle cx="9" cy="8" r="3" />
      <path d="M3.5 19c0-3 2.5-5 5.5-5s5.5 2 5.5 5" />
      <path d="M16 6.2a3 3 0 0 1 0 5.6" />
      <path d="M17 14.2c2.2.5 3.8 2.3 3.8 4.8" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6" />
      <line x1="15" y1="15" x2="20" y2="20" />
    </>
  ),
  backlog: (
    <>
      <rect x="3.5" y="7" width="17" height="13" rx="2" />
      <path d="M3.5 7 5.2 3.6A1.6 1.6 0 0 1 6.6 3h10.8a1.6 1.6 0 0 1 1.4.6L20.5 7" />
      <line x1="9.5" y1="12.5" x2="14.5" y2="12.5" />
    </>
  ),
};

function NavIcon({ name }: { name: string }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      {NAV_ICON_PATHS[name]}
    </svg>
  );
}

type NavKey = "day" | "week" | "month" | "backlog" | "all" | "attention" | "teams" | "search";

interface NavItem {
  key: NavKey;
  name: string;
  icon: string;
}

const NAV_GROUPS: { label: string; items: NavItem[] }[] = [
  {
    label: "Plan",
    items: [
      { key: "day", name: "Day", icon: "day" },
      { key: "week", name: "Week", icon: "week" },
      { key: "month", name: "Month", icon: "month" },
      // ADMIN-only (docs/DESIGN_V7_BACKLOG.md) — the render loop below filters
      // this one item out for USER; the server enforces the boundary
      // regardless (requireAdmin on GET /api/backlog).
      { key: "backlog", name: "Backlog", icon: "backlog" },
    ],
  },
  {
    label: "Review",
    items: [
      { key: "all", name: "All tasks", icon: "all" },
      { key: "attention", name: "Attention", icon: "attention" },
    ],
  },
  {
    label: "Manage",
    items: [
      { key: "teams", name: "Teams", icon: "teams" },
      { key: "search", name: "Search", icon: "search" },
    ],
  },
];

// Deterministic per-person avatar tone (docs/DESIGN_V3_TEAMS.md "Avatar
// tones") — identical copy to views/TeamPage.tsx, views/TeamsLanding.tsx and
// components/TaskRow.tsx, each of which computes it locally per the
// file-ownership split rather than importing across view boundaries.
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
function initials(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

function KeyGlyph() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="8" cy="15" r="4.2" />
      <path d="M11 12 19 4" />
      <path d="M16 7l2.5 2.5" />
      <path d="M13.5 9.5 16 12" />
    </svg>
  );
}

function LogoutGlyph() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3" />
      <path d="M10 8l-4 4 4 4" />
      <line x1="6" y1="12" x2="16" y2="12" />
    </svg>
  );
}

const SIDEBAR_KEY = "taskman-sidebar-expanded";

export default function App() {
  const { user, logout } = useAuth();
  const [changePasswordOpen, setChangePasswordOpen] = useState(false);

  const [tab, setTab] = useState<NavKey>("day");

  const [expanded, setExpanded] = useState<boolean>(() => {
    try {
      return localStorage.getItem(SIDEBAR_KEY) !== "0";
    } catch {
      return true;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(SIDEBAR_KEY, expanded ? "1" : "0");
    } catch {
      // ignore
    }
  }, [expanded]);

  const [themeMode, setThemeModeState] = useState<ThemeMode>(() => readStoredTheme());
  useLayoutEffect(() => {
    applyTheme(themeMode);
  }, [themeMode]);
  function setThemeMode(mode: ThemeMode) {
    setThemeModeState(mode);
    try {
      if (mode === "system") localStorage.removeItem(THEME_KEY);
      else localStorage.setItem(THEME_KEY, mode);
    } catch {
      // ignore
    }
  }

  // Period state lives here (not in the individual views) because the app
  // shell header now owns the period-nav + quick-add per docs/DESIGN_V2_UI.md.
  const [dayDate, setDayDate] = useState(today());
  const [weekPeriod, setWeekPeriod] = useState(currentWeek());
  const [monthPeriod, setMonthPeriod] = useState(currentMonth());
  const [reloadToken, setReloadToken] = useState(0);

  const [attentionCount, setAttentionCount] = useState<number | null>(null);
  const refreshAttention = useCallback(() => {
    api
      .attentionView()
      .then((d) => setAttentionCount(d.overdue.length + d.slipped.length))
      .catch(() => {});
  }, []);
  useEffect(() => {
    refreshAttention();
  }, [refreshAttention]);
  useTasksChangedSubscription(refreshAttention);

  const [teamPageId, setTeamPageIdState] = useState<string | null>(() => readStoredTeam());
  function setTeamPageId(id: string | null) {
    setTeamPageIdState(id);
    try {
      if (id) localStorage.setItem(TEAM_KEY, id);
      else localStorage.removeItem(TEAM_KEY);
    } catch {
      // ignore
    }
  }

  // "N teams · M members" eyebrow (docs/DESIGN_V3_TEAMS.md). Refetched
  // whenever the Teams landing becomes visible (tab activates, or returning
  // from a team page via teamPageId -> null) and on notifyTasksChanged, so
  // team/member CRUD elsewhere doesn't leave the counts stale.
  const [teamCount, setTeamCount] = useState<number | null>(null);
  const [memberTotal, setMemberTotal] = useState<number | null>(null);
  const refreshTeamCounts = useCallback(() => {
    api
      .listTeams()
      .then((t) => setTeamCount(t.length))
      .catch(() => {});
    api
      .listMembers()
      .then((m) => setMemberTotal(m.length))
      .catch(() => {});
  }, []);
  useEffect(() => {
    refreshTeamCounts();
  }, [refreshTeamCounts]);
  useEffect(() => {
    if (tab === "teams" && teamPageId === null) refreshTeamCounts();
  }, [tab, teamPageId, refreshTeamCounts]);
  useTasksChangedSubscription(refreshTeamCounts);

  interface HeaderMeta {
    eyebrow: string;
    title: string;
    periodNav?: { onPrev: () => void; onToday: () => void; onNext: () => void };
    quickAdd?: { horizon: Horizon; periodLabel: string; placeholder: string };
  }

  const metaByTab: Record<NavKey, HeaderMeta> = {
    day: {
      eyebrow: formatDayEyebrow(dayDate),
      title: "Today",
      periodNav: {
        onPrev: () => setDayDate((d) => addDays(d, -1)),
        onToday: () => setDayDate(today()),
        onNext: () => setDayDate((d) => addDays(d, 1)),
      },
      quickAdd: { horizon: "daily", periodLabel: formatDayBadgePeriod(dayDate), placeholder: "Add a task for today…" },
    },
    week: {
      eyebrow: formatWeekLabel(weekPeriod),
      title: "This week",
      periodNav: {
        onPrev: () => setWeekPeriod((w) => addWeeks(w, -1)),
        onToday: () => setWeekPeriod(currentWeek()),
        onNext: () => setWeekPeriod((w) => addWeeks(w, 1)),
      },
      quickAdd: { horizon: "weekly", periodLabel: formatWeekBadgePeriod(weekPeriod), placeholder: "Add a weekly task…" },
    },
    month: {
      eyebrow: formatMonthLabel(monthPeriod),
      title: "This month",
      periodNav: {
        onPrev: () => setMonthPeriod((m) => addMonths(m, -1)),
        onToday: () => setMonthPeriod(currentMonth()),
        onNext: () => setMonthPeriod((m) => addMonths(m, 1)),
      },
      quickAdd: { horizon: "monthly", periodLabel: formatMonthLabel(monthPeriod), placeholder: "Add a monthly goal…" },
    },
    // No periodNav/quickAdd — BacklogView owns its own composer + "N parked"
    // count in its own body (mirrors AllTasksView's toolbar count), same
    // idiom as the all/attention/search entries below.
    backlog: { eyebrow: "Plan · Parked", title: "Backlog" },
    all: { eyebrow: "All horizons", title: "All tasks" },
    attention: { eyebrow: "Needs a decision", title: "Attention" },
    teams: {
      eyebrow:
        teamCount !== null && memberTotal !== null
          ? `${teamCount} team${teamCount === 1 ? "" : "s"} · ${memberTotal} member${memberTotal === 1 ? "" : "s"}`
          : "Teams",
      title: "Teams",
    },
    search: { eyebrow: "Filter everything", title: "Search" },
  };
  const meta = metaByTab[tab];
  // Team page renders its own in-content header (docs/DESIGN_V3_TEAMS.md) and
  // owns the full remaining height of <main> — the app-shell header and the
  // standard content-scroll/content-inner wrapper are skipped for it.
  const inTeamPage = tab === "teams" && !!teamPageId;

  return (
    <div className="app-shell">
      <aside className={`sidebar${expanded ? "" : " collapsed"}`}>
        <div className="sidebar-head">
          <button
            type="button"
            className="sidebar-logo"
            title={expanded ? "Collapse sidebar" : "Expand sidebar"}
            onClick={() => setExpanded((e) => !e)}
            style={{ fontFamily: "'Newsreader', serif" }}
          >
            t
          </button>
          {expanded && <span className="sidebar-word">taskman</span>}
          <button
            type="button"
            className="sidebar-collapse"
            title="Toggle sidebar"
            onClick={() => setExpanded((e) => !e)}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="4" width="18" height="16" rx="2" />
              <line x1="9" y1="4" x2="9" y2="20" />
            </svg>
          </button>
        </div>

        <nav className="sidebar-nav">
          {NAV_GROUPS.map((group) => (
            <div className="nav-group" key={group.label}>
              {expanded && <div className="nav-group-label">{group.label}</div>}
              {group.items
                .filter((item) => item.key !== "backlog" || user.systemRole === "ADMIN")
                .map((item) => {
                const active = tab === item.key;
                const showBadge = item.key === "attention" && !!attentionCount && attentionCount > 0;
                return (
                  <button
                    key={item.key}
                    type="button"
                    className={`nav-item${active ? " active" : ""}${expanded ? "" : " collapsed"}`}
                    title={item.name}
                    onClick={() => setTab(item.key)}
                  >
                    <span className="nav-icon">
                      <NavIcon name={item.icon} />
                    </span>
                    {expanded && <span className="nav-label">{item.name}</span>}
                    {showBadge && expanded && (
                      <span className={`nav-badge${active ? " active" : ""}`}>{attentionCount}</span>
                    )}
                  </button>
                );
              })}
            </div>
          ))}
        </nav>

        <div className={`sidebar-footer has-auth-identity${expanded ? "" : " collapsed"}`}>
          <div className="auth-identity">
            <span
              className="auth-avatar"
              style={{ background: AVATAR_TONES[toneIndex(user.id)].bg, color: AVATAR_TONES[toneIndex(user.id)].fg }}
              title={`${user.name} · ${user.systemRole}`}
            >
              {initials(user.name)}
            </span>
            {expanded && (
              <div className="auth-identity-id">
                <div className="auth-identity-name">{user.name}</div>
                <span className={`auth-role-badge${user.systemRole === "ADMIN" ? " admin" : ""}`}>{user.systemRole}</span>
              </div>
            )}
            <div className="auth-identity-actions">
              <button type="button" className="auth-icon-btn" title="Change password" onClick={() => setChangePasswordOpen(true)}>
                <KeyGlyph />
              </button>
              <button type="button" className="auth-icon-btn" title="Log out" onClick={logout}>
                <LogoutGlyph />
              </button>
            </div>
          </div>

          {expanded ? (
            <div className="theme-toggle">
              <div className="theme-toggle-label">Theme</div>
              <div className="theme-toggle-seg">
                <button type="button" className={themeMode === "system" ? "active" : ""} title="Follow system" onClick={() => setThemeMode("system")}>
                  Auto
                </button>
                <button type="button" className={themeMode === "light" ? "active" : ""} title="Light" onClick={() => setThemeMode("light")}>
                  Light
                </button>
                <button type="button" className={themeMode === "dark" ? "active" : ""} title="Dark" onClick={() => setThemeMode("dark")}>
                  Dark
                </button>
              </div>
            </div>
          ) : (
            <button
              type="button"
              className="theme-toggle-collapsed"
              title={`Theme: ${themeMode}`}
              onClick={() => setThemeMode(themeMode === "system" ? "light" : themeMode === "light" ? "dark" : "system")}
            >
              {themeMode === "system" ? "◐" : themeMode === "light" ? "☀" : "☾"}
            </button>
          )}
        </div>
      </aside>

      <main className="main">
        {!inTeamPage && (
          <header className="main-header">
            <div className="header-titles">
              <div className="eyebrow">{meta.eyebrow}</div>
              <h1 className="view-title" style={{ fontFamily: "'Newsreader', serif" }}>
                {meta.title}
              </h1>
            </div>

            <div className="header-controls">
              {meta.periodNav && (
                <div className="period-seg">
                  <button type="button" title="Previous" onClick={meta.periodNav.onPrev}>
                    ‹
                  </button>
                  <button type="button" className="today-btn" onClick={meta.periodNav.onToday}>
                    Today
                  </button>
                  <button type="button" title="Next" onClick={meta.periodNav.onNext}>
                    ›
                  </button>
                </div>
              )}
              {meta.quickAdd && (
                // Single TaskComposer instance shared across the Day/Week/Month
                // tabs (docs/DESIGN_V31_COMPOSER.md "v3.2"): this JSX position
                // stays mounted continuously across those three tabs (only
                // `meta.quickAdd`'s presence gates it, same as the old
                // QuickAdd), so React preserves the component's draft state
                // (title/notes/pills/expanded) across tab switches — only the
                // horizon/periods/periodLabel/placeholder PROPS change per
                // active tab, live-updating the collapsed badge. `periods`
                // carries ALL three currently-viewed periods (not just the
                // active tab's) so an overridden horizon — via a #token or
                // recurrence coupling — resolves its period from THAT tab's
                // own viewed period, exact parity with the old App
                // handleQuickAdd derivation from dayDate/weekPeriod/monthPeriod.
                <div className="header-quickadd">
                  <TaskComposer
                    context="personal"
                    horizon={meta.quickAdd.horizon}
                    // backlog: "" is a type-completeness filler only — the
                    // personal composer's horizon is always daily/weekly/
                    // monthly here, so this key is never actually read
                    // (docs/DESIGN_V7_BACKLOG.md; api.ts's Horizon gained
                    // "backlog", which widens Record<Horizon, string>).
                    periods={{ daily: dayDate, weekly: weekPeriod, monthly: monthPeriod, backlog: "" }}
                    periodLabel={meta.quickAdd.periodLabel}
                    placeholder={meta.quickAdd.placeholder}
                    onCreated={() => setReloadToken((v) => v + 1)}
                  />
                </div>
              )}
            </div>
          </header>
        )}

        {inTeamPage ? (
          <TeamPage teamId={teamPageId!} onBack={() => setTeamPageId(null)} />
        ) : (
          <div className="content-scroll">
            <div className="content-inner">
              {tab === "day" && <DayView date={dayDate} reloadToken={reloadToken} />}
              {tab === "week" && <WeekView week={weekPeriod} reloadToken={reloadToken} />}
              {tab === "month" && <MonthView month={monthPeriod} reloadToken={reloadToken} />}
              {tab === "backlog" && <BacklogView />}
              {tab === "all" && <AllTasksView />}
              {tab === "attention" && <AttentionView />}
              {tab === "teams" && <TeamsLanding onOpenTeam={setTeamPageId} />}
              {tab === "search" && <SearchView />}
            </div>
          </div>
        )}
      </main>

      <ToastHost />
      {changePasswordOpen && <ChangePasswordPanel onClose={() => setChangePasswordOpen(false)} />}
    </div>
  );
}
