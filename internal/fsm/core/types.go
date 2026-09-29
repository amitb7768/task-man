package fsm

// EventStateSkipped is the event name recorded in state history when a
// happy-path milestone is bypassed by a transition that jumps ahead.
const EventStateSkipped = "state_skipped"

// Status represents the coarse-grained state for a resource.
type Status string

// Substatus represents the finer-grained state nested under Status.
type Substatus string

// State is the persisted representation that callers store via StateStore.
// State is immutable once returned; Manager callers should treat it as copy-by-value.
type State struct {
	Status    Status    `json:"status"`
	Substatus Substatus `json:"substatus"`
	Version   int64     `json:"version"`
}

// Transition defines a state change allowed for a resource.
type Transition struct {
	FromStatus    Status
	FromSubstatus Substatus // empty string acts as wildcard
	Event         string
	ToStatus      Status
	ToSubstatus   Substatus
	GuardName     string
	DisplayName   string // Display name for UI corresponding to to_status and to_substatus
}

// TemplateNode represents a milestone in the timeline for a resource type.
// Defined explicitly in the YAML timeline section, not derived from transitions.
//
// Sequence > 0: part of the happy-path template (shown as PENDING when not yet reached).
// Sequence == 0: only shown when it actually occurs in history (e.g. CANCELLED states).
// Terminal == true: no PENDING nodes are rendered after this state.
type TemplateNode struct {
	Status      string // e.g. "DISCHARGE_REQUESTED"
	SubStatus   string // e.g. "WARD_ACK_PENDING"
	DisplayName string // e.g. "Discharge Requested"
	Sequence    int    // 1-based happy-path order; 0 = not part of happy path
	Terminal    bool   // true = no pending steps shown after this state
}
