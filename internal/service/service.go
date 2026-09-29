// Package service owns taskman's business flows: validation, derivation,
// scoping/authz, the task FSM, materialization, summary — each method runs
// its own queries/transactions over the gorm handle internal/repo opens
// (docs/DESIGN_PG_FSM_MIGRATION.md, P3). It is the behavior-identical
// successor of the Mongo-era server/store.go — same validation order, same
// error statuses, same JSON-visible results. Business errors cross the
// boundary as *APIError; internal/httpapi maps them to HTTP.
package service

import (
	"fmt"

	"gorm.io/gorm"

	fsm "taskman/internal/fsm/core"
)

// Service is the Postgres-backed store. One instance per process; safe for
// concurrent use (gorm.DB is a connection pool).
type Service struct {
	db *gorm.DB
	// taskFSM routes every task status change (see fsm.go). Built once here.
	taskFSM *fsm.Manager
}

// New wraps an open gorm handle (repo.Open). Callers run repo.Migrate(dsn)
// first.
//
// It panics if the embedded transitions/task.yaml fails to load — a build
// defect (the YAML is compiled in and covered by the loader tests), not a
// runtime condition.
func New(db *gorm.DB) *Service {
	mgr, err := newTaskManager(db)
	if err != nil {
		panic(fmt.Sprintf("service: load task FSM: %v", err))
	}
	return newWithManager(db, mgr)
}

// newWithManager is the test seam: a Service over db with a caller-built task
// FSM (e.g. a restricted or instrumented transition table).
func newWithManager(db *gorm.DB, mgr *fsm.Manager) *Service {
	return &Service{db: db, taskFSM: mgr}
}

// APIError is the business-error contract: it carries an HTTP status out of
// the service; internal/httpapi's writeErr maps it on the way out.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

func badRequest(format string, args ...any) error {
	return &APIError{400, fmt.Sprintf(format, args...)}
}
func notFoundErr(format string, args ...any) error {
	return &APIError{404, fmt.Sprintf(format, args...)}
}
func conflictErr(format string, args ...any) error {
	return &APIError{409, fmt.Sprintf(format, args...)}
}
func unauthorizedErr(format string, args ...any) error {
	return &APIError{401, fmt.Sprintf(format, args...)}
}
func forbiddenErr(format string, args ...any) error {
	return &APIError{403, fmt.Sprintf(format, args...)}
}
