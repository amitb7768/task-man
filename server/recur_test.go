package main

import (
	"reflect"
	"testing"
)

// intPtr is a test convenience for populating Recurrence.Interval (*int).
func intPtr(n int) *int { return &n }

func TestValidateRecurrence(t *testing.T) {
	cases := []struct {
		name    string
		r       Recurrence
		wantErr bool
	}{
		{"daily ok", Recurrence{Freq: FreqDaily}, false},
		{"weekly ok", Recurrence{Freq: FreqWeekly}, false},
		{"monthly ok", Recurrence{Freq: FreqMonthly}, false},
		{"monthly with dayOfMonth ok", Recurrence{Freq: FreqMonthly, DayOfMonth: 15}, false},
		{"weekdays ok", Recurrence{Freq: FreqWeekdays, Weekdays: []int{1, 3, 5}}, false},
		{"weekdays missing list", Recurrence{Freq: FreqWeekdays}, true},
		{"weekdays out of range", Recurrence{Freq: FreqWeekdays, Weekdays: []int{0}}, true},
		{"weekdays out of range high", Recurrence{Freq: FreqWeekdays, Weekdays: []int{8}}, true},
		{"invalid freq", Recurrence{Freq: "yearly"}, true},
		{"weekdays list on non-weekdays freq", Recurrence{Freq: FreqDaily, Weekdays: []int{1}}, true},
		{"dayOfMonth on non-monthly freq", Recurrence{Freq: FreqDaily, DayOfMonth: 5}, true},
		{"dayOfMonth out of range", Recurrence{Freq: FreqMonthly, DayOfMonth: 32}, true},
		{"interval absent ok", Recurrence{Freq: FreqDaily}, false},
		{"interval 1 ok", Recurrence{Freq: FreqDaily, Interval: intPtr(1)}, false},
		{"interval 2 ok", Recurrence{Freq: FreqWeekly, Interval: intPtr(2)}, false},
		{"interval 0 invalid", Recurrence{Freq: FreqDaily, Interval: intPtr(0)}, true},
		{"interval negative invalid", Recurrence{Freq: FreqMonthly, Interval: intPtr(-1)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRecurrence(&c.r)
			if (err != nil) != c.wantErr {
				t.Errorf("validateRecurrence(%+v) err=%v, wantErr=%v", c.r, err, c.wantErr)
			}
		})
	}
}

func TestRecurrenceHorizon(t *testing.T) {
	cases := []struct {
		freq   string
		want   string
		wantOK bool
	}{
		{FreqDaily, HorizonDaily, true},
		{FreqWeekdays, HorizonDaily, true},
		{FreqWeekly, HorizonWeekly, true},
		{FreqMonthly, HorizonMonthly, true},
		{"bogus", "", false},
	}
	for _, c := range cases {
		got, ok := recurrenceHorizon(c.freq)
		if got != c.want || ok != c.wantOK {
			t.Errorf("recurrenceHorizon(%q) = (%q,%v), want (%q,%v)", c.freq, got, ok, c.want, c.wantOK)
		}
	}
}

func TestMissingPeriodsDaily(t *testing.T) {
	r := &Recurrence{Freq: FreqDaily}
	got, err := missingPeriods(r, HorizonDaily, "2026-07-05", "2026-07-08")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []string{"2026-07-06", "2026-07-07", "2026-07-08"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missingPeriods = %v, want %v", got, want)
	}
}

func TestMissingPeriodsNoneWhenCurrent(t *testing.T) {
	r := &Recurrence{Freq: FreqDaily}
	got, err := missingPeriods(r, HorizonDaily, "2026-07-08", "2026-07-08")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missingPeriods = %v, want none (latest == current)", got)
	}
}

func TestMissingPeriodsWeekdays(t *testing.T) {
	// Mon/Wed/Fri only (1,3,5). From 2026-07-05 (Sun) through 2026-07-13 (Mon):
	// candidate dates 07-06(Mon) 07-07(Tue) 07-08(Wed) 07-09(Thu) 07-10(Fri)
	// 07-11(Sat) 07-12(Sun) 07-13(Mon) -> keep Mon/Wed/Fri/Mon.
	r := &Recurrence{Freq: FreqWeekdays, Weekdays: []int{1, 3, 5}}
	got, err := missingPeriods(r, HorizonDaily, "2026-07-05", "2026-07-13")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []string{"2026-07-06", "2026-07-08", "2026-07-10", "2026-07-13"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missingPeriods(weekdays) = %v, want %v", got, want)
	}
}

