package fsm

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

// Non-empty input passes through verbatim.
func TestResolveOrSynthesiseXRequestID_PassesThroughUpstream(t *testing.T) {
	got := resolveOrSynthesiseXRequestID("client-trace-42")
	if got != "client-trace-42" {
		t.Errorf("got %q, want verbatim passthrough", got)
	}
}

// Empty input produces a non-empty id with the synthesis prefix.
func TestResolveOrSynthesiseXRequestID_SynthesisesWhenEmpty(t *testing.T) {
	got := resolveOrSynthesiseXRequestID("")
	if got == "" {
		t.Fatalf("synthesis must always produce a non-empty id")
	}
	if !strings.HasPrefix(got, syntheticXRequestIDPrefix) {
		t.Errorf("got %q, want prefix %q", got, syntheticXRequestIDPrefix)
	}
}

// Oversized upstream ids must be truncated to maxXRequestIDLen so a hostile
// client cannot blow past the btree index size limit and fail every insert.
func TestResolveOrSynthesiseXRequestID_TruncatesOversizedUpstreamID(t *testing.T) {
	huge := strings.Repeat("a", maxXRequestIDLen*10)
	got := resolveOrSynthesiseXRequestID(huge)
	if len(got) != maxXRequestIDLen {
		t.Errorf("oversized id not truncated: len=%d, want %d", len(got), maxXRequestIDLen)
	}
	// Value at the boundary must pass through unchanged.
	exact := strings.Repeat("b", maxXRequestIDLen)
	if got := resolveOrSynthesiseXRequestID(exact); got != exact {
		t.Errorf("id at exact cap was modified: len=%d, want %d", len(got), maxXRequestIDLen)
	}
}

// Guard against a future switch to a non-unique source (clock, hostname, etc).
func TestResolveOrSynthesiseXRequestID_SynthesesAreUnique(t *testing.T) {
	a := resolveOrSynthesiseXRequestID("")
	b := resolveOrSynthesiseXRequestID("")
	if a == b {
		t.Errorf("two synthesised ids collided: %q == %q", a, b)
	}
}

type fakePostCommitRepo struct {
	mu          sync.Mutex
	xRequestIDs []string
	actionTypes []string
}

func (f *fakePostCommitRepo) CreateAction(ctx context.Context, tx *gorm.DB, aggregateType, aggregateID, actionType, event string, payload map[string]any, maxRetries int, xRequestID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.xRequestIDs = append(f.xRequestIDs, xRequestID)
	f.actionTypes = append(f.actionTypes, actionType)
	return nil
}

var _ PostCommitActionRepository = (*fakePostCommitRepo)(nil)

type fakePostCommitAction struct{ typ string }

func (a *fakePostCommitAction) Type() string { return a.typ }
func (a *fakePostCommitAction) BuildPayload(ctx *TransitionContext) (map[string]any, error) {
	return map[string]any{"k": "v"}, nil
}
func (a *fakePostCommitAction) Execute(ctx context.Context, payload map[string]any) error {
	return nil
}
func (a *fakePostCommitAction) MaxRetries() int                      { return 3 }
func (a *fakePostCommitAction) RetryDelay(attempt int) time.Duration { return time.Second }

var _ PostCommitAction = (*fakePostCommitAction)(nil)

type fakeRegistry struct {
	event   string
	actions []PostCommitAction
}

func (r *fakeRegistry) ActionsFor(event string) []PostCommitAction {
	if event == r.event {
		return r.actions
	}
	return nil
}
func (r *fakeRegistry) GetByType(actionType string) PostCommitAction { return nil }

var _ PostCommitActionRegistry = (*fakeRegistry)(nil)

// Upstream id on ctx must reach every action in the transition unchanged.
func TestPersistPostCommitActions_SharesUpstreamRequestIDAcrossActions(t *testing.T) {
	repo := &fakePostCommitRepo{}
	registry := &fakeRegistry{
		event: "transition_event",
		actions: []PostCommitAction{
			&fakePostCommitAction{typ: "action_a"},
			&fakePostCommitAction{typ: "action_b"},
			&fakePostCommitAction{typ: "action_c"},
		},
	}
	manager := &Manager{
		PostCommitRegistry: registry,
		PostCommitRepo:     repo,
		AggregateType:      "DISCHARGE",
	}

	const upstreamID = "client-trace-xyz"
	ctx := context.WithValue(context.Background(), XRequestIDContextKey, upstreamID)
	tctx := &TransitionContext{Ctx: ctx, EntityID: "entity-1", Event: "transition_event"}

	if err := manager.persistPostCommitActions(tctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.xRequestIDs) != 3 {
		t.Fatalf("expected 3 CreateAction calls, got %d", len(repo.xRequestIDs))
	}
	for i, id := range repo.xRequestIDs {
		if id != upstreamID {
			t.Errorf("action[%d] (%s) got %q, want upstream %q",
				i, repo.actionTypes[i], id, upstreamID)
		}
	}
}

// Regression: synthesis branch must not panic when TransitionContext.Ctx is nil.
func TestPersistPostCommitActions_NilCtxDoesNotPanic(t *testing.T) {
	repo := &fakePostCommitRepo{}
	registry := &fakeRegistry{
		event:   "transition_event",
		actions: []PostCommitAction{&fakePostCommitAction{typ: "action_a"}},
	}
	manager := &Manager{
		PostCommitRegistry: registry,
		PostCommitRepo:     repo,
		AggregateType:      "DISCHARGE",
	}

	tctx := &TransitionContext{Ctx: nil, EntityID: "entity-1", Event: "transition_event"}

	if err := manager.persistPostCommitActions(tctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.xRequestIDs) != 1 {
		t.Fatalf("expected 1 CreateAction call, got %d", len(repo.xRequestIDs))
	}
	if !strings.HasPrefix(repo.xRequestIDs[0], syntheticXRequestIDPrefix) {
		t.Errorf("got %q, want prefix %q", repo.xRequestIDs[0], syntheticXRequestIDPrefix)
	}
}

// Per-transition contract: one synthesised id is shared across all actions
// (not one per action) so a non-request-driven transition still groups in logs.
func TestPersistPostCommitActions_SynthesisesOnceWhenCtxHasNoRequestID(t *testing.T) {
	repo := &fakePostCommitRepo{}
	registry := &fakeRegistry{
		event: "transition_event",
		actions: []PostCommitAction{
			&fakePostCommitAction{typ: "action_a"},
			&fakePostCommitAction{typ: "action_b"},
		},
	}
	manager := &Manager{
		PostCommitRegistry: registry,
		PostCommitRepo:     repo,
		AggregateType:      "DISCHARGE",
	}

	tctx := &TransitionContext{Ctx: context.Background(), EntityID: "entity-1", Event: "transition_event"}

	if err := manager.persistPostCommitActions(tctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.xRequestIDs) != 2 {
		t.Fatalf("expected 2 CreateAction calls, got %d", len(repo.xRequestIDs))
	}

	first := repo.xRequestIDs[0]
	if first == "" {
		t.Fatalf("synthesised id must not be empty")
	}
	if !strings.HasPrefix(first, syntheticXRequestIDPrefix) {
		t.Errorf("got %q, want prefix %q", first, syntheticXRequestIDPrefix)
	}
	if repo.xRequestIDs[1] != first {
		t.Errorf("per-transition contract violated: action_a got %q, action_b got %q", first, repo.xRequestIDs[1])
	}
}
