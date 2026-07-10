package main

import "fmt"

// Recurrence freqs.
const (
	FreqDaily    = "daily"
	FreqWeekdays = "weekdays"
	FreqWeekly   = "weekly"
	FreqMonthly  = "monthly"
)

// Recurrence describes a recurring-task preset, embedded on the live
// instance of a series.
type Recurrence struct {
	Freq     string `bson:"freq" json:"freq"`
	Interval *int   `bson:"interval,omitempty" json:"interval,omitempty"` // "every N periods"; absent == 1
	// Anchor is server-managed: the period occurrence math counts from.
	// Never client-settable (see computeAnchor / re-anchoring in store.go).
	Anchor     string `bson:"anchor,omitempty" json:"anchor,omitempty"`
	Weekdays   []int  `bson:"weekdays,omitempty" json:"weekdays,omitempty"`
	DayOfMonth int    `bson:"dayOfMonth,omitempty" json:"dayOfMonth,omitempty"`
}

// recurrenceHorizon maps a recurrence freq to the horizon of the tasks it
// spawns.
func recurrenceHorizon(freq string) (string, bool) {
	switch freq {
	case FreqDaily, FreqWeekdays:
		return HorizonDaily, true
	case FreqWeekly:
		return HorizonWeekly, true
	case FreqMonthly:
		return HorizonMonthly, true
	default:
		return "", false
	}
}

// validateRecurrence checks a Recurrence for internal consistency. It does
// not check horizon against the task's own horizon (spawnable freq always
// determines horizon; callers should set horizon = recurrenceHorizon(freq)).
func validateRecurrence(r *Recurrence) error {
	horizon, ok := recurrenceHorizon(r.Freq)
	if !ok {
		return fmt.Errorf("invalid recurrence freq %q", r.Freq)
	}
	if r.Freq == FreqWeekdays {
		if len(r.Weekdays) == 0 {
			return fmt.Errorf("recurrence freq %q requires weekdays", r.Freq)
		}
		for _, d := range r.Weekdays {
			if d < 1 || d > 7 {
				return fmt.Errorf("invalid weekday %d (must be 1..7, ISO Mon=1)", d)
			}
		}
	} else if len(r.Weekdays) > 0 {
		return fmt.Errorf("weekdays only valid with freq %q", FreqWeekdays)
	}
	if r.DayOfMonth != 0 {
		if r.Freq != FreqMonthly {
			return fmt.Errorf("dayOfMonth only valid with freq %q", FreqMonthly)
		}
		if r.DayOfMonth < 1 || r.DayOfMonth > 31 {
			return fmt.Errorf("invalid dayOfMonth %d", r.DayOfMonth)
		}
	}
	if r.Interval != nil && *r.Interval < 1 {
		return fmt.Errorf("interval must be >= 1")
	}
	_ = horizon
	return nil
}

// effectiveInterval returns r's occurrence interval, defaulting to 1 when
// absent (validateRecurrence already rejects a present-but-invalid value,
// so the <1 guard here is just defensive).
func effectiveInterval(r *Recurrence) int {
	if r.Interval == nil || *r.Interval < 1 {
		return 1
	}
	return *r.Interval
}

// cloneRecurrence returns an independent value copy of r, including its
// Interval sub-field — which is itself a pointer. A plain `rc := *r` copy
// would leave rc.Interval pointing at the *same* int as r.Interval; since
// encoding/json's Unmarshal decodes into an already-non-nil destination
// pointer in place (rather than allocating a new one), a subsequent
// json.Unmarshal onto r silently mutates the "clone" too. Used by PatchTask
// to snapshot the pre-patch recurrence before Unmarshal merges the patch in.
func cloneRecurrence(r *Recurrence) *Recurrence {
	if r == nil {
		return nil
	}
	rc := *r
	if r.Interval != nil {
		iv := *r.Interval
		rc.Interval = &iv
	}
	return &rc
}