func TestMissingPeriodsWeekly(t *testing.T) {
	r := &Recurrence{Freq: FreqWeekly}
	got, err := missingPeriods(r, HorizonWeekly, "2026-W26", "2026-W28")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []string{"2026-W27", "2026-W28"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missingPeriods(weekly) = %v, want %v", got, want)
	}
}

func TestMissingPeriodsWeeklyYearRollover(t *testing.T) {
	r := &Recurrence{Freq: FreqWeekly}
	got, err := missingPeriods(r, HorizonWeekly, "2026-W52", "2027-W01")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []string{"2026-W53", "2027-W01"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missingPeriods(weekly year rollover) = %v, want %v", got, want)
	}
}

func TestMissingPeriodsMonthly(t *testing.T) {
	r := &Recurrence{Freq: FreqMonthly}
	got, err := missingPeriods(r, HorizonMonthly, "2026-10", "2027-01")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []string{"2026-11", "2026-12", "2027-01"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missingPeriods(monthly) = %v, want %v", got, want)
	}
}

// TestMissingPeriodsInterval extends the freq table above with
// anchor+interval cases (v2): interval 2/3 per freq, including a
// weekdays-with-interval case that crosses an ISO-year boundary
// (2026-W51..2027-W02, spanning 2026's W53).
func TestMissingPeriodsInterval(t *testing.T) {
	cases := []struct {
		name            string
		r               *Recurrence
		horizon         string
		latest, current string
		want            []string
	}{
		{
			name:    "daily interval 2",
			r:       &Recurrence{Freq: FreqDaily, Interval: intPtr(2), Anchor: "2026-07-01"},
			horizon: HorizonDaily,
			latest:  "2026-07-01",
			current: "2026-07-10",
			want:    []string{"2026-07-03", "2026-07-05", "2026-07-07", "2026-07-09"},
		},
		{
			name:    "daily interval 3",
			r:       &Recurrence{Freq: FreqDaily, Interval: intPtr(3), Anchor: "2026-07-01"},
			horizon: HorizonDaily,
			latest:  "2026-07-01",
			current: "2026-07-10",
			want:    []string{"2026-07-04", "2026-07-07", "2026-07-10"},
		},
		{
			name:    "weekly interval 2",
			r:       &Recurrence{Freq: FreqWeekly, Interval: intPtr(2), Anchor: "2026-W27"},
			horizon: HorizonWeekly,
			latest:  "2026-W27",
			current: "2026-W33",
			want:    []string{"2026-W29", "2026-W31", "2026-W33"},
		},
		{
			name:    "weekly interval 3",
			r:       &Recurrence{Freq: FreqWeekly, Interval: intPtr(3), Anchor: "2026-W27"},
			horizon: HorizonWeekly,
			latest:  "2026-W27",
			current: "2026-W33",
			want:    []string{"2026-W30", "2026-W33"},
		},
		{
			name:    "monthly interval 2",
			r:       &Recurrence{Freq: FreqMonthly, Interval: intPtr(2), Anchor: "2026-01"},
			horizon: HorizonMonthly,
			latest:  "2026-01",
			current: "2026-07",
			want:    []string{"2026-03", "2026-05", "2026-07"},
		},
		{
			name:    "monthly interval 3",
			r:       &Recurrence{Freq: FreqMonthly, Interval: intPtr(3), Anchor: "2026-01"},
			horizon: HorizonMonthly,
			latest:  "2026-01",
			current: "2026-07",
			want:    []string{"2026-04", "2026-07"},
		},
		{
			// Anchor week 2026-W27 (Mon 2026-06-29); Mon/Wed/Fri only every
			// 3rd occurrence-week: W27 (offset 0), W30 (offset 3).
			name:    "weekdays interval 3 same year",
			r:       &Recurrence{Freq: FreqWeekdays, Weekdays: []int{1, 3, 5}, Interval: intPtr(3), Anchor: "2026-W27"},
			horizon: HorizonDaily,
			latest:  "2026-06-29",
			current: "2026-07-24",
			want:    []string{"2026-07-01", "2026-07-03", "2026-07-20", "2026-07-22", "2026-07-24"},
		},
		{
			// Anchor week 2026-W51 (Mon 2026-12-14); Mon/Fri only every 2nd
			// occurrence-week: W51(0), W53(2), 2027-W02(4) — crossing the
			// ISO year boundary through 2026's 53rd week.
			name:    "weekdays interval 2 crossing year boundary",
			r:       &Recurrence{Freq: FreqWeekdays, Weekdays: []int{1, 5}, Interval: intPtr(2), Anchor: "2026-W51"},
			horizon: HorizonDaily,
			latest:  "2026-12-14",
			current: "2027-01-15",
			want:    []string{"2026-12-18", "2026-12-28", "2027-01-01", "2027-01-11", "2027-01-15"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := missingPeriods(c.r, c.horizon, c.latest, c.current)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("missingPeriods(%+v, %q, %q, %q) = %v, want %v", c.r, c.horizon, c.latest, c.current, got, c.want)
			}
		})
	}
}

