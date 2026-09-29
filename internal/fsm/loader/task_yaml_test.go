package loader

import (
	"context"
	"testing"

	fsm "taskman/internal/fsm/core"
)

// TestNewFromFS_TaskYAML is the wave-2.1 loader smoke test (see
// docs/DESIGN_PG_FSM_MIGRATION.md, FSM section): it only proves the copied
// ipd loader correctly parses taskman's own transitions/task.yaml contract
// (4 states, 12 permissive pairs) — it does not exercise any taskman
// business logic, which is wave 2.2's job once hooks are wired.
func TestNewFromFS_TaskYAML(t *testing.T) {
	store, err := NewFromFS("task.yaml")
	if err != nil {
		t.Fatalf("NewFromFS(task.yaml): unexpected error: %v", err)
	}

	trs := store.ListTransitions()
	if got := len(trs); got != 12 {
		t.Fatalf("expected 12 transitions, got %d", got)
	}

	states := map[fsm.Status]struct{}{}
	// targetsFor[from_status|event] -> set of to_status values seen for that pair
	targetsFor := map[string]map[fsm.Status]struct{}{}
	for _, tr := range trs {
		states[tr.FromStatus] = struct{}{}
		states[tr.ToStatus] = struct{}{}

		key := string(tr.FromStatus) + "|" + tr.Event
		if targetsFor[key] == nil {
			targetsFor[key] = map[fsm.Status]struct{}{}
		}
		targetsFor[key][tr.ToStatus] = struct{}{}
	}

	if got := len(states); got != 4 {
		t.Fatalf("expected 4 distinct states, got %d: %v", got, states)
	}

	ctx := context.Background()
	for _, tr := range trs {
		key := string(tr.FromStatus) + "|" + tr.Event
		if targets := targetsFor[key]; len(targets) != 1 {
			t.Fatalf("(from_status=%s, event=%s) resolves to %d distinct to_status values, want exactly 1: %v",
				tr.FromStatus, tr.Event, len(targets), targets)
		}

		from := fsm.State{Status: tr.FromStatus, Substatus: tr.FromSubstatus}
		got, err := store.FindTransition(ctx, from, tr.Event)
		if err != nil {
			t.Fatalf("FindTransition(%s, %s): unexpected error: %v", tr.FromStatus, tr.Event, err)
		}
		if got == nil {
			t.Fatalf("FindTransition(%s, %s): expected a match, got nil", tr.FromStatus, tr.Event)
		}
		if got.ToStatus != tr.ToStatus {
			t.Errorf("FindTransition(%s, %s) = %s, want %s", tr.FromStatus, tr.Event, got.ToStatus, tr.ToStatus)
		}
	}
}
