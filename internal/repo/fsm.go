package repo

// Task status FSM wiring (docs/DESIGN_PG_FSM_MIGRATION.md, wave 2.2).
//
// Every PATCH that CHANGES a task's status runs through the copied ipd FSM
// engine (internal/fsm) inside the patch transaction; a PATCH that leaves
// status alone never enters it. The YAML (transitions/task.yaml) is
// permissive — all 12 ordered pairs legal — so wiring the engine changes no
// API-visible behavior: the hooks below perform exactly the writes
// patchTaskTx performed before (status UPDATE + the rest of the row + the
// task_activity status row), split across the engine's template steps.
//
// Post-commit actions, the outbox and state_history stay nil (see
// internal/fsm/README.md): task_activity is the single audit sink.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	fsm "taskman/internal/fsm/core"
	"taskman/internal/fsm/loader"
	"taskman/internal/model"
)

// taskTransitionsFile is the embedded YAML the task Manager is built from.
const taskTransitionsFile = "task.yaml"

// systemActor is the FSM actor for a status change with no session user
// (direct repo callers / tests). The engine rejects an empty actor; the
// actor is never persisted or returned (by/byName on the activity row come
// from the ctx user, exactly as before), so this is invisible to the API.
const systemActor = "system"

// newTaskManager builds the task FSM over the embedded task.yaml, reading
// state through db (or, inside ExecuteTransition, through the patch tx).
func newTaskManager(db *gorm.DB) (*fsm.Manager, error) {
	ts, err := loader.NewFromFS(taskTransitionsFile)
	if err != nil {
		return nil, err
	}
	return newTaskManagerFrom(db, ts), nil
}

// newTaskManagerFrom wires a Manager over an arbitrary TransitionStore — the
// seam tests use to inject a restricted (or instrumented) transition table.
func newTaskManagerFrom(db *gorm.DB, ts fsm.TransitionStore) *fsm.Manager {
	return &fsm.Manager{
		Transitions: ts,
		States:      &taskStateStore{db: db},
		// Guards, StateHistoryRepo, PostCommit*, OutboxNotifier: nil by design.
	}
}

// taskEvents maps (current status -> requested status) to the task.yaml
// event (see that file's header comment for the event vocabulary).
var taskEvents = map[[2]string]string{
	{model.StatusTodo, model.StatusInProgress}: "start",
	{model.StatusTodo, model.StatusDone}:       "complete",
	{model.StatusTodo, model.StatusCancelled}:  "cancel",

	{model.StatusInProgress, model.StatusTodo}:      "revert",
	{model.StatusInProgress, model.StatusDone}:      "complete",
	{model.StatusInProgress, model.StatusCancelled}: "cancel",

	{model.StatusDone, model.StatusTodo}:       "reopen",
	{model.StatusDone, model.StatusInProgress}: "resume",
	{model.StatusDone, model.StatusCancelled}:  "cancel",

	{model.StatusCancelled, model.StatusTodo}:       "uncancel",
	{model.StatusCancelled, model.StatusInProgress}: "resume",
	{model.StatusCancelled, model.StatusDone}:       "complete",
}

// eventFor returns the FSM event for a status change from -> to. An unknown
// pair (including from == to, which never enters the FSM) is an error.
func eventFor(from, to string) (string, error) {
	if ev, ok := taskEvents[[2]string{from, to}]; ok {
		return ev, nil
	}
	return "", fmt.Errorf("fsm: no event for status change %q -> %q", from, to)
}

// taskStateStore reads tasks.status from Postgres. GetStateTx (the
// Manager's optional txStateStore extension) reads through the patch
// transaction, where the row is already locked FOR UPDATE.
type taskStateStore struct {
	db *gorm.DB
}

func (s *taskStateStore) GetState(ctx context.Context, id string) (fsm.State, error) {
	return s.GetStateTx(ctx, s.db, id)
}

func (s *taskStateStore) GetStateTx(ctx context.Context, tx *gorm.DB, id string) (fsm.State, error) {
	var status string
	res := tx.WithContext(ctx).Raw("SELECT status FROM tasks WHERE id = ?", id).Scan(&status)
	if res.Error != nil {
		return fsm.State{}, res.Error
	}
	if res.RowsAffected == 0 {
		return fsm.State{}, notFoundErr("task not found")
	}
	return fsm.State{Status: fsm.Status(status)}, nil
}

// taskPatchHooks performs a status-changing patch's writes inside the FSM's
// template steps. It closes over patchTaskTx's merged state: t is the fully
// merged/validated/derived task, from its pre-patch status, now the patch
// timestamp, u the session user (nil in direct repo calls).
type taskPatchHooks struct {
	fsm.NoopHooks

	t    *model.Task
	from string
	now  time.Time
	u    *model.CtxUser

	// entry is the status activity row WriteAudit inserted (Seq populated by
	// RETURNING) — appended to the response by the caller after success.
	entry *model.ActivityEntry
	// hookErr is the first error a hook returned, so the caller can surface
	// it unwrapped (identical to the pre-FSM error value).
	hookErr error
}

