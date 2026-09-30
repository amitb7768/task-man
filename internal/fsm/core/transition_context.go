package fsm

import (
	"context"

	"gorm.io/gorm"
)

// TransitionContext carries all state needed during a transition.
// Hooks receive this context and can read/write to Metadata for inter-step communication.
type TransitionContext struct {
	// Ctx is the Go context for cancellation and deadline propagation
	Ctx context.Context

	// Tx is the active GORM transaction for all DB operations
	Tx *gorm.DB

	// EntityID is the resource identifier (e.g., discharge_details.id as string)
	EntityID string

	// OldState contains the status and substatus before transition
	OldState State

	// NewState contains the target status and substatus after transition
	NewState State

	// Transition is the matched transition definition from YAML
	// Provides access to FromStatus, FromSubstatus, ToStatus, ToSubstatus, Event, GuardName, DisplayName
	Transition *Transition

	// Event is the event name that triggered this transition
	Event string

	// Actor is the user/system ID performing the transition
	Actor string

	// Metadata is extensible data passed between hooks
	// Clients can use this for inter-hook communication
	Metadata map[string]any
}
