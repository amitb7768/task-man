package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"

	fsm "taskman/internal/fsm/core"
	"taskman/internal/fsm/loader"
	"taskman/internal/model"
)

var allStatuses = []string{model.StatusTodo, model.StatusInProgress, model.StatusDone, model.StatusCancelled}

// yamlWithout renders the real task.yaml's transitions back to YAML,
// dropping every row matching (from, event) — a restricted table built
// through the same loader production uses.
func yamlWithout(t *testing.T, from, event string) []byte {
	t.Helper()
	real, err := loader.NewFromFS(taskTransitionsFile)
	if err != nil {
		t.Fatalf("load task.yaml: %v", err)
	}
	var b bytes.Buffer
	b.WriteString("transitions:\n")
	dropped := 0
	for _, tr := range real.ListTransitions() {
		if string(tr.FromStatus) == from && tr.Event == event {
			dropped++
			continue
		}
		fmt.Fprintf(&b, "  - from_status: %s\n    from_substatus: \"\"\n    event: %s\n    to_status: %s\n    to_substatus: \"\"\n    displayName: %q\n    guard: \"\"\n",
			tr.FromStatus, tr.Event, tr.ToStatus, tr.DisplayName)
	}
	if dropped != 1 {
		t.Fatalf("expected to drop exactly one row (%s/%s), dropped %d", from, event, dropped)
	}
	return b.Bytes()
}

// statusRows counts task_activity status rows for a task straight from the table.
func statusRows(t *testing.T, s *Service, id string) int64 {
	t.Helper()
	var n int64
	if err := s.db.Table("task_activity").Where("task_id = ? AND kind = ?", id, model.ActivityStatus).Count(&n).Error; err != nil {
		t.Fatalf("count activity: %v", err)
	}
	return n
}

func TestEventForCoversEveryPairAndRejectsUnknown(t *testing.T) {
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			ev, err := eventFor(from, to)
			if from == to {
				if err == nil {
					t.Fatalf("%s->%s: want error, got event %q", from, to, ev)
				}
				continue
			}
			if err != nil || ev == "" {
				t.Fatalf("%s->%s: event=%q err=%v", from, to, ev, err)
			}
		}
	}
	if _, err := eventFor(model.StatusTodo, "bogus"); err == nil {
		t.Fatal("unknown target must error")
	}
	// Every mapped (from, event) resolves in the real YAML to exactly `to`.
	ts, err := loader.NewFromFS(taskTransitionsFile)
	if err != nil {
		t.Fatal(err)
	}
	for pair, ev := range taskEvents {
		tr, err := ts.FindTransition(context.Background(), fsm.State{Status: fsm.Status(pair[0])}, ev)
		if err != nil || tr == nil || string(tr.ToStatus) != pair[1] {
			t.Fatalf("%v via %q: got %+v err=%v", pair, ev, tr, err)
		}
	}
}

