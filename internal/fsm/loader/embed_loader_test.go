package loader

import (
	"context"
	"testing"

	fsm "taskman/internal/fsm/core"
)

const sampleTransitions = `
transitions:
  - from_status: START
    from_substatus: INIT
    event: approve
    to_status: IN_PROGRESS
    to_substatus: RUNNING
    guard: guard_ok
  - from_status: IN_PROGRESS
    from_substatus: ""
    event: cancel
    to_status: CANCELLED
    to_substatus: ""
    guard: ""
`

func TestTransitionStoreEmbed_NewFromBytes(t *testing.T) {
	store, err := NewFromBytes([]byte(sampleTransitions))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(store.ListTransitions()); got != 2 {
		t.Fatalf("expected 2 transitions, got %d", got)
	}

	ctx := context.Background()
	state := fsm.State{Status: "START", Substatus: "INIT"}
	tr, err := store.FindTransition(ctx, state, "approve")
	if err != nil {
		t.Fatalf("find transition error: %v", err)
	}
	if tr == nil || tr.ToStatus != "IN_PROGRESS" || tr.GuardName != "guard_ok" {
		t.Fatalf("unexpected transition %+v", tr)
	}

	// wildcard match
	state = fsm.State{Status: "IN_PROGRESS", Substatus: "ANY"}
	tr, err = store.FindTransition(ctx, state, "cancel")
	if err != nil {
		t.Fatalf("find transition error: %v", err)
	}
	if tr == nil || tr.ToStatus != "CANCELLED" {
		t.Fatalf("expected wildcard transition, got %+v", tr)
	}
}

// fakeGuardRegistry is a minimal stub satisfying guardRegistry for tests.
type fakeGuardRegistry struct{ names []string }

func (f fakeGuardRegistry) RegisteredGuards() []string { return f.names }

func TestTransitionStoreEmbed_ValidateGuards(t *testing.T) {
	store, err := NewFromBytes([]byte(sampleTransitions))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All referenced guards registered — must pass.
	if err := store.ValidateGuards(fakeGuardRegistry{names: []string{"guard_ok"}}); err != nil {
		t.Fatalf("expected no error when guard registered, got %v", err)
	}

	// Empty guard names are pass-through and not validated.
	if err := store.ValidateGuards(fakeGuardRegistry{names: []string{"guard_ok", "extra_unused"}}); err != nil {
		t.Fatalf("extra registered guards must not fail validation, got %v", err)
	}

	// Missing guard surfaces an error.
	if err := store.ValidateGuards(fakeGuardRegistry{names: nil}); err == nil {
		t.Fatalf("expected error when guard_ok not registered, got nil")
	}

	// Nil evaluator is a misuse.
	if err := store.ValidateGuards(nil); err == nil {
		t.Fatalf("expected error when evaluator is nil, got nil")
	}
}
