package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Horizons, in ascending granularity rank (smaller rank = finer-grained).
const (
	HorizonDaily   = "daily"
	HorizonWeekly  = "weekly"
	HorizonMonthly = "monthly"
	// HorizonBacklog is a sentinel horizon for unstaffed, personal-only
	// planning tasks (docs/DESIGN_V7_BACKLOG.md): always period "", never
	// teamId/dueDate/recurrence. It carries no granularity rank — deliberately
	// absent from horizonRank, since it never participates in parent/child
	// rank comparisons.
	HorizonBacklog = "backlog"
)

const dateLayout = "2006-01-02"

// horizonRank returns the granularity rank of a horizon (daily=1, weekly=2,
// monthly=3), and whether the horizon is valid.
func horizonRank(h string) (int, bool) {
	switch h {
	case HorizonDaily:
		return 1, true
	case HorizonWeekly:
		return 2, true
	case HorizonMonthly:
		return 3, true
	default:
		return 0, false
	}
}

// validHorizon reports whether h is one of the three recognized horizons or
// the backlog sentinel.
func validHorizon(h string) bool {
	if h == HorizonBacklog {
		return true
	}
	_, ok := horizonRank(h)
	return ok
}

// parseDate parses a "YYYY-MM-DD" string strictly (rejecting non-canonical
// forms by round-tripping the formatted output).
func parseDate(s string) (time.Time, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: %w", s, err)
	}
	if t.Format(dateLayout) != s {
		return time.Time{}, fmt.Errorf("invalid date %q", s)
	}
	return t, nil
}

// parseISOWeek parses a "YYYY-Www" string into its year and week number,
// validating that the week actually exists in that ISO year.
func parseISOWeek(s string) (year, week int, err error) {
	parts := strings.SplitN(s, "-W", 2)
	if len(parts) != 2 || len(parts[0]) != 4 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("invalid ISO week %q", s)
	}
	year, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid ISO week %q", s)
	}
	week, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid ISO week %q", s)
	}
	if week < 1 || week > 53 {
		return 0, 0, fmt.Errorf("invalid ISO week %q", s)
	}
	// Round-trip: compute the Monday of (year, week) and verify ISOWeek()
	// agrees. This rejects e.g. "2027-W53" (2027 only has 52 weeks) without
	// a separate weeks-in-year table.
	mon := isoWeekMonday(year, week)
	gotYear, gotWeek := mon.ISOWeek()
	if gotYear != year || gotWeek != week {
		return 0, 0, fmt.Errorf("invalid ISO week %q", s)
	}
	return year, week, nil
}

// isoWeekMonday returns the Monday of the given ISO (year, week).
func isoWeekMonday(year, week int) time.Time {
	// Jan 4 is always in ISO week 1 of its year.
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.Local)
	wd := int(jan4.Weekday())
	if wd == 0 {
		wd = 7 // Sunday -> 7
	}
	mondayWeek1 := jan4.AddDate(0, 0, -(wd - 1))
	return mondayWeek1.AddDate(0, 0, (week-1)*7)
}

// isoWeekString formats a time.Time as its "YYYY-Www" ISO week string.
func isoWeekString(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

// parseMonth parses a "YYYY-MM" string.
func parseMonth(s string) (year, month int, err error) {
	const layout = "2006-01"
	t, err := time.Parse(layout, s)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid month %q: %w", s, err)
	}
	if t.Format(layout) != s {
		return 0, 0, fmt.Errorf("invalid month %q", s)
	}
	return t.Year(), int(t.Month()), nil
}

// validatePeriod checks that period is well-formed for the given horizon.
func validatePeriod(horizon, period string) error {
	switch horizon {
	case HorizonDaily:
		_, err := parseDate(period)
		return err
	case HorizonWeekly:
		_, _, err := parseISOWeek(period)
		return err
	case HorizonMonthly:
		_, _, err := parseMonth(period)
		return err
	case HorizonBacklog:
		if period != "" {
			return fmt.Errorf("horizon %q requires an empty period", horizon)
		}
		return nil
	default:
		return fmt.Errorf("invalid horizon %q", horizon)
	}
}

// currentPeriodAt returns the period string for the given horizon that
// contains t. Pure function of t, for testability; currentPeriod wraps it
// with time.Now().
func currentPeriodAt(horizon string, t time.Time) string {
	switch horizon {
	case HorizonDaily:
		return t.Format(dateLayout)
	case HorizonWeekly:
		return isoWeekString(t)
	case HorizonMonthly:
		return t.Format("2006-01")
	default:
		return ""
	}
}

// currentPeriod returns "now"'s period string for the given horizon, in the
// machine's local timezone.
func currentPeriod(horizon string) string {
	return currentPeriodAt(horizon, time.Now())
}