// 1. Teeth: a restricted table (no done->todo) rejects the patch with 409,
// leaving the task and its timeline untouched; the permissive Service accepts
// the same patch.
func TestFSMRestrictedManagerRejectsIllegalTransition(t *testing.T) {
	e := tkSetup(t)
	ts, err := loader.NewFromBytes(yamlWithout(t, model.StatusDone, "reopen"))
	if err != nil {
		t.Fatalf("restricted yaml: %v", err)
	}
	restricted := newWithManager(e.s.db, newTaskManagerFrom(e.s.db, ts))

	v := tkCreate(t, e.asAlice, e.s, "t", func(tk *model.Task) { tk.Status = model.StatusDone })
	before, err := e.s.getTaskRaw(e.noUser, v.ID)
	if err != nil {
		t.Fatal(err)
	}

	patch := `{"status":"todo","title":"renamed"}`
	_, err = restricted.PatchTask(e.asAlice, v.ID, []byte(patch))
	tkWantErr(t, err, 409, "illegal status transition: done -> todo")

	after, err := e.s.getTaskRaw(e.noUser, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != model.StatusDone || after.Title != "t" || !after.UpdatedAt.Equal(before.UpdatedAt) ||
		after.CompletedAt == nil || !after.CompletedAt.Equal(*before.CompletedAt) {
		t.Fatalf("rejected transition changed the task: before=%+v after=%+v", before, after)
	}
	if n := statusRows(t, e.s, v.ID); n != 0 || len(after.Activity) != 0 {
		t.Fatalf("rejected transition left activity: rows=%d activity=%+v", n, after.Activity)
	}

	// Other pairs still pass through the restricted manager.
	got, err := restricted.PatchTask(e.asAlice, v.ID, []byte(`{"status":"in_progress"}`))
	if err != nil || got.Status != model.StatusInProgress {
		t.Fatalf("done->in_progress on restricted: %v %+v", err, got)
	}
	tkPatch(t, e.asAlice, e.s, v.ID, `{"status":"done"}`)

	// Contrast: the production (permissive) Service accepts the same patch.
	got = tkPatch(t, e.asAlice, e.s, v.ID, patch)
	if got.Status != model.StatusTodo || got.Title != "renamed" || got.CompletedAt != nil {
		t.Fatalf("permissive store: %+v", got)
	}
	if n := statusRows(t, e.s, v.ID); n != 3 {
		t.Fatalf("status rows = %d, want 3", n)
	}
}

// 2. Audit fidelity: the stored row and the response JSON equal the
// hand-built pre-FSM entry.
func TestFSMAuditRowFidelity(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAlice, e.s, "a", nil)
	got := tkPatch(t, e.asAlice, e.s, v.ID, `{"status":"in_progress"}`)

	if len(got.Activity) != 1 {
		t.Fatalf("response activity = %+v", got.Activity)
	}
	resp := got.Activity[0]
	now := got.UpdatedAt // the patch's `now`
	aliceID := e.alice.ID
	want := model.ActivityEntry{
		ID:     resp.ID, // NewID(): random, only its identity is checked
		TaskID: v.ID,
		Seq:    resp.Seq,
		Kind:   model.ActivityStatus,
		Date:   localDate(now),
		At:     now,
		By:     &aliceID,
		ByName: "Alice",
		From:   model.StatusTodo,
		To:     model.StatusInProgress,
	}
	if resp.ID == "" || resp.Seq == 0 {
		t.Fatalf("id/seq not populated: %+v", resp)
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(resp)
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("response activity JSON\n got %s\nwant %s", gotJSON, wantJSON)
	}
	// Exact key set of the pre-FSM shape.
	var keys map[string]any
	_ = json.Unmarshal(gotJSON, &keys)
	if len(keys) != 8 {
		t.Fatalf("activity JSON keys = %v", keys)
	}
	for _, k := range []string{"id", "kind", "date", "at", "by", "byName", "from", "to"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("activity JSON missing %q: %s", k, gotJSON)
		}
	}

	// Every stored column.
	var acts []model.ActivityEntry
	if err := e.s.db.Where("task_id = ?", v.ID).Find(&acts).Error; err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("stored rows = %d", len(acts))
	}
	st := acts[0]
	if st.ID != want.ID || st.TaskID != want.TaskID || st.Seq != want.Seq || st.Kind != want.Kind ||
		st.Date != want.Date || !st.At.Equal(want.At) || st.By == nil || *st.By != aliceID ||
		st.ByName != want.ByName || st.Text != "" || st.From != want.From || st.To != want.To || st.EditedAt != nil {
		t.Fatalf("stored row\n got %+v\nwant %+v", st, want)
	}

	// A fresh detail read renders the same JSON (UTC-normalized At).
	fresh, err := e.s.getTaskRaw(e.noUser, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	want.At = now.UTC()
	wantUTC, _ := json.Marshal(want)
	freshJSON, _ := json.Marshal(fresh.Activity[0])
	if !bytes.Equal(wantUTC, freshJSON) {
		t.Fatalf("read-back activity JSON\n got %s\nwant %s", freshJSON, wantUTC)
	}

	// No session user -> by/byName omitted (direct repo caller path; the
	// engine's actor falls back to systemActor, which is never persisted).
	v2 := tkCreate(t, e.asAlice, e.s, "b", nil)
	got2 := tkPatch(t, e.noUser, e.s, v2.ID, `{"status":"done"}`)
	j, _ := json.Marshal(got2.Activity[0])
	var k2 map[string]any
	_ = json.Unmarshal(j, &k2)
	if _, ok := k2["by"]; ok {
		t.Fatalf("no-user entry must omit by: %s", j)
	}
	if _, ok := k2["byName"]; ok {
		t.Fatalf("no-user entry must omit byName: %s", j)
	}
}

