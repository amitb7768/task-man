package service

import (
	"testing"

	"taskman/internal/model"
)

// TestProgressGroupedMatchesPerTask pins the P3 N+1 fix: toViews' one
// grouped query must yield exactly what the old per-task count did, on a
// mixed tree — a parent with done/cancelled/todo/in_progress children, a
// child that is itself a parent (only DIRECT children count), and a task
// with no children at all.
func TestProgressGroupedMatchesPerTask(t *testing.T) {
	e := tkSetup(t)
	ctx := e.asAlice

	parent := tkCreate(t, ctx, e.s, "parent", nil)
	withStatus := func(status string) func(*model.Task) {
		return func(tk *model.Task) { tk.ParentID = &parent.ID; tk.Status = status }
	}
	tkCreate(t, ctx, e.s, "c-done", withStatus(model.StatusDone))
	tkCreate(t, ctx, e.s, "c-cancelled", withStatus(model.StatusCancelled))
	mid := tkCreate(t, ctx, e.s, "c-todo", withStatus(model.StatusTodo))
	tkCreate(t, ctx, e.s, "c-in-progress", withStatus(model.StatusInProgress))
	tkCreate(t, ctx, e.s, "grandchild", func(tk *model.Task) { tk.ParentID = &mid.ID; tk.Status = model.StatusDone })
	lone := tkCreate(t, ctx, e.s, "lone", nil)

	want := map[string]model.Progress{
		parent.ID: {Done: 2, Total: 4}, // done + cancelled count as done; grandchild excluded
		mid.ID:    {Done: 1, Total: 1},
		lone.ID:   {},
	}

	// List path: one grouped query for the whole list.
	views, _, err := e.s.ViewDay(ctx, tkToday(), nil)
	if err != nil {
		t.Fatalf("ViewDay: %v", err)
	}
	if len(views) != 7 {
		t.Fatalf("ViewDay: want 7 tasks, got %d", len(views))
	}
	for _, v := range views {
		// Oracle: the pre-P3 per-task query, verbatim.
		var row struct{ Total, Done int }
		if err := e.s.db.Raw(`SELECT count(*) AS total,
			count(*) FILTER (WHERE status IN ?) AS done
			FROM tasks WHERE parent_id = ?`, terminalStatuses, v.ID).Scan(&row).Error; err != nil {
			t.Fatalf("oracle: %v", err)
		}
		if got, old := v.Progress, (model.Progress{Done: row.Done, Total: row.Total}); got != old {
			t.Errorf("%s: grouped progress %+v != per-task %+v", v.Title, got, old)
		}
		if w, ok := want[v.ID]; ok && v.Progress != w {
			t.Errorf("%s: progress %+v, want %+v", v.Title, v.Progress, w)
		}
	}

	// Single-task path (toView via the same helper with one id).
	for id, w := range want {
		d, err := e.s.GetTaskDetail(ctx, id)
		if err != nil {
			t.Fatalf("GetTaskDetail %s: %v", id, err)
		}
		if d.Progress != w {
			t.Errorf("detail %s: progress %+v, want %+v", d.Title, d.Progress, w)
		}
	}
}
