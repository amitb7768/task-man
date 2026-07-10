// Hand-rolled quick-add token parser — no date library. Documented forms only
// (docs/DESIGN_V2_UI.md): unrecognized tokens are left in the title text.
//
//   !high !med !low            priority
//   ^today ^tomorrow           due date
//   ^mon..^sun                 due date (next occurrence of that weekday)
//   ^<mon><day>  (e.g. ^jul12) due date (this year, or next if already past)
//   ^YYYY-MM-DD                due date (literal)
//   @Name                      assignee (team context only — resolved by the caller)
//   *daily *weekly *monthly *weekdays   recurrence
//   #daily #weekly #monthly    horizon override
import type { Horizon, Priority, RecurrenceFreq } from "../api";
import { addDays, formatDate, parseDate, today as todayFn } from "../period";

export interface ParsedQuickAdd {
  title: string;
  priority?: Priority;
  dueDate?: string; // YYYY-MM-DD
  dueLabel?: string; // short human label for preview chips, e.g. "Today", "Fri", "Jul 12"
  assigneeName?: string;
  recurrence?: { freq: RecurrenceFreq; weekdays?: number[] };
  horizon?: Horizon;
}

const PRIORITY_MAP: Record<string, Priority> = {
  high: "high",
  h: "high",
  med: "medium",
  medium: "medium",
  m: "medium",
  low: "low",
  l: "low",
};

// index = JS Date#getUTCDay() (0 = Sunday .. 6 = Saturday)
const WEEKDAYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];
const MONTHS: Record<string, number> = {
  jan: 0,
  feb: 1,
  mar: 2,
  apr: 3,
  may: 4,
  jun: 5,
  jul: 6,
  aug: 7,
  sep: 8,
  oct: 9,
  nov: 10,
  dec: 11,
};
const HORIZONS: readonly string[] = ["daily", "weekly", "monthly"];
const RECUR_FREQS: readonly string[] = ["daily", "weekly", "monthly", "weekdays"];
// freq -> horizon of the tasks it spawns (mirrors server/recur.go recurrenceHorizon).
const RECUR_HORIZON: Record<string, Horizon> = {
  daily: "daily",
  weekdays: "daily",
  weekly: "weekly",
  monthly: "monthly",
};
// ISO Mon=1..Sun=7. *weekdays with no explicit day list means Mon-Fri.
const MON_FRI = [1, 2, 3, 4, 5];

function capitalize(s: string): string {
  return s.length ? s[0].toUpperCase() + s.slice(1) : s;
}

// Next date >= base whose weekday matches `target` (today itself if it matches).
function resolveWeekday(base: string, target: string): string {
  const idx = WEEKDAYS.indexOf(target);
  if (idx < 0) return base;
  const cur = parseDate(base).getUTCDay();
  const diff = (idx - cur + 7) % 7;
  return addDays(base, diff);
}

// This year's <mon><day>, rolled to next year if it has already passed.
function resolveMonthDay(base: string, mon: string, day: number): string | null {
  const m = MONTHS[mon];
  if (m === undefined) return null;
  const year = parseDate(base).getUTCFullYear();
  const candidate = new Date(Date.UTC(year, m, day));
  if (candidate.getUTCMonth() !== m) return null; // invalid day for that month
  const candidateStr = formatDate(candidate);
  if (candidateStr < base) {
    return formatDate(new Date(Date.UTC(year + 1, m, day)));
  }
  return candidateStr;
}

function isIsoDate(s: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return false;
  const [y, mo, d] = s.split("-").map(Number);
  if (mo < 1 || mo > 12 || d < 1 || d > 31) return false;
  const dt = new Date(Date.UTC(y, mo - 1, d));
  return dt.getUTCFullYear() === y && dt.getUTCMonth() === mo - 1 && dt.getUTCDate() === d;
}

export function parseQuickAdd(raw: string, base: string = todayFn()): ParsedQuickAdd {
  const words = raw.split(/\s+/).filter(Boolean);
  const titleWords: string[] = [];
  const result: ParsedQuickAdd = { title: "" };

  for (const w of words) {
    const lw = w.toLowerCase();
    let matched = false;

    if (w.length > 1 && w[0] === "!") {
      const p = PRIORITY_MAP[lw.slice(1)];
      if (p) {
        result.priority = p;
        matched = true;
      }
    } else if (w.length > 1 && w[0] === "@") {
      result.assigneeName = w.slice(1);
      matched = true;
    } else if (w.length > 1 && w[0] === "#") {
      const h = lw.slice(1);
      if (HORIZONS.includes(h)) {
        result.horizon = h as Horizon;
        matched = true;
      }
    } else if (w.length > 1 && w[0] === "*") {
      const f = lw.slice(1);
      if (RECUR_FREQS.includes(f)) {
        result.recurrence = { freq: f as RecurrenceFreq };
        if (f === "weekdays") result.recurrence.weekdays = MON_FRI;
        matched = true;
      }
    } else if (w.length > 1 && w[0] === "^") {
      const d = lw.slice(1);
      if (d === "today") {
        result.dueDate = base;
        result.dueLabel = "Today";
        matched = true;
      } else if (d === "tomorrow" || d === "tmrw") {
        result.dueDate = addDays(base, 1);
        result.dueLabel = "Tomorrow";
        matched = true;
      } else if (WEEKDAYS.includes(d)) {
        result.dueDate = resolveWeekday(base, d);
        result.dueLabel = capitalize(d);
        matched = true;
      } else if (isIsoDate(d)) {
        result.dueDate = d;
        result.dueLabel = d;
        matched = true;
      } else {
        const m = d.match(/^([a-z]{3})(\d{1,2})$/);
        if (m && MONTHS[m[1]] !== undefined) {
          const resolved = resolveMonthDay(base, m[1], Number(m[2]));
          if (resolved) {
            result.dueDate = resolved;
            result.dueLabel = `${capitalize(m[1])} ${m[2]}`;
            matched = true;
          }
        }
      }
    }

    if (!matched) titleWords.push(w);
  }

  // A recurrence token implies its horizon (server requires task.horizon ==
  // recurrence's horizon); it wins over both the view default and any
  // conflicting #override token (docs/DESIGN_V2_UI.md quick-add tokens).
  if (result.recurrence) {
    result.horizon = RECUR_HORIZON[result.recurrence.freq];
  }

  result.title = titleWords.join(" ").trim();
  return result;
}