// 3. All 12 ordered pairs transition, one status row each.
func TestFSMAllTwelvePairs(t *testing.T) {
	e := tkSetup(t)
	pairs := 0
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			if from == to {
				continue
			}
			pairs++
			t.Run(from+"->"+to, func(t *testing.T) {
				v := tkCreate(t, e.asAlice, e.s, "p", func(tk *model.Task) { tk.Status = from })
				got := tkPatch(t, e.asAlice, e.s, v.ID, `{"status":"`+to+`"}`)
				if got.Status != to {
					t.Fatalf("status = %q", got.Status)
				}
				fresh, err := e.s.getTaskRaw(e.noUser, v.ID)
				if err != nil {
					t.Fatal(err)
				}
				if fresh.Status != to {
					t.Fatalf("stored status = %q", fresh.Status)
				}
				if n := statusRows(t, e.s, v.ID); n != 1 {
					t.Fatalf("status rows = %d", n)
				}
				if a := fresh.Activity[0]; a.From != from || a.To != to {
					t.Fatalf("activity %+v", a)
				}
			})
		}
	}
	if pairs != 12 {
		t.Fatalf("pairs = %d", pairs)
	}
}

// countingTransitions / countingStates instrument the engine: ExecuteTransition
// always reads state and looks up the transition, so zero calls on both
// proves it was never invoked.
type countingTransitions struct {
	fsm.TransitionStore
	n atomic.Int64
}

func (c *countingTransitions) FindTransition(ctx context.Context, from fsm.State, event string) (*fsm.Transition, error) {
	c.n.Add(1)
	return c.TransitionStore.FindTransition(ctx, from, event)
}

type countingStates struct {
	inner *taskStateStore
	n     atomic.Int64
}

func (c *countingStates) GetState(ctx context.Context, id string) (fsm.State, error) {
	c.n.Add(1)
	return c.inner.GetState(ctx, id)
}

func (c *countingStates) GetStateTx(ctx context.Context, tx *gorm.DB, id string) (fsm.State, error) {
	c.n.Add(1)
	return c.inner.GetStateTx(ctx, tx, id)
}

// 4. A patch that leaves status unchanged never enters the FSM.
func TestFSMNotInvokedWhenStatusUnchanged(t *testing.T) {
	e := tkSetup(t)
	real, err := loader.NewFromFS(taskTransitionsFile)
	if err != nil {
		t.Fatal(err)
	}
	ct := &countingTransitions{TransitionStore: real}
	cs := &countingStates{inner: &taskStateStore{db: e.s.db}}
	mgr := newTaskManagerFrom(e.s.db, ct)
	mgr.States = cs
	spy := newWithManager(e.s.db, mgr)

	v := tkCreate(t, e.asAlice, e.s, "t", nil)
	tkPatch(t, e.asAlice, spy, v.ID, `{"title":"t2"}`)
	tkPatch(t, e.asAlice, spy, v.ID, `{"status":"todo","priority":"high"}`) // same status, explicitly sent
	if ct.n.Load() != 0 || cs.n.Load() != 0 {
		t.Fatalf("FSM entered on status-unchanged patch: find=%d getState=%d", ct.n.Load(), cs.n.Load())
	}
	if n := statusRows(t, e.s, v.ID); n != 0 {
		t.Fatalf("status rows = %d", n)
	}

	// Contrast: a status change goes through it exactly once.
	tkPatch(t, e.asAlice, spy, v.ID, `{"status":"in_progress"}`)
	if ct.n.Load() != 1 || cs.n.Load() != 1 {
		t.Fatalf("status change: find=%d getState=%d, want 1/1", ct.n.Load(), cs.n.Load())
	}
}

// 5. Canary: status-derived side effects still happen through the FSM path.
func TestFSMPreservesStatusSideEffects(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAlice, e.s, "team", func(tk *model.Task) { tk.TeamID = tkStr(e.team) })
	tkExec(t, e.s, "UPDATE tasks SET week_of = ? WHERE id = ?", tkOldWeek, v.ID)
	got := tkPatch(t, e.asAlice, e.s, v.ID, `{"status":"done"}`)
	if got.CompletedAt == nil || !got.CompletedAt.Equal(got.UpdatedAt) {
		t.Fatalf("completedAt=%v updatedAt=%v", got.CompletedAt, got.UpdatedAt)
	}
	if string(got.WeekOf) != tkThisWeek() {
		t.Fatalf("weekOf=%q want %q", got.WeekOf, tkThisWeek())
	}
	fresh, err := e.s.getTaskRaw(e.noUser, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != model.StatusDone || fresh.CompletedAt == nil || !fresh.CompletedAt.Equal(*got.CompletedAt) ||
		string(fresh.WeekOf) != tkThisWeek() || !fresh.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("stored row diverges from response: %+v", fresh)
	}
}
