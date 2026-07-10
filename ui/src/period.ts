// Period-string helpers: daily "YYYY-MM-DD", weekly ISO "YYYY-Www" (Monday start),
// monthly "YYYY-MM". All date arithmetic is done on UTC-anchored Date objects that
// represent a calendar date (not a wall-clock instant), so we never trip over the
// browser's local timezone/DST while adding/subtracting days. "today" itself is read
// from the local wall clock, since that's what the user means by "today".

const pad2 = (n: number): string => String(n).padStart(2, "0");

export function parseDate(s: string): Date {
  const [y, m, d] = s.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d));
}

export function formatDate(d: Date): string {
  return `${d.getUTCFullYear()}-${pad2(d.getUTCMonth() + 1)}-${pad2(d.getUTCDate())}`;
}

export function today(): string {
  const now = new Date();
  return `${now.getFullYear()}-${pad2(now.getMonth() + 1)}-${pad2(now.getDate())}`;
}

export function addDays(dateStr: string, delta: number): string {
  const d = parseDate(dateStr);
  d.setUTCDate(d.getUTCDate() + delta);
  return formatDate(d);
}

// ISO-8601 week: the week containing the year's first Thursday is week 1.
// Handles year-boundary edges (e.g. Jan 1 can fall in week 52/53 of the prior year).
export function toISOWeek(dateStr: string): string {
  const d = parseDate(dateStr);
  const dayNum = d.getUTCDay() || 7; // Mon=1..Sun=7
  d.setUTCDate(d.getUTCDate() + 4 - dayNum); // move to this ISO week's Thursday
  const yearStart = new Date(Date.UTC(d.getUTCFullYear(), 0, 1));
  const weekNo = Math.ceil(((d.getTime() - yearStart.getTime()) / 86400000 + 1) / 7);
  return `${d.getUTCFullYear()}-W${pad2(weekNo)}`;
}

export function currentWeek(): string {
  return toISOWeek(today());
}

export function isoWeekMonday(weekStr: string): Date {
  const [yearStr, weekNoStr] = weekStr.split("-W");
  const year = Number(yearStr);
  const week = Number(weekNoStr);
  // Jan 4 always falls in ISO week 1.
  const jan4 = new Date(Date.UTC(year, 0, 4));
  const jan4Day = jan4.getUTCDay() || 7;
  const week1Monday = new Date(jan4);
  week1Monday.setUTCDate(jan4.getUTCDate() - (jan4Day - 1));
  const monday = new Date(week1Monday);
  monday.setUTCDate(week1Monday.getUTCDate() + (week - 1) * 7);
  return monday;
}

export function weekDates(weekStr: string): string[] {
  const monday = isoWeekMonday(weekStr);
  const out: string[] = [];
  for (let i = 0; i < 7; i++) {
    const d = new Date(monday);
    d.setUTCDate(monday.getUTCDate() + i);
    out.push(formatDate(d));
  }
  return out;
}

export function addWeeks(weekStr: string, delta: number): string {
  const monday = isoWeekMonday(weekStr);
  monday.setUTCDate(monday.getUTCDate() + delta * 7);
  return toISOWeek(formatDate(monday));
}

export function currentMonth(): string {
  return today().slice(0, 7);
}

export function addMonths(monthStr: string, delta: number): string {
  const [y, m] = monthStr.split("-").map(Number);
  const d = new Date(Date.UTC(y, m - 1 + delta, 1));
  return `${d.getUTCFullYear()}-${pad2(d.getUTCMonth() + 1)}`;
}

export function formatDayLabel(dateStr: string): string {
  return parseDate(dateStr).toLocaleDateString(undefined, {
    weekday: "short",
    year: "numeric",
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

export function formatShortDayLabel(dateStr: string): string {
  return parseDate(dateStr).toLocaleDateString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

export function formatWeekLabel(weekStr: string): string {
  const monday = isoWeekMonday(weekStr);
  const sunday = new Date(monday);
  sunday.setUTCDate(monday.getUTCDate() + 6);
  const [, weekNo] = weekStr.split("-W");
  const fmt = (d: Date) =>
    d.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" });
  return `Week ${weekNo} · ${fmt(monday)} – ${fmt(sunday)}`;
}

export function formatMonthLabel(monthStr: string): string {
  const [y, m] = monthStr.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
    timeZone: "UTC",
  });
}

export function formatCompactDate(dateStr: string): string {
  return parseDate(dateStr).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

export function isOverdue(dueDate: string | undefined, status: string): boolean {
  if (!dueDate) return false;
  if (status === "done" || status === "cancelled") return false;
  return dueDate < today();
}

export const WEEKDAY_LABELS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

// ---- Additions for the v2 app shell (header eyebrow / quick-add badges) ----
// Kept separate from the longer-form labels above so existing call sites and
// their exact output strings are untouched.

// "Wednesday · Jul 8" — app-shell header eyebrow for the Day tab.
export function formatDayEyebrow(dateStr: string): string {
  const weekday = parseDate(dateStr).toLocaleDateString(undefined, {
    weekday: "long",
    timeZone: "UTC",
  });
  return `${weekday} · ${formatCompactDate(dateStr)}`;
}

// "Today · Jul 8" when the date is today, else just "Jul 8" — quick-add period badge.
export function formatDayBadgePeriod(dateStr: string): string {
  if (dateStr === today()) return `Today · ${formatCompactDate(dateStr)}`;
  return formatCompactDate(dateStr);
}

// "This week · W28" when the week is the current one, else "W28" — quick-add period badge.
export function formatWeekBadgePeriod(weekStr: string): string {
  const weekNo = weekStr.split("-W")[1] ?? weekStr;
  return weekStr === currentWeek() ? `This week · W${weekNo}` : `W${weekNo}`;
}

// "W28" — compact week label for chips (vs. the full "Week 28 · Jul 6 – 12").
export function formatWeekShort(weekStr: string): string {
  const weekNo = weekStr.split("-W")[1] ?? weekStr;
  return `W${weekNo}`;
}

// "Jul 2026" — compact month label for chips (vs. the full "July 2026").
export function formatMonthShort(monthStr: string): string {
  const [y, m] = monthStr.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString(undefined, {
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  });
}
