package loader

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"

	fsm "taskman/internal/fsm/core"
	"taskman/internal/fsm/transitions"
)

// TransitionStoreEmbed implements core.TransitionStore backed by embedded YAML transitions.
type TransitionStoreEmbed struct {
	mu          sync.RWMutex
	transitions []fsm.Transition
	timeline    []fsm.TemplateNode // explicitly defined timeline milestones
}

// NewFromBytes instantiates a TransitionStoreEmbed by parsing YAML bytes.
func NewFromBytes(b []byte) (*TransitionStoreEmbed, error) {
	type spec struct {
		Transitions []struct {
			FromStatus    string `yaml:"from_status"`
			FromSubstatus string `yaml:"from_substatus"`
			Event         string `yaml:"event"`
			ToStatus      string `yaml:"to_status"`
			ToSubstatus   string `yaml:"to_substatus"`
			Guard         string `yaml:"guard"`
			DisplayName   string `yaml:"displayName"`
		} `yaml:"transitions"`
		Timeline []struct {
			Status      string `yaml:"status"`
			SubStatus   string `yaml:"substatus"`
			DisplayName string `yaml:"display_name"`
			Sequence    int    `yaml:"sequence"`
			Terminal    bool   `yaml:"terminal"`
		} `yaml:"timeline"`
	}

	var s spec
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("unmarshal transitions: %w", err)
	}

	seen := map[string]struct{}{}
	mapped := make([]fsm.Transition, 0, len(s.Transitions))
	for i, t := range s.Transitions {
		// Allow empty FromStatus for initial state transitions (creation transitions)
		// Event and ToStatus must always be provided
		if t.Event == "" || t.ToStatus == "" {
			return nil, fmt.Errorf("invalid transition at index %d: event and to_status are required", i)
		}

		// Multiple initial (empty from_status) transitions allowed when keyed
		// by distinct events; the (from, substatus, event) uniqueness check
		// below still rejects two creation transitions on the SAME event.

		key := fmt.Sprintf("%s|%s|%s", t.FromStatus, t.FromSubstatus, t.Event)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate transition %s", key)
		}
		seen[key] = struct{}{}

		mapped = append(mapped, fsm.Transition{
			FromStatus:    fsm.Status(t.FromStatus),
			FromSubstatus: fsm.Substatus(t.FromSubstatus),
			Event:         t.Event,
			ToStatus:      fsm.Status(t.ToStatus),
			ToSubstatus:   fsm.Substatus(t.ToSubstatus),
			GuardName:     t.Guard,
			DisplayName:   t.DisplayName,
		})
	}

	// Parse timeline section (optional)
	var timeline []fsm.TemplateNode
	if len(s.Timeline) > 0 {
		// Build sets for validation:
		// exactStates: exact (to_status|to_substatus) matches
		// statusOnly: statuses that appear in any transition target
		exactStates := make(map[string]struct{}, len(mapped))
		statusOnly := make(map[string]struct{}, len(mapped))
		for _, tr := range mapped {
			exactStates[string(tr.ToStatus)+"|"+string(tr.ToSubstatus)] = struct{}{}
			statusOnly[string(tr.ToStatus)] = struct{}{}
		}

		for i, t := range s.Timeline {
			if t.Status == "" {
				return nil, fmt.Errorf("timeline entry at index %d: status is required", i)
			}
			if t.DisplayName == "" {
				return nil, fmt.Errorf("timeline entry at index %d: display_name is required", i)
			}

			// Validate that the timeline state exists in transitions.
			// If substatus is empty, validate that at least the status exists as a target.
			// This allows timeline entries like DISCHARGE_CANCELLED (no substatus) to match
			// cancel transitions that have specific substatuses (CLINICAL_DETERIORATION, etc.).
			key := t.Status + "|" + t.SubStatus
			if t.SubStatus == "" {
				if _, ok := statusOnly[t.Status]; !ok {
					return nil, fmt.Errorf("timeline entry at index %d: status %q is not a valid transition target", i, t.Status)
				}
			} else {
				if _, ok := exactStates[key]; !ok {
					return nil, fmt.Errorf("timeline entry at index %d: state %q is not a valid transition target", i, key)
				}
			}

			timeline = append(timeline, fsm.TemplateNode{
				Status:      t.Status,
				SubStatus:   t.SubStatus,
				DisplayName: t.DisplayName,
				Sequence:    t.Sequence,
				Terminal:    t.Terminal,
			})
		}
	}

	return &TransitionStoreEmbed{transitions: mapped, timeline: timeline}, nil
}

// NewFromFS loads a named YAML file from the embedded filesystem. Name should include the file name only.
func NewFromFS(name string) (*TransitionStoreEmbed, error) {
	if name == "" {
		return nil, fmt.Errorf("transition file name required")
	}

	// Keep reference relative to loader package for go:embed.
	path := filepath.ToSlash(name)
	data, err := transitions.FS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read embedded transitions %s: %w", name, err)
	}

	return NewFromBytes(data)
}

// FindTransition implements core.TransitionStore.
func (s *TransitionStoreEmbed) FindTransition(ctx context.Context, from fsm.State, event string) (*fsm.Transition, error) {
	_ = ctx

	s.mu.RLock()
	defer s.mu.RUnlock()

	// exact match first
	for _, tr := range s.transitions {
		if tr.FromStatus == from.Status && tr.FromSubstatus == from.Substatus && tr.Event == event {
			copy := tr
			return &copy, nil
		}
	}

	// wildcard fallback
	for _, tr := range s.transitions {
		if tr.FromStatus == from.Status && tr.FromSubstatus == "" && tr.Event == event {
			copy := tr
			return &copy, nil
		}
	}

	return nil, nil
}

// ListTransitions returns a copy of internal transitions.
func (s *TransitionStoreEmbed) ListTransitions() []fsm.Transition {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]fsm.Transition, len(s.transitions))
	copy(out, s.transitions)
	return out
}

// guardRegistry is the minimal surface ValidateGuards needs; an interface
// keeps loader free of a fsm/guards import (avoids cycle).
type guardRegistry interface {
	RegisteredGuards() []string
}

// ValidateGuards returns an aggregated error for every guard name referenced
// by a transition but not registered on eval. Call at startup, post-Register.
func (s *TransitionStoreEmbed) ValidateGuards(eval guardRegistry) error {
	if eval == nil {
		return fmt.Errorf("loader: ValidateGuards requires a non-nil evaluator")
	}

	registered := make(map[string]struct{})
	for _, name := range eval.RegisteredGuards() {
		registered[name] = struct{}{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	missing := make(map[string]struct{})
	for _, tr := range s.transitions {
		if tr.GuardName == "" {
			continue
		}
		if _, ok := registered[tr.GuardName]; !ok {
			missing[tr.GuardName] = struct{}{}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, 0, len(missing))
	for n := range missing {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Errorf("loader: unregistered guards referenced by transitions: %v", names)
}

// GetHappyPathTemplate returns the timeline milestones defined in the YAML timeline section.
// Returns a copy of the parsed timeline nodes. If no timeline section is defined, returns nil.
func (s *TransitionStoreEmbed) GetHappyPathTemplate() []fsm.TemplateNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.timeline) == 0 {
		return nil
	}

	out := make([]fsm.TemplateNode, len(s.timeline))
	copy(out, s.timeline)
	return out
}