func TestComputeAnchor(t *testing.T) {
	cases := []struct {
		freq, period, want string
	}{
		{FreqDaily, "2026-07-08", "2026-07-08"},
		{FreqWeekly, "2026-W28", "2026-W28"},
		{FreqMonthly, "2026-07", "2026-07"},
		{FreqWeekdays, "2026-07-08", "2026-W28"}, // date -> its ISO week
	}
	for _, c := range cases {
		got, err := computeAnchor(c.freq, c.period)
		if err != nil {
			t.Fatalf("computeAnchor(%q,%q) unexpected err: %v", c.freq, c.period, err)
		}
		if got != c.want {
			t.Errorf("computeAnchor(%q,%q) = %q, want %q", c.freq, c.period, got, c.want)
		}
	}
}

func TestEffectiveInterval(t *testing.T) {
	cases := []struct {
		name string
		r    *Recurrence
		want int
	}{
		{"absent", &Recurrence{Freq: FreqDaily}, 1},
		{"explicit 1", &Recurrence{Freq: FreqDaily, Interval: intPtr(1)}, 1},
		{"explicit 3", &Recurrence{Freq: FreqDaily, Interval: intPtr(3)}, 3},
	}
	for _, c := range cases {
		if got := effectiveInterval(c.r); got != c.want {
			t.Errorf("effectiveInterval(%s) = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestCloneRecurrenceSurvivesInPlaceUnmarshal reproduces the exact aliasing
// pitfall PatchTask depends on cloneRecurrence to avoid: encoding/json's
// Unmarshal decodes into an already-non-nil destination pointer in place
// (rather than allocating a new one), so a shallow struct copy of a
// *Recurrence still shares its Interval memory with the original. A clone
// must survive a subsequent mutation of the original's Interval unchanged.
func TestCloneRecurrenceSurvivesInPlaceUnmarshal(t *testing.T) {
	orig := &Recurrence{Freq: FreqDaily, Interval: intPtr(2)}
	clone := cloneRecurrence(orig)

	// Simulate what json.Unmarshal(raw, t) does to t.Recurrence.Interval
	// when the field is already non-nil: overwrite the pointee in place.
	*orig.Interval = 3

	if effectiveInterval(clone) != 2 {
		t.Errorf("clone.Interval mutated by aliasing: effectiveInterval(clone) = %d, want 2", effectiveInterval(clone))
	}
	if effectiveInterval(orig) != 3 {
		t.Fatalf("sanity check failed: effectiveInterval(orig) = %d, want 3", effectiveInterval(orig))
	}
}

func TestCloneRecurrenceNil(t *testing.T) {
	if got := cloneRecurrence(nil); got != nil {
		t.Errorf("cloneRecurrence(nil) = %v, want nil", got)
	}
}

func TestDueDateForSpawn(t *testing.T) {
	cases := []struct {
		name    string
		r       Recurrence
		horizon string
		period  string
		want    string
	}{
		{"monthly with dayOfMonth", Recurrence{Freq: FreqMonthly, DayOfMonth: 15}, HorizonMonthly, "2026-07", "2026-07-15"},
		{"monthly with dayOfMonth clamped", Recurrence{Freq: FreqMonthly, DayOfMonth: 31}, HorizonMonthly, "2026-02", "2026-02-28"},
		{"monthly without dayOfMonth", Recurrence{Freq: FreqMonthly}, HorizonMonthly, "2026-07", ""},
		{"daily freq never sets dueDate", Recurrence{Freq: FreqDaily}, HorizonDaily, "2026-07-08", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dueDateForSpawn(&c.r, c.horizon, c.period)
			if got != c.want {
				t.Errorf("dueDateForSpawn(%+v, %q, %q) = %q, want %q", c.r, c.horizon, c.period, got, c.want)
			}
		})
	}
}
