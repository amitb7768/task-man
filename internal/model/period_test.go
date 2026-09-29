package model

import (
	"testing"
	"time"
)

func TestValidatePeriod(t *testing.T) {
	cases := []struct {
		name    string
		horizon string
		period  string
		wantErr bool
	}{
		{"daily ok", HorizonDaily, "2026-07-08", false},
		{"daily bad month", HorizonDaily, "2026-13-01", true},
		{"daily wrong format", HorizonDaily, "07-08-2026", true},
		{"weekly ok", HorizonWeekly, "2026-W28", false},
		{"weekly ok year end", HorizonWeekly, "2026-W53", false},          // 2026 has 53 ISO weeks
		{"weekly invalid week for year", HorizonWeekly, "2027-W53", true}, // 2027 has 52
		{"weekly week 0", HorizonWeekly, "2026-W00", true},
		{"weekly bad format no zero pad", HorizonWeekly, "2026-W5", true},
		{"monthly ok", HorizonMonthly, "2026-07", false},
		{"monthly bad month", HorizonMonthly, "2026-13", true},
		{"invalid horizon", "yearly", "2026", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePeriod(c.horizon, c.period)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidatePeriod(%q, %q) err=%v, wantErr=%v", c.horizon, c.period, err, c.wantErr)
			}
		})
	}
}

func TestHorizonRank(t *testing.T) {
	cases := []struct {
		horizon  string
		wantRank int
		wantOK   bool
	}{
		{HorizonDaily, 1, true},
		{HorizonWeekly, 2, true},
		{HorizonMonthly, 3, true},
		{"bogus", 0, false},
	}
	for _, c := range cases {
		rank, ok := HorizonRank(c.horizon)
		if rank != c.wantRank || ok != c.wantOK {
			t.Errorf("HorizonRank(%q) = (%d,%v), want (%d,%v)", c.horizon, rank, ok, c.wantRank, c.wantOK)
		}
	}
}

func TestCurrentPeriodAt(t *testing.T) {
	// Monday 2026-07-06 is in ISO week 2026-W28, month 2026-07.
	ref := time.Date(2026, 7, 8, 15, 30, 0, 0, time.UTC)
	cases := []struct {
		horizon string
		want    string
	}{
		{HorizonDaily, "2026-07-08"},
		{HorizonWeekly, "2026-W28"},
		{HorizonMonthly, "2026-07"},
	}
	for _, c := range cases {
		got := CurrentPeriodAt(c.horizon, ref)
		if got != c.want {
			t.Errorf("CurrentPeriodAt(%q, %v) = %q, want %q", c.horizon, ref, got, c.want)
		}
	}
}

func TestNextPeriod(t *testing.T) {
	cases := []struct {
		name    string
		horizon string
		period  string
		want    string
	}{
		{"daily simple", HorizonDaily, "2026-07-08", "2026-07-09"},
		{"daily month rollover", HorizonDaily, "2026-07-31", "2026-08-01"},
		{"daily year rollover", HorizonDaily, "2026-12-31", "2027-01-01"},
		{"daily leap day", HorizonDaily, "2028-02-28", "2028-02-29"},
		{"weekly simple", HorizonWeekly, "2026-W27", "2026-W28"},
		{"weekly year rollover", HorizonWeekly, "2026-W53", "2027-W01"},
		{"monthly simple", HorizonMonthly, "2026-07", "2026-08"},
		{"monthly year rollover", HorizonMonthly, "2026-12", "2027-01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NextPeriod(c.horizon, c.period)
			if err != nil {
				t.Fatalf("NextPeriod(%q,%q) unexpected err: %v", c.horizon, c.period, err)
			}
			if got != c.want {
				t.Errorf("NextPeriod(%q,%q) = %q, want %q", c.horizon, c.period, got, c.want)
			}
		})
	}
}

func TestWeekOfDate(t *testing.T) {
	cases := []struct {
		date string
		want string
	}{
		{"2026-07-06", "2026-W28"}, // Monday
		{"2026-07-12", "2026-W28"}, // Sunday, same week
		{"2026-07-13", "2026-W29"}, // next Monday
		// ISO year-boundary edge case: Jan 1 2027 is a Friday, which falls
		// in the last ISO week of 2026 (week 53).
		{"2027-01-01", "2026-W53"},
		{"2026-12-28", "2026-W53"}, // Monday of that same week
	}
	for _, c := range cases {
		got, err := WeekOfDate(c.date)
		if err != nil {
			t.Fatalf("WeekOfDate(%q) unexpected err: %v", c.date, err)
		}
		if got != c.want {
			t.Errorf("WeekOfDate(%q) = %q, want %q", c.date, got, c.want)
		}
	}
}

