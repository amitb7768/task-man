package guards

import (
	"context"
	"fmt"
	"sync"

	"gorm.io/gorm"

	fsm "taskman/internal/fsm/core"
)

// GuardFunc allows domains to inject custom logic before a transition persists.
// tx is the active FSM transaction (use it for read-your-own-writes); tx is
// nil on the read-only ComputeTransition path — guards fall back to db then.
type GuardFunc func(ctx context.Context, tx *gorm.DB, resourceID string, from fsm.State, to fsm.State, metadata map[string]interface{}) error

// Evaluator is a threadsafe registry of guard functions.
type Evaluator struct {
	mu     sync.RWMutex
	guards map[string]GuardFunc
}

// New creates a new guard evaluator.
func New() *Evaluator {
	return &Evaluator{guards: map[string]GuardFunc{}}
}

// Register sets a guard function. Re-registering the same name overwrites it.
func (g *Evaluator) Register(name string, fn GuardFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if name == "" {
		return
	}
	g.guards[name] = fn
}

// Evaluate runs the guard by name if defined; empty guard names are treated as pass-through.
func (g *Evaluator) Evaluate(ctx context.Context, tx *gorm.DB, name string, resourceID string, from fsm.State, to fsm.State, metadata map[string]interface{}) error {
	if name == "" {
		return nil
	}

	g.mu.RLock()
	fn, ok := g.guards[name]
	g.mu.RUnlock()

	if !ok {
		return fmt.Errorf("unknown guard: %s", name)
	}

	return fn(ctx, tx, resourceID, from, to, metadata)
}

// RegisteredGuards returns guard names currently installed (for validation/logging).
func (g *Evaluator) RegisteredGuards() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	names := make([]string, 0, len(g.guards))
	for name := range g.guards {
		names = append(names, name)
	}
	return names
}
