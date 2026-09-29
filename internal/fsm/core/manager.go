package fsm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	// DECOUPLING: taskman has no bitbucket.org/pbhealthjira/go/log; stdlib
	// log/slog replaces it at every call site below.
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// GuardEvaluator is implemented by the guards subsystem. tx carries the active
// FSM transaction for read-your-own-writes; tx is nil from the read-only
// ComputeTransition path — guards should fall back to a package-level db then.
type GuardEvaluator interface {
	Evaluate(ctx context.Context, tx *gorm.DB, name string, resourceID string, from State, to State, metadata map[string]interface{}) error
}

// Manager is the reusable FSM engine. It provides the template method ExecuteTransition
// to orchestrate state changes, hooks, and transactions.
type Manager struct {
	Transitions TransitionStore // required
	States      StateStore      // required
	Guards      GuardEvaluator  // optional

	// StateHistoryRepo enables automatic recording of skipped happy-path states.
	// When set (along with a timeline in the TransitionStore), the Manager detects
	// intermediate milestones bypassed by a transition and records them in a single
	// batch INSERT within the same transaction. Optional — modules without timelines
	// or that don't need skip tracking can leave this nil.
	StateHistoryRepo StateHistoryRepository // optional

	// PostCommitActions enables durable post-commit side effects with retry.
	// When set, the manager persists registered actions within the transaction,
	// and a background worker executes them with retry and backoff.
	// Both fields must be set together; if either is nil, post-commit actions are skipped.
	PostCommitRegistry PostCommitActionRegistry   // optional
	PostCommitRepo     PostCommitActionRepository // optional
	AggregateType      string                     // required when PostCommitRegistry is set (e.g., "DISCHARGE")

	// OutboxNotifier wakes the outbox worker immediately after a successful
	// commit, decoupling publish latency from the worker's polling interval.
	// Optional — when nil, the worker falls back to its periodic tick. The
	// outbox row itself is still the source of truth (written via WriteOutbox
	// hook inside the transaction), so crash recovery is unaffected.
	// AggregateType must be set for the notification to be routed to the
	// correct subscriber.
	OutboxNotifier OutboxNotifier // optional
}

// txStateStore is an optional extension for StateStore implementations that
// can read via the current transaction to guarantee read-your-own-writes.
type txStateStore interface {
	GetStateTx(ctx context.Context, tx *gorm.DB, resourceID string) (State, error)
}

func (m *Manager) getState(ctx context.Context, tx *gorm.DB, resourceID string) (State, error) {
	if tx != nil {
		if stateStore, ok := m.States.(txStateStore); ok {
			return stateStore.GetStateTx(ctx, tx, resourceID)
		}
	}
	return m.States.GetState(ctx, resourceID)
}

// computeTransitionFromState evaluates the guard with the active tx for
// read-your-own-writes. tx may be nil (ComputeTransition path).
func (m *Manager) computeTransitionFromState(
	ctx context.Context,
	tx *gorm.DB,
	resourceID string,
	current State,
	event string,
	metadata map[string]interface{},
) (State, *Transition, error) {
	tr, err := m.Transitions.FindTransition(ctx, current, event)
	if err != nil {
		return State{}, nil, fmt.Errorf("fsm: find transition: %w", err)
	}
	if tr == nil {
		return State{}, nil, ErrNoTransition
	}

	next := State{
		Status:    tr.ToStatus,
		Substatus: tr.ToSubstatus,
		Version:   current.Version + 1,
	}

	if tr.GuardName != "" && m.Guards != nil {
		if err := m.Guards.Evaluate(ctx, tx, tr.GuardName, resourceID, current, next, metadata); err != nil {
			return State{}, nil, fmt.Errorf("%w: %v", ErrGuardFailed, err)
		}
	}

	return next, tr, nil
}

// ComputeTransition validates the requested event for a resource and returns the computed
// next state (not yet persisted), the transition definition, or an error.
func (m *Manager) ComputeTransition(ctx context.Context, resourceID string, event string, metadata map[string]interface{}) (State, *Transition, error) {
	if m.Transitions == nil || m.States == nil {
		return State{}, nil, fmt.Errorf("fsm: manager misconfigured")
	}

	current, err := m.getState(ctx, nil, resourceID)
	if err != nil {
		return State{}, nil, fmt.Errorf("fsm: get state: %w", err)
	}
	// No active tx on the read-only path; concrete guards fall back to db.
	return m.computeTransitionFromState(ctx, nil, resourceID, current, event, metadata)
}