func TestDatesInWeek(t *testing.T) {
	got, err := DatesInWeek("2026-W28")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := [7]string{
		"2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09",
		"2026-07-10", "2026-07-11", "2026-07-12",
	}
	if got != want {
		t.Errorf("DatesInWeek(2026-W28) = %v, want %v", got, want)
	}
}

func TestMonthOfWeek(t *testing.T) {
	cases := []struct {
		week string
		want string
	}{
		{"2026-W28", "2026-07"},
		// A week straddling a month boundary: 2026-06-29 (Mon) .. 2026-07-05
		// (Sun) is ISO week 2026-W27; its Thursday (2026-07-02) is in July,
		// so by the Thursday convention this week belongs to July.
		{"2026-W27", "2026-07"},
	}
	for _, c := range cases {
		got, err := MonthOfWeek(c.week)
		if err != nil {
			t.Fatalf("MonthOfWeek(%q) unexpected err: %v", c.week, err)
		}
		if got != c.want {
			t.Errorf("MonthOfWeek(%q) = %q, want %q", c.week, got, c.want)
		}
	}
}

func TestWeeksInMonth(t *testing.T) {
	// July 2026: Jul 1 is a Wednesday (week 2026-W27), Jul 31 is a Friday.
	got, err := WeeksInMonth("2026-07")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []string{"2026-W27", "2026-W28", "2026-W29", "2026-W30", "2026-W31"}
	if len(got) != len(want) {
		t.Fatalf("WeeksInMonth(2026-07) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("WeeksInMonth(2026-07)[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDaysBetween(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026-07-01", "2026-07-01", 0},
		{"2026-07-01", "2026-07-09", 8},
		{"2026-07-09", "2026-07-01", -8}, // negative when b is before a
		{"2026-12-31", "2027-01-01", 1},  // year rollover
	}
	for _, c := range cases {
		got, err := DaysBetween(c.a, c.b)
		if err != nil {
			t.Fatalf("DaysBetween(%q,%q) unexpected err: %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("DaysBetween(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if _, err := DaysBetween("bogus", "2026-07-01"); err == nil {
		t.Error("DaysBetween with invalid date: want error, got nil")
	}
}

func TestWeeksBetween(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026-W27", "2026-W27", 0},
		{"2026-W27", "2026-W33", 6},
		{"2026-W33", "2026-W27", -6},
		// ISO year-boundary: 2026 has a W53.
		{"2026-W52", "2027-W01", 2},
	}
	for _, c := range cases {
		got, err := WeeksBetween(c.a, c.b)
		if err != nil {
			t.Fatalf("WeeksBetween(%q,%q) unexpected err: %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("WeeksBetween(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if _, err := WeeksBetween("bogus", "2026-W27"); err == nil {
		t.Error("WeeksBetween with invalid week: want error, got nil")
	}
}

func TestMonthsBetween(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026-01", "2026-01", 0},
		{"2026-01", "2026-07", 6},
		{"2026-07", "2026-01", -6},
		{"2026-12", "2027-01", 1}, // year rollover
	}
	for _, c := range cases {
		got, err := MonthsBetween(c.a, c.b)
		if err != nil {
			t.Fatalf("MonthsBetween(%q,%q) unexpected err: %v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("MonthsBetween(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if _, err := MonthsBetween("bogus", "2026-07"); err == nil {
		t.Error("MonthsBetween with invalid month: want error, got nil")
	}
}

func TestDateForDayOfMonth(t *testing.T) {
	cases := []struct {
		year, month, day int
		want             string
	}{
		{2026, 2, 31, "2026-02-28"}, // clamp to Feb 28 (2026 not leap)
		{2028, 2, 31, "2028-02-29"}, // clamp to Feb 29 (2028 leap)
		{2026, 4, 31, "2026-04-30"}, // clamp to Apr 30
		{2026, 7, 15, "2026-07-15"}, // no clamp needed
	}
	for _, c := range cases {
		got := DateForDayOfMonth(c.year, c.month, c.day).Format(DateLayout)
		if got != c.want {
			t.Errorf("DateForDayOfMonth(%d,%d,%d) = %q, want %q", c.year, c.month, c.day, got, c.want)
		}
	}
}
