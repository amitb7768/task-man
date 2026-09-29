package fsm

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrPermanent, when returned (or wrapped) from PostCommitAction.Execute,
// instructs the background worker to dead-letter the action immediately
// without further retry attempts. Use it for validation errors, malformed
// payloads, missing references — anything that will not succeed on retry.
//
// DECOUPLING: taskman has no bgtask package, so this is a local sentinel
// (ipd aliases it to bgtask.ErrPermanent so both errors.Is checks match a
// single sentinel there; taskman has only the one, defined here).
//
// Usage:
//
//	if isValidationError(err) {
//	    return fmt.Errorf("%w: %s", fsmcore.ErrPermanent, err.Error())
//	}
var ErrPermanent = errors.New("fsm: permanent failure")

// TransitionStore provides allowed transitions for a domain.
type TransitionStore interface {
	// FindTransition returns the allowed transition for the provided state and event.
	// Implementations must first try an exact match (status + substatus) before falling back
	// to wildcard (matching status where FromSubstatus == "").
	FindTransition(ctx context.Context, from State, event string) (*Transition, error)

	// ListTransitions returns the immutable list of transitions for inspection or validation.
	ListTransitions() []Transition

	// GetHappyPathTemplate returns an ordered list of unique states representing the
	// happy-path progression. Cancel/error transitions (events starting with "cancel_")
	// and transitions to statuses containing "CANCELLED" are excluded.
	// The result is deduplicated by (ToStatus, ToSubstatus) in YAML definition order.
	GetHappyPathTemplate() []TemplateNode
}

// StateStore defines the persistence boundary the Manager depends on.
// Implementations can be backed by databases, caches, or in-memory maps for tests.
type StateStore interface {
	// GetState returns the current state persisted for a resource.
	GetState(ctx context.Context, resourceID string) (State, error)
}

// PostCommitAction defines the contract for a durable post-commit side effect.
// Implementations live in the postcommit package; this interface lives in core
// to avoid circular imports between core and postcommit.
//
// Each action must be idempotent — the worker may execute it more than once.
//
// FUTURE: For dependent actions that must execute in order, define them
// as a single composite Action. Ordering/grouping support can be added later.
type PostCommitAction interface {
	// Type returns the unique identifier for this action (e.g., "initiate_discharge_billing").
	Type() string

	// BuildPayload is called WITHIN the database transaction.
	// Capture any data needed for Execute. Return nil payload to skip.
	// Return an error to abort the entire transition (rolls back the transaction).
	BuildPayload(ctx *TransitionContext) (map[string]any, error)

	// Execute performs the actual side effect. Must be idempotent.
	Execute(ctx context.Context, payload map[string]any) error

	// MaxRetries returns the max retry attempts. 0 = unlimited, -1 = no retries.
	MaxRetries() int

	// RetryDelay returns the delay before the nth retry attempt (1-indexed).
	RetryDelay(attempt int) time.Duration
}

// PostCommitActionRegistry provides access to registered post-commit actions.
type PostCommitActionRegistry interface {
	// ActionsFor returns all actions registered for the given event.
	ActionsFor(event string) []PostCommitAction

	// GetByType returns the action implementation for the given type.
	GetByType(actionType string) PostCommitAction
}

// OutboxNotifier is a 1-method fan-out signal fired by the Manager after a
// successful state-change commit. Subscribed outbox workers wake immediately
// instead of waiting for the next polling tick. The signal carries no payload
// — workers re-read the outbox table via the existing claim path, so the
// outbox row remains the single source of truth and crash-recovery is
// preserved by the polling tick.
//
// Defined here (rather than in the outbox package) to avoid importing outbox
// from core.
type OutboxNotifier interface {
	Notify(aggregateType string)
}

// PostCommitActionRepository persists post-commit actions within transactions.
// This interface is consumed by the Manager to write actions atomically
// with the state change. The full Repository (with claim/retry) lives in
// the postcommit package.
type PostCommitActionRepository interface {
	// CreateAction persists a post-commit action within the given transaction.
	// queue is the bgtask worker partition (usually the FSM aggregate type).
	// xRequestID is the per-transition trace id (Manager populates it; empty
	// is stored as NULL as a defensive fallback for direct callers).
	CreateAction(ctx context.Context, tx *gorm.DB, queue, aggregateID, actionType, event string, payload map[string]any, maxRetries int, xRequestID string) error
}
