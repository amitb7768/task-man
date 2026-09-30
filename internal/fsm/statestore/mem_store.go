package statestore

import (
	"context"
	"errors"
	"sync"

	fsm "taskman/internal/fsm/core"
)

// InMemoryStore is a threadsafe StateStore useful for unit tests and local development.
type InMemoryStore struct {
	mu        sync.RWMutex
	states    map[string]fsm.State
	audits    map[string][]map[string]interface{}
	processed map[string]map[string]struct{}
}

// NewInMemoryStore constructs a fresh in-memory implementation.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		states:    map[string]fsm.State{},
		audits:    map[string][]map[string]interface{}{},
		processed: map[string]map[string]struct{}{},
	}
}

// GetState implements fsm.StateStore.
func (s *InMemoryStore) GetState(ctx context.Context, resourceID string) (fsm.State, error) {
	_ = ctx

	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.states[resourceID]
	if !ok {
		return fsm.State{}, errors.New("state not found")
	}
	return state, nil
}

// SetState is a helper for testing to manually seed state.
func (s *InMemoryStore) SetState(resourceID string, state fsm.State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[resourceID] = state
}

// AuditLog exposes recorded audits for inspection in tests.
func (s *InMemoryStore) AuditLog(resourceID string) []map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cpy := make([]map[string]interface{}, len(s.audits[resourceID]))
	copy(cpy, s.audits[resourceID])
	return cpy
}