// nextPeriod advances period by one unit of horizon. period must already be
// valid for horizon.
func nextPeriod(horizon, period string) (string, error) {
	switch horizon {
	case HorizonDaily:
		t, err := parseDate(period)
		if err != nil {
			return "", err
		}
		return t.AddDate(0, 0, 1).Format(dateLayout), nil
	case HorizonWeekly:
		year, week, err := parseISOWeek(period)
		if err != nil {
			return "", err
		}
		mon := isoWeekMonday(year, week).AddDate(0, 0, 7)
		return isoWeekString(mon), nil
	case HorizonMonthly:
		year, month, err := parseMonth(period)
		if err != nil {
			return "", err
		}
		t := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local).AddDate(0, 1, 0)
		return t.Format("2006-01"), nil
	default:
		return "", fmt.Errorf("invalid horizon %q", horizon)
	}
}

// daysBetween returns the number of days from date a to date b (b - a),
// which is negative if b is before a. Both must be valid "YYYY-MM-DD"
// dates.
func daysBetween(a, b string) (int, error) {
	ta, err := parseDate(a)
	if err != nil {
		return 0, err
	}
	tb, err := parseDate(b)
	if err != nil {
		return 0, err
	}
	return int(tb.Sub(ta).Hours() / 24), nil
}

// weeksBetween returns the number of ISO weeks from week a to week b (b -
// a), which is negative if b is before a. Both must be valid "YYYY-Www"
// ISO week strings.
func weeksBetween(a, b string) (int, error) {
	ya, wa, err := parseISOWeek(a)
	if err != nil {
		return 0, err
	}
	yb, wb, err := parseISOWeek(b)
	if err != nil {
		return 0, err
	}
	ma := isoWeekMonday(ya, wa)
	mb := isoWeekMonday(yb, wb)
	return int(mb.Sub(ma).Hours() / (24 * 7)), nil
}

// monthsBetween returns the number of months from month a to month b (b -
// a), which is negative if b is before a. Both must be valid "YYYY-MM"
// month strings.
func monthsBetween(a, b string) (int, error) {
	ya, moa, err := parseMonth(a)
	if err != nil {
		return 0, err
	}
	yb, mob, err := parseMonth(b)
	if err != nil {
		return 0, err
	}
	return (yb*12 + mob) - (ya*12 + moa), nil
}

// weekOfDate returns the ISO week string containing the given date.
func weekOfDate(date string) (string, error) {
	t, err := parseDate(date)
	if err != nil {
		return "", err
	}
	return isoWeekString(t), nil
}

// monthOfDate returns the "YYYY-MM" month string containing the given date.
func monthOfDate(date string) (string, error) {
	t, err := parseDate(date)
	if err != nil {
		return "", err
	}
	return t.Format("2006-01"), nil
}

// monthOfWeek returns the month "containing" an ISO week, using the
// Thursday-of-the-week convention (the same rule ISO 8601 uses to decide
// which year a week belongs to): a week that spans a month boundary is
// attributed to the month of its Thursday.
func monthOfWeek(week string) (string, error) {
	year, wk, err := parseISOWeek(week)
	if err != nil {
		return "", err
	}
	thu := isoWeekMonday(year, wk).AddDate(0, 0, 3)
	return thu.Format("2006-01"), nil
}

// datesInWeek returns the 7 date strings (Monday..Sunday) of an ISO week.
func datesInWeek(week string) ([7]string, error) {
	var out [7]string
	year, wk, err := parseISOWeek(week)
	if err != nil {
		return out, err
	}
	mon := isoWeekMonday(year, wk)
	for i := 0; i < 7; i++ {
		out[i] = mon.AddDate(0, 0, i).Format(dateLayout)
	}
	return out, nil
}

// weeksInMonth returns the ISO week strings that overlap the given month,
// in chronological order.
func weeksInMonth(month string) ([]string, error) {
	year, mo, err := parseMonth(month)
	if err != nil {
		return nil, err
	}
	first := time.Date(year, time.Month(mo), 1, 0, 0, 0, 0, time.Local)
	last := first.AddDate(0, 1, -1)

	var weeks []string
	seen := map[string]bool{}
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		w := isoWeekString(d)
		if !seen[w] {
			seen[w] = true
			weeks = append(weeks, w)
		}
	}
	return weeks, nil
}

// clampDayOfMonth returns the largest valid day <= day for the month
// containing t's year/month (e.g. day=31 in a 30-day month clamps to 30).
func dateForDayOfMonth(year, month, day int) time.Time {
	// First day of the *next* month, minus one day, gives the last day of
	// the target month.
	firstOfNext := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local).AddDate(0, 1, 0)
	lastDay := firstOfNext.AddDate(0, 0, -1).Day()
	if day > lastDay {
		day = lastDay
	}
	if day < 1 {
		day = 1
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
}