// ExecuteTransition is the Template Method that orchestrates a complete
// state transition within a single database transaction.
//
// The method:
//  1. Begins a transaction (or uses provided tx)
//  2. Looks up and validates the transition
//  3. Calls hooks in order: Validate → ApplyStateChange → UpdateOtherFields → WriteOutbox → WriteAudit
//  4. Persists any registered post-commit actions (durable, retryable side effects)
//  5. Commits on success, rolls back on any error
//  6. Calls PostCommit hook after successful commit (for lightweight, non-durable operations)
//
// For durable side effects with retry, use the PostCommitAction system
// (PostCommitRegistry + PostCommitRepo) instead of the PostCommit hook.
// The PostCommit hook remains available for lightweight operations like
// cache invalidation or in-memory pubsub that don't need durability.
//
// Parameters:
//   - ctx: Go context for cancellation
//   - db: GORM database handle (can be *gorm.DB or already a transaction)
//   - entityID: Resource identifier as string
//   - event: Event name triggering the transition
//   - actor: User/system ID performing the action
//   - metadata: Extensible data for hooks (can be nil)
//   - hooks: Client-provided hook implementations
//
// Returns error if any transactional step fails (transition is rolled back).
func (m *Manager) ExecuteTransition(
	ctx context.Context,
	db *gorm.DB,
	entityID string,
	event string,
	actor string,
	metadata map[string]any,
	hooks TransitionHooks,
) error {
	if actor == "" {
		return fmt.Errorf("fsm: actor is required")
	}

	if metadata == nil {
		metadata = make(map[string]any)
	}

	// Resolve the trace id once per transition and stamp it onto ctx so every
	// downstream hook (WriteOutbox, post-commit actions) and every log line
	// for this transition shares the same id — request-driven or not.
	if ctx == nil {
		ctx = context.Background()
	}
	if resolved := resolveOrSynthesiseXRequestID(RequestIDFromContext(ctx)); resolved != RequestIDFromContext(ctx) {
		ctx = context.WithValue(ctx, XRequestIDContextKey, resolved)
	}

	var transitionCtx *TransitionContext

	// Execute within transaction
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Get current state
		oldState, err := m.getState(ctx, tx, entityID)
		if err != nil {
			return fmt.Errorf("get state: %w", err)
		}

		// tx propagates read-your-own-writes into guards as well as state reads.
		newState, transition, err := m.computeTransitionFromState(ctx, tx, entityID, oldState, event, metadata)
		if err != nil {
			return fmt.Errorf("compute transition: %w", err)
		}

		// Build context for hooks
		transitionCtx = &TransitionContext{
			Ctx:        ctx,
			Tx:         tx,
			EntityID:   entityID,
			OldState:   oldState,
			NewState:   newState,
			Transition: transition,
			Event:      event,
			Actor:      actor,
			Metadata:   metadata,
		}

		// Execute transactional hooks in order (Template Method pattern)
		steps := []struct {
			name string
			fn   func(*TransitionContext) error
		}{
			{"Validate", hooks.Validate},
			{"ApplyStateChange", hooks.ApplyStateChange},
			{"UpdateOtherFields", hooks.UpdateOtherFields},
			{"WriteOutbox", hooks.WriteOutbox},
			{"WriteAudit", hooks.WriteAudit},
		}

		for _, step := range steps {
			if err := step.fn(transitionCtx); err != nil {
				return fmt.Errorf("%s: %w", step.name, err)
			}
		}

		// Record skipped happy-path states (if timeline is defined and states were bypassed).
		if err := m.writeSkippedStates(transitionCtx); err != nil {
			return fmt.Errorf("WriteSkippedStates: %w", err)
		}

		// Persist registered post-commit actions within the same transaction.
		// These are durable side effects executed by the background worker with retry.
		if err := m.persistPostCommitActions(transitionCtx); err != nil {
			return fmt.Errorf("PersistPostCommitActions: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("fsm: %w", err)
	}

	// Wake the outbox worker as soon as the commit lands, so the kafka publish
	// does not have to wait for the next poll tick.
	//
	// Critical detail: when ExecuteTransition is called with a `db` that's
	// already an open transaction, gorm's inner `db.Transaction(...)` opens a
	// SAVEPOINT — not a real commit. Returning nil from that inner call only
	// means the savepoint was released; the outer transaction (and the new
	// outbox row) is not yet visible to other connections. Firing Notify here
	// would wake the worker before the row exists, the worker would find
	// nothing, and the wake would be wasted.
	//
	// So we Notify only when this Manager actually owns the commit (db is not
	// already in a tx). Callers that hold an outer transaction must invoke
	// `NotifyOutbox()` themselves after their own commit succeeds.
	if !inExistingTx(db) {
		m.NotifyOutbox()
	}

	// Execute PostCommit hook after successful commit.
	// This hook is for lightweight, non-durable operations (cache invalidation, etc.).
	// For durable side effects with retry, use the PostCommitAction system instead.
	// Errors are logged but not returned to avoid misleading caller.
	if transitionCtx != nil {
		if err := hooks.PostCommit(transitionCtx); err != nil {
			slog.ErrorContext(transitionCtx.Ctx, "PostCommit hook failed (transaction already committed)",
				"error", err,
				"entityID", entityID,
				"event", event,
			)
		}
	}

	return nil
}

// NotifyOutbox fires the outbox-worker wake for this Manager's aggregate type.
// Safe to call when OutboxNotifier or AggregateType is unset — it no-ops.
//
// Use this from a caller that holds an outer transaction across an
// ExecuteTransition call: invoke it AFTER your own Commit() succeeds, so the
// worker wakes only once the new outbox row is actually visible to other
// connections.
func (m *Manager) NotifyOutbox() {
	if m.OutboxNotifier != nil && m.AggregateType != "" {
		m.OutboxNotifier.Notify(m.AggregateType)
	}
}

// inExistingTx reports whether db's connection pool is itself a transaction
// committer (i.e. an active *sql.Tx wrapped by gorm). Mirrors the check gorm
// uses internally to decide between BEGIN and SAVEPOINT.
func inExistingTx(db *gorm.DB) bool {
	if db == nil || db.Statement == nil {
		return false
	}
	committer, ok := db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// XRequestIDContextKey is the canonical ctx key shared with the upstream HTTP
// middleware and the log package's GetLoggerWithContext. Exported so the
// postcommit worker reuses the same key without silent drift.
const XRequestIDContextKey = "x-request-id"

// RequestIDFromContext returns the x-request-id on ctx, or "" if absent.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(XRequestIDContextKey).(string)
	return v
}

// syntheticXRequestIDPrefix marks ids invented by the Manager when the
// transition has no upstream request-id. Lets oncall tell internal-origin
// traces from client-origin ones at a glance.
const syntheticXRequestIDPrefix = "fsm-"

// maxXRequestIDLen caps the value persisted to post_commit_actions.x_request_id.
// X-Request-Id is a client-controlled header; an oversized value would blow
// past Postgres' btree index entry limit (~2704 bytes) and fail the FSM
// transaction. 128 chars covers UUIDs, prefixed/composite ids, and reasonable
// upstream conventions while staying well below the index ceiling. The full
// value remains intact in upstream API/BFF logs.
const maxXRequestIDLen = 128

func resolveOrSynthesiseXRequestID(provided string) string {
	if provided != "" {
		if len(provided) > maxXRequestIDLen {
			provided = provided[:maxXRequestIDLen]
		}
		return provided
	}
	return syntheticXRequestIDPrefix + uuid.NewString()
}

// persistPostCommitActions discovers and persists all registered post-commit actions
// for the current event within the active transaction.
func (m *Manager) persistPostCommitActions(ctx *TransitionContext) error {
	// Skip if post-commit action system is not configured
	if m.PostCommitRegistry == nil || m.PostCommitRepo == nil {
		return nil
	}

	actions := m.PostCommitRegistry.ActionsFor(ctx.Event)

	if len(actions) == 0 {
		return nil
	}

	// context.WithValue panics on a nil parent; guard for callers that build
	// a TransitionContext without Ctx.
	parentCtx := ctx.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	// One trace id per transition — all actions in this tx share it.
	upstreamID := RequestIDFromContext(parentCtx)
	xRequestID := resolveOrSynthesiseXRequestID(upstreamID)

	// Surface the synthetic id on the Manager-side log too, so the trace is
	// continuous from the first log through to worker execution.
	logCtx := parentCtx
	if upstreamID == "" {
		logCtx = context.WithValue(parentCtx, XRequestIDContextKey, xRequestID)
	}

	for _, action := range actions {
		payload, err := action.BuildPayload(ctx)
		if err != nil {
			return fmt.Errorf("BuildPayload(%s): %w", action.Type(), err)
		}

		// nil payload means the action opted out for this transition
		if payload == nil {
			continue
		}

		if err := m.PostCommitRepo.CreateAction(
			parentCtx,
			ctx.Tx,
			m.AggregateType,
			ctx.EntityID,
			action.Type(),
			ctx.Event,
			payload,
			action.MaxRetries(),
			xRequestID,
		); err != nil {
			return fmt.Errorf("persist post-commit action(%s): %w", action.Type(), err)
		}

		slog.DebugContext(logCtx, "Persisted post-commit action",
			"actionType", action.Type(),
			"entityID", ctx.EntityID,
			"event", ctx.Event,
		)
	}

	return nil
}

// PersistPostCommitActions persists post-commit actions for callers that create
// records outside ExecuteTransition but still need durable post-commit behavior.
func (m *Manager) PersistPostCommitActions(ctx *TransitionContext) error {
	return m.persistPostCommitActions(ctx)
}

// writeSkippedStates detects happy-path milestones that were bypassed by the
// current transition and records them as state_history entries in a single batch INSERT.
//
// Skip detection is purely in-memory: compare the sequence of fromState and toState
// in the timeline template. Any template node whose sequence falls strictly between
// the two is considered skipped.
//
// No-op when:
//   - StateHistoryRepo is nil (module opted out)
//   - No timeline is defined in the TransitionStore
//   - No states were actually skipped
func (m *Manager) writeSkippedStates(ctx *TransitionContext) error {
	if m.StateHistoryRepo == nil || m.Transitions == nil {
		return nil
	}

	template := m.Transitions.GetHappyPathTemplate()
	if len(template) == 0 {
		return nil
	}

	// Build sequence lookup: (status|substatus) → sequence number
	seqIndex := make(map[string]int, len(template))
	for _, t := range template {
		if t.Sequence > 0 {
			seqIndex[t.Status+"|"+t.SubStatus] = t.Sequence
		}
	}

	// Determine from/to sequence positions
	fromKey := string(ctx.OldState.Status) + "|" + string(ctx.OldState.Substatus)
	toKey := string(ctx.NewState.Status) + "|" + string(ctx.NewState.Substatus)

	fromSeq := seqIndex[fromKey] // 0 if creation transition or state not in template
	toSeq, toInTemplate := seqIndex[toKey]
	if !toInTemplate || toSeq <= fromSeq+1 {
		// Target not in happy path, or no states in between → nothing to skip
		return nil
	}

	// Collect template nodes that fall between fromSeq and toSeq
	var skippedNodes []TemplateNode
	for _, t := range template {
		if t.Sequence > fromSeq && t.Sequence < toSeq {
			skippedNodes = append(skippedNodes, t)
		}
	}
	if len(skippedNodes) == 0 {
		return nil
	}

	// Sort by sequence to maintain order in history
	sort.Slice(skippedNodes, func(i, j int) bool {
		return skippedNodes[i].Sequence < skippedNodes[j].Sequence
	})

	// Build metadata once — shared across all skip records
	skipMeta := map[string]interface{}{
		"skip_reason":        "transition_bypass",
		"triggered_by_event": ctx.Event,
	}
	metaBytes, err := json.Marshal(skipMeta)
	if err != nil {
		return fmt.Errorf("marshal skip metadata: %w", err)
	}
	metaJSON := datatypes.JSON(metaBytes)

	// Determine from fields for skip records
	var fromStatus, fromSubstatus *string
	if ctx.OldState.Status != "" {
		s := string(ctx.OldState.Status)
		fromStatus = &s
		ss := string(ctx.OldState.Substatus)
		fromSubstatus = &ss
	}

	now := time.Now()
	records := make([]StateHistory, 0, len(skippedNodes))
	for _, node := range skippedNodes {
		records = append(records, StateHistory{
			ResourceID:    ctx.EntityID,
			ResourceType:  m.AggregateType,
			FromStatus:    fromStatus,
			FromSubstatus: fromSubstatus,
			ToStatus:      node.Status,
			ToSubstatus:   node.SubStatus,
			Event:         EventStateSkipped,
			ActorID:       ctx.Actor,
			Metadata:      metaJSON,
			CreatedAt:     now,
		})
	}

	if err := m.StateHistoryRepo.CreateBatch(ctx.Ctx, ctx.Tx, records); err != nil {
		return fmt.Errorf("batch insert skipped states: %w", err)
	}

	slog.DebugContext(ctx.Ctx, "Recorded skipped happy-path states",
		"entityID", ctx.EntityID,
		"event", ctx.Event,
		"skippedCount", len(records),
	)

	return nil
}
