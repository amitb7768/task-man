package fsm

import "errors"

var (
	// ErrNoTransition indicates no allowed transition was found for the current state and event.
	ErrNoTransition = errors.New("no allowed transition for event")
	// ErrGuardFailed indicates a guard prevented a transition from executing.
	ErrGuardFailed = errors.New("guard validation failed")
	// ErrConcurrentUpdate indicates optimistic locking failure when persisting state.
	ErrConcurrentUpdate = errors.New("concurrent update detected")
	// ErrDuplicateEvent indicates the event ID was already processed (idempotency).
	ErrDuplicateEvent = errors.New("event already processed (idempotent)")
)
