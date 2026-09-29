package main

// recur.go recurrence math moved to internal/model/recur.go (wave 1.0,
// docs/DESIGN_PG_FSM_MIGRATION.md). This file is a thin shim so store.go
// and every other server/*.go file keep compiling unchanged: the old
// lowercase names now just forward to the exported model package.
//
// Tests moved to internal/model/recur_test.go (renamed to the exported
// calls below). model.Recurrence keeps its bson tags for now (the Mongo
// store still round-trips it through this alias — see the comment on
// model.Recurrence; removed when the Mongo store dies in wave 1.2).

import "taskman/internal/model"

// Recurrence describes a recurring-task preset, embedded on the live
// instance of a series.
type Recurrence = model.Recurrence

// Recurrence freqs.
const (
	FreqDaily    = model.FreqDaily
	FreqWeekdays = model.FreqWeekdays
	FreqWeekly   = model.FreqWeekly
	FreqMonthly  = model.FreqMonthly
)

var (
	recurrenceHorizon  = model.RecurrenceHorizon
	validateRecurrence = model.ValidateRecurrence
	effectiveInterval  = model.EffectiveInterval
	cloneRecurrence    = model.CloneRecurrence
	computeAnchor      = model.ComputeAnchor
	isOccurrence       = model.IsOccurrence
	missingPeriods     = model.MissingPeriods
	dueDateForSpawn    = model.DueDateForSpawn
)