// computeAnchor returns the server-managed anchor period for a recurrence
// (re)anchored at the given freq+period: the period itself, except for
// freq=weekdays where it's the ISO week containing that date (period is a
// date string for weekdays, since it spawns daily instances).
func computeAnchor(freq, period string) (string, error) {
	if freq == FreqWeekdays {
		return weekOfDate(period)
	}
	return period, nil
}

// isOccurrence reports whether period is a spawn-worthy occurrence of r,
// per the anchor+interval occurrence rule:
//
//	daily:    daysBetween(anchor, P)   % interval == 0
//	weekly:   weeksBetween(anchor, P)  % interval == 0
//	monthly:  monthsBetween(anchor, P) % interval == 0
//	weekdays: weekday(P) in weekdays AND weeksBetween(anchorWeek, isoWeek(P)) % interval == 0
func isOccurrence(r *Recurrence, period string, interval int) (bool, error) {
	switch r.Freq {
	case FreqDaily:
		return intervalMatches(r.Anchor, period, interval, daysBetween)
	case FreqWeekly:
		return intervalMatches(r.Anchor, period, interval, weeksBetween)
	case FreqMonthly:
		return intervalMatches(r.Anchor, period, interval, monthsBetween)
	case FreqWeekdays:
		if !weekdayMatches(period, r.Weekdays) {
			return false, nil
		}
		week, err := weekOfDate(period)
		if err != nil {
			return false, err
		}
		return intervalMatches(r.Anchor, week, interval, weeksBetween)
	default:
		return false, fmt.Errorf("invalid recurrence freq %q", r.Freq)
	}
}

// intervalMatches reports whether the distance from anchor to period (as
// computed by between) is a multiple of interval. An empty anchor — legacy
// data materialized before v2 gained anchors — always matches: with the
// interval it implies (1), every period is an occurrence, so no anchor is
// needed to decide that.
func intervalMatches(anchor, period string, interval int, between func(a, b string) (int, error)) (bool, error) {
	if anchor == "" {
		return true, nil
	}
	d, err := between(anchor, period)
	if err != nil {
		return false, err
	}
	return floorMod(d, interval) == 0, nil
}

// floorMod is the non-negative remainder of a/n (n <= 0 is treated as 1).
func floorMod(a, n int) int {
	if n <= 0 {
		n = 1
	}
	m := a % n
	if m < 0 {
		m += n
	}
	return m
}

// missingPeriods returns the periods (in chronological order) that should be
// spawned for a series, given the newest existing instance's period and
// "now"'s period for the horizon. Periods are strictly after latestPeriod
// and up to and including currentPeriod, filtered to occurrence periods per
// r's anchor+interval rule (isOccurrence) — which, for freq=weekdays, also
// requires the date's ISO weekday to appear in r.Weekdays.
//
// If latestPeriod >= currentPeriod there is nothing missing yet (returns nil).
func missingPeriods(r *Recurrence, horizon, latestPeriod, currentPeriod string) ([]string, error) {
	if latestPeriod >= currentPeriod {
		return nil, nil
	}
	interval := effectiveInterval(r)
	var out []string
	p, err := nextPeriod(horizon, latestPeriod)
	if err != nil {
		return nil, err
	}
	for p <= currentPeriod {
		ok, err := isOccurrence(r, p, interval)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
		p, err = nextPeriod(horizon, p)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// weekdayMatches reports whether date's ISO weekday (Mon=1..Sun=7) is in days.
func weekdayMatches(date string, days []int) bool {
	t, err := parseDate(date)
	if err != nil {
		return false
	}
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	for _, d := range days {
		if d == wd {
			return true
		}
	}
	return false
}

// dueDateForSpawn computes the dueDate of a newly-spawned monthly instance
// when the recurrence specifies dayOfMonth; returns "" otherwise (or for
// non-monthly freqs).
func dueDateForSpawn(r *Recurrence, horizon, period string) string {
	if horizon != HorizonMonthly || r.DayOfMonth == 0 {
		return ""
	}
	year, month, err := parseMonth(period)
	if err != nil {
		return ""
	}
	return dateForDayOfMonth(year, month, r.DayOfMonth).Format(dateLayout)
}
