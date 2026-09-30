package httpapi

// types.go is what's left of the Mongo-era store.go
// (docs/DESIGN_PG_FSM_MIGRATION.md): business flows live in
// internal/service, the domain types in internal/model. The package keeps
// the old names as aliases so handlers read the same, plus a handler-level
// apiError for the errors raised before a request ever reaches the service
// (bad JSON, auth gates, content-type guard).

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"taskman/internal/model"
	"taskman/internal/service"
)

// Store is the business service the handlers call into.
type Store = service.Service

type (
	Task          = model.Task
	TaskView      = model.TaskView
	TaskDetail    = model.TaskDetail
	ActivityEntry = model.ActivityEntry
	Team          = model.Team
	Member        = model.Member
	Board         = model.Board
	NoteInput     = model.NoteInput
	SearchParams  = model.SearchParams
	SummaryParams = model.SummaryParams
	SummaryResult = model.SummaryResult
	SummaryTask   = model.SummaryTask
	Recurrence    = model.Recurrence
)

// Task statuses / roles / activity kinds / horizons.
const (
	StatusTodo       = model.StatusTodo
	StatusInProgress = model.StatusInProgress
	StatusDone       = model.StatusDone
	StatusCancelled  = model.StatusCancelled

	RoleAdmin = model.RoleAdmin
	RoleUser  = model.RoleUser

	ActivityNote   = model.ActivityNote
	ActivityStatus = model.ActivityStatus

	HorizonDaily   = model.HorizonDaily
	HorizonWeekly  = model.HorizonWeekly
	HorizonMonthly = model.HorizonMonthly
	HorizonBacklog = model.HorizonBacklog
)

// defaultHistoryLimit mirrors internal/service's (unexported) page-size default
// for GET /api/teams/{id}/history when no ?limit= is given; the service clamps
// out-of-range values itself.
const defaultHistoryLimit = 50

var currentPeriod = model.CurrentPeriod

// apiError carries an HTTP status alongside a message for handler-level
// errors; writeErr maps it (and the service's *service.APIError) to the response
// code, defaulting to 500 for anything else.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &apiError{400, fmt.Sprintf(format, args...)}
}
func unauthorizedErr(format string, args ...any) error {
	return &apiError{401, fmt.Sprintf(format, args...)}
}
func forbiddenErr(format string, args ...any) error {
	return &apiError{403, fmt.Sprintf(format, args...)}
}
func unsupportedMediaErr(format string, args ...any) error {
	return &apiError{415, fmt.Sprintf(format, args...)}
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.status, map[string]string{"error": ae.msg})
		return
	}
	var re *service.APIError
	if errors.As(err, &re) {
		writeJSON(w, re.Status, map[string]string{"error": re.Msg})
		return
	}
	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}
