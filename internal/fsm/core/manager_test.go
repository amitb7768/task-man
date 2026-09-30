package fsm

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

type stubTransitionStore struct {
	transitions map[string]Transition
}

func (s *stubTransitionStore) FindTransition(ctx context.Context, from State, event string) (*Transition, error) {
	_ = ctx
	key := string(from.Status) + "|" + string(from.Substatus) + "|" + event
	if tr, ok := s.transitions[key]; ok {
		copy := tr
		return &copy, nil
	}
	key = string(from.Status) + "|" + "|" + event
	if tr, ok := s.transitions[key]; ok {
		copy := tr
		return &copy, nil
	}
	return nil, nil
}

func (s *stubTransitionStore) ListTransitions() []Transition        { return nil }
func (s *stubTransitionStore) GetHappyPathTemplate() []TemplateNode { return nil }

type stubStateStore struct {
	state State
	err   error
}

func (s *stubStateStore) GetState(ctx context.Context, resourceID string) (State, error) {
	_ = ctx
	if s.err != nil {
		return State{}, s.err
	}
	return s.state, nil
}

type stubGuards struct {
	err error
}

func (g *stubGuards) Evaluate(ctx context.Context, tx *gorm.DB, name string, resourceID string, from State, to State, metadata map[string]interface{}) error {
	_ = ctx
	_ = tx
	_ = name
	_ = resourceID
	_ = from
	_ = to
	_ = metadata
	return g.err
}

func TestManagerComputeTransition_Success(t *testing.T) {
	manager := &Manager{
		Transitions: &stubTransitionStore{
			transitions: map[string]Transition{
				"NEW|INIT|approve": {
					FromStatus:    "NEW",
					FromSubstatus: "INIT",
					ToStatus:      "READY",
					ToSubstatus:   "WAITING",
				},
			},
		},
		States: &stubStateStore{state: State{Status: "NEW", Substatus: "INIT", Version: 1}},
		Guards: &stubGuards{},
	}

	newState, tr, err := manager.ComputeTransition(context.Background(), "res-1", "approve", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if newState.Status != "READY" || newState.Version != 2 {
		t.Fatalf("unexpected new state %+v", newState)
	}
	if tr == nil || tr.ToStatus != "READY" {
		t.Fatalf("expected transition returned")
	}
}

func TestManagerComputeTransition_GuardFailure(t *testing.T) {
	manager := &Manager{
		Transitions: &stubTransitionStore{
			transitions: map[string]Transition{
				"NEW|INIT|approve": {
					FromStatus:    "NEW",
					FromSubstatus: "INIT",
					ToStatus:      "READY",
					GuardName:     "guard_fail",
				},
			},
		},
		States: &stubStateStore{state: State{Status: "NEW", Substatus: "INIT", Version: 1}},
		Guards: &stubGuards{err: errors.New("blocked")},
	}

	_, _, err := manager.ComputeTransition(context.Background(), "res-1", "approve", nil)
	if err == nil || !errors.Is(err, ErrGuardFailed) {
		t.Fatalf("expected guard failure, got %v", err)
	}
}

func TestManagerComputeTransition_NoTransition(t *testing.T) {
	manager := &Manager{
		Transitions: &stubTransitionStore{transitions: map[string]Transition{}},
		States:      &stubStateStore{state: State{Status: "NEW", Substatus: "INIT"}},
	}

	_, _, err := manager.ComputeTransition(context.Background(), "res-1", "unknown", nil)
	if err == nil || !errors.Is(err, ErrNoTransition) {
		t.Fatalf("expected ErrNoTransition, got %v", err)
	}
}

// Spy hooks to verify execution order
type spyHooks struct {
	NoopHooks
	calls []string
}

func (s *spyHooks) Validate(ctx *TransitionContext) error {
	s.calls = append(s.calls, "Validate")
	return nil
}
func (s *spyHooks) ApplyStateChange(ctx *TransitionContext) error {
	s.calls = append(s.calls, "Apply")
	return nil
}
func (s *spyHooks) UpdateOtherFields(ctx *TransitionContext) error {
	s.calls = append(s.calls, "Update")
	return nil
}
func (s *spyHooks) WriteOutbox(ctx *TransitionContext) error {
	s.calls = append(s.calls, "Outbox")
	return nil
}
func (s *spyHooks) WriteAudit(ctx *TransitionContext) error {
	s.calls = append(s.calls, "Audit")
	return nil
}
func (s *spyHooks) PostCommit(ctx *TransitionContext) error {
	s.calls = append(s.calls, "PostCommit")
	return nil
}

/*
// NOTE: GORM mocking is complex without a full driver.
// Ideally we would use go-sqlmock with gormer driver, but for now we skip
// direct GORM ExecuteTransition testing in unit tests if no DB is available.
// Alternatively, we can rely on integration tests.
//
// However, the structure is:
//
// func TestManagerExecuteTransition(t *testing.T) {
//     ... setup mocked DB ...
//     hooks := &spyHooks{}
//     err := manager.ExecuteTransition(ctx, db, "id", "event", "actor", nil, hooks)
//     ... assert hooks.calls order ...
// }
*/
