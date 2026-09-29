// Package repo is the Postgres persistence layer: gorm over the schema in
// migrations/0001_init.up.sql. It is the behavior-identical replacement for
// server/store.go's Mongo implementation (contract:
// docs/DESIGN_PG_FSM_MIGRATION.md) — method-for-method, same validation
// order, same error statuses, same JSON-visible results.
package repo

import (
	"fmt"
	"net/url"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	fsm "taskman/internal/fsm/core"
)

// Store is the Postgres-backed store. One instance per process; safe for
// concurrent use (gorm.DB is a connection pool).
type Store struct {
	db *gorm.DB
	// taskFSM routes every task status change (see fsm.go). Built once here.
	taskFSM *fsm.Manager
}

// New wraps an open gorm handle. Callers run Migrate(dsn) first.
//
// It panics if the embedded transitions/task.yaml fails to load — a build
// defect (the YAML is compiled in and covered by the loader tests), not a
// runtime condition.
func New(db *gorm.DB) *Store {
	mgr, err := newTaskManager(db)
	if err != nil {
		panic(fmt.Sprintf("repo: load task FSM: %v", err))
	}
	return newWithManager(db, mgr)
}

// newWithManager is the test seam: a Store over db with a caller-built task
// FSM (e.g. a restricted or instrumented transition table).
func newWithManager(db *gorm.DB, mgr *fsm.Manager) *Store {
	return &Store{db: db, taskFSM: mgr}
}

// Open opens a gorm Postgres handle on dsn. SQL logging is off: the server
// has its own request log, and every repo error is returned to the caller.
//
// The session TimeZone is forced to UTC so every timestamptz reads back in
// UTC — the Mongo driver decoded UTC, so this keeps read-path JSON in its
// historical "…Z" form across ALL repos (create/patch responses still carry
// the in-memory local `now`, also Mongo parity). pgx passes unknown URL
// query params to the server as runtime session parameters.
func Open(dsn string) (*gorm.DB, error) {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		q := u.Query()
		if q.Get("TimeZone") == "" && q.Get("timezone") == "" {
			q.Set("TimeZone", "UTC")
			u.RawQuery = q.Encode()
			dsn = u.String()
		}
	}
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

// APIError carries an HTTP status through the repo boundary. It mirrors
// server/store.go's apiError; the server's writeErr maps it on the way out.
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
