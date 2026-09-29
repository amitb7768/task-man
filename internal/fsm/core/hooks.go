package fsm

// TransitionHooks defines the extension points for client-specific logic.
// Mandatory hooks must be implemented; optional hooks can embed NoopHooks.
type TransitionHooks interface {
	// === Mandatory Hooks ===

	// Validate checks business rules before any state change.
	// Return an error to abort the entire transition (triggers rollback).
	//
	// Use cases:
	//   - Check if patient has cleared billing before DISCHARGE_COMPLETE
	//   - Verify required documents are present
	//   - Validate business invariants
	Validate(ctx *TransitionContext) error

	// ApplyStateChange persists the new state to the database.
	// This is where the primary entity table gets updated.
	//
	// Use cases:
	//   - Update discharge_details.status and discharge_details.sub_status
	//   - Optimistic locking checks
	ApplyStateChange(ctx *TransitionContext) error

	// === Optional Hooks (embed NoopHooks for defaults) ===

	// UpdateOtherFields handles additional field updates beyond status.
	// Called after ApplyStateChange, within the same transaction.
	//
	// Use cases:
	//   - Set discharge_date when transitioning to DISCHARGE_COMPLETE
	//   - Update end_time, is_active flags
	//   - Set related entity fields
	UpdateOtherFields(ctx *TransitionContext) error

	// WriteOutbox creates outbox events for downstream consumers (Kafka).
	// Called within the same transaction to ensure atomicity.
	//
	// Use cases:
	//   - Publish state change events to Kafka via outbox pattern
	//   - Notify external systems of state changes
	WriteOutbox(ctx *TransitionContext) error

	// WriteAudit records the transition in the audit/history table.
	// Called within the same transaction.
	//
	// Use cases:
	//   - Create state_history record
	//   - Log actor and metadata for compliance
	WriteAudit(ctx *TransitionContext) error

	// PostCommit is called AFTER the transaction is committed.
	// This hook runs outside the transaction and is safe for async operations.
	// Errors from PostCommit are logged but do NOT rollback the transition.
	//
	// Use cases:
	//   - Trigger async bed billing
	//   - Send notifications
	//   - Start background jobs
	PostCommit(ctx *TransitionContext) error
}

// NoopHooks provides no-op implementations for optional hooks.
// Clients can embed this to avoid implementing all optional methods.
//
// Example usage:
//
//	type MyHooks struct {
//	    fsm.NoopHooks  // Provides defaults for optional hooks
//	}
//
//	func (h *MyHooks) Validate(ctx *fsm.TransitionContext) error { ... }
//	func (h *MyHooks) ApplyStateChange(ctx *fsm.TransitionContext) error { ... }
type NoopHooks struct{}

// UpdateOtherFields is a no-op implementation.
func (n *NoopHooks) UpdateOtherFields(ctx *TransitionContext) error { return nil }

// WriteOutbox is a no-op implementation.
func (n *NoopHooks) WriteOutbox(ctx *TransitionContext) error { return nil }

// WriteAudit is a no-op implementation.
func (n *NoopHooks) WriteAudit(ctx *TransitionContext) error { return nil }

// PostCommit is a no-op implementation.
func (n *NoopHooks) PostCommit(ctx *TransitionContext) error { return nil }