func (h *taskPatchHooks) fail(err error) error {
	if h.hookErr == nil {
		h.hookErr = err
	}
	return err
}

// Validate is permissive (no business rules at launch). It only asserts the
// engine resolved the target patchTaskTx asked for — a YAML row whose event
// leads somewhere else would otherwise write a status the client never sent.
func (h *taskPatchHooks) Validate(tc *fsm.TransitionContext) error {
	if string(tc.NewState.Status) != h.t.Status {
		return h.fail(fmt.Errorf("fsm: event %q from %q resolves to %q, want %q",
			tc.Event, tc.OldState.Status, tc.NewState.Status, h.t.Status))
	}
	return nil
}

// ApplyStateChange is the status write, compare-and-set on the old status.
// The row is locked FOR UPDATE by the patch tx, so 0 rows cannot happen —
// the CAS is the engine's concurrency-backstop convention.
func (h *taskPatchHooks) ApplyStateChange(tc *fsm.TransitionContext) error {
	res := tc.Tx.Exec("UPDATE tasks SET status = ? WHERE id = ? AND status = ?", h.t.Status, h.t.ID, h.from)
	if res.Error != nil {
		return h.fail(res.Error)
	}
	if res.RowsAffected == 0 {
		return h.fail(fsm.ErrConcurrentUpdate)
	}
	return nil
}

// UpdateOtherFields writes every other column exactly as the pre-FSM patch
// UPDATE did (Select("*") minus the immutable columns and status).
func (h *taskPatchHooks) UpdateOtherFields(tc *fsm.TransitionContext) error {
	res := tc.Tx.Model(&model.Task{ID: h.t.ID}).Select("*").Omit("id", "created_at", "status").Updates(h.t)
	if res.Error != nil {
		return h.fail(res.Error)
	}
	return nil
}

// WriteAudit appends the status activity row, built exactly as before.
func (h *taskPatchHooks) WriteAudit(tc *fsm.TransitionContext) error {
	e := statusActivity(h.t.ID, h.from, h.t.Status, h.now, h.u)
	if err := tc.Tx.Create(&e).Error; err != nil {
		return h.fail(err)
	}
	h.entry = &e
	return nil
}

// statusActivity builds the auto-logged status activity entry. u is nil in
// direct repo tests -> by/byName empty.
func statusActivity(taskID, from, to string, now time.Time, u *model.CtxUser) model.ActivityEntry {
	e := model.ActivityEntry{
		ID:     NewID(),
		TaskID: taskID,
		Kind:   model.ActivityStatus,
		Date:   localDate(now),
		At:     now,
		From:   from,
		To:     to,
	}
	if u != nil {
		uid := u.ID
		e.By, e.ByName = &uid, u.Name
	}
	return e
}

// transitionTaskStatus runs a status-changing patch's writes through the FSM
// inside tx (the engine nests a SAVEPOINT). On success it returns the
// inserted status activity entry. Error mapping:
//   - no event for the pair / no allowed transition -> 409 "illegal status
//     transition: <from> -> <to>" (unreachable with the permissive YAML)
//   - CAS backstop (fsm.ErrConcurrentUpdate) -> 409 "task changed
//     concurrently, retry" (unreachable under the row lock)
//   - any other hook error -> that error, unwrapped
func (s *Store) transitionTaskStatus(ctx context.Context, tx *gorm.DB, t *model.Task, from string, now time.Time, u *model.CtxUser) (*model.ActivityEntry, error) {
	illegal := conflictErr("illegal status transition: %s -> %s", from, t.Status)
	event, err := eventFor(from, t.Status)
	if err != nil {
		return nil, illegal
	}
	actor := systemActor
	if u != nil {
		actor = u.ID
	}
	h := &taskPatchHooks{t: t, from: from, now: now, u: u}
	if err := s.taskFSM.ExecuteTransition(ctx, tx, t.ID, event, actor, nil, h); err != nil {
		switch {
		case errors.Is(err, fsm.ErrNoTransition), errors.Is(err, fsm.ErrGuardFailed):
			return nil, illegal
		case errors.Is(err, fsm.ErrConcurrentUpdate):
			return nil, conflictErr("task changed concurrently, retry")
		case h.hookErr != nil:
			return nil, h.hookErr
		}
		var ae *APIError
		if errors.As(err, &ae) {
			return nil, ae
		}
		return nil, err
	}
	return h.entry, nil
}
