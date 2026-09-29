package main

// period.go pure date/period math moved to internal/model/period.go (wave
// 1.0, docs/DESIGN_PG_FSM_MIGRATION.md). This file is a thin shim so
// store.go and every other server/*.go file keep compiling unchanged: the
// old lowercase names now just forward to the exported model package.
//
// Tests moved to internal/model/period_test.go (renamed to the exported
// calls below).

import "taskman/internal/model"

// Horizons, in ascending granularity rank (smaller rank = finer-grained).
const (
	HorizonDaily   = model.HorizonDaily
	HorizonWeekly  = model.HorizonWeekly
	HorizonMonthly = model.HorizonMonthly
	HorizonBacklog = model.HorizonBacklog
)

const dateLayout = model.DateLayout

var (
	horizonRank       = model.HorizonRank
	validHorizon      = model.ValidHorizon
	parseDate         = model.ParseDate
	parseISOWeek      = model.ParseISOWeek
	isoWeekMonday     = model.ISOWeekMonday
	isoWeekString     = model.ISOWeekString
	parseMonth        = model.ParseMonth
	validatePeriod    = model.ValidatePeriod
	currentPeriodAt   = model.CurrentPeriodAt
	currentPeriod     = model.CurrentPeriod
	nextPeriod        = model.NextPeriod
	daysBetween       = model.DaysBetween
	weeksBetween      = model.WeeksBetween
	monthsBetween     = model.MonthsBetween
	weekOfDate        = model.WeekOfDate
	monthOfDate       = model.MonthOfDate
	monthOfWeek       = model.MonthOfWeek
	datesInWeek       = model.DatesInWeek
	weeksInMonth      = model.WeeksInMonth
	dateForDayOfMonth = model.DateForDayOfMonth
)
