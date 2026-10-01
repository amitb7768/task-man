package service

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"taskman/internal/model"
)

var tkOldWeek = model.CurrentPeriodAt(model.HorizonWeekly, time.Now().AddDate(0, 0, -21))

func TestTaskCreateDerivesOwnerAndWeekOf(t *testing.T) {
	e := tkSetup(t)
	p := tkCreate(t, e.asAlice, e.s, "personal", func(tk *model.Task) {
		tk.WeekOf = "2000-W01"       // never client-settable
		tk.OwnerID = tkStr(e.bob.ID) // never client-settable
		tk.SeriesID = tkStr("x")     // never client-settable
		tk.Activity = []model.ActivityEntry{{ID: "evil", Kind: "note"}}
	})
	if p.OwnerID == nil || *p.OwnerID != e.alice.ID || p.WeekOf != "" || p.SeriesID != nil || p.Activity != nil {
		t.Fatalf("personal derivation wrong: %+v", p.Task)
	}
	tm := tkCreate(t, e.asAlice, e.s, "team", func(tk *model.Task) { tk.TeamID = tkStr(e.team) })
	if tm.OwnerID != nil || string(tm.WeekOf) != tkThisWeek() {
		t.Fatalf("team derivation wrong: owner=%v weekOf=%q", tm.OwnerID, tm.WeekOf)
	}
	_, err := e.s.CreateTask(e.asAlice, &model.Task{Title: "x", Horizon: "daily", Period: tkToday(), TeamID: tkStr(e.otherTeam)})
	tkWantErr(t, err, 403, "not a member of that team")
	_, err = e.s.CreateTask(e.asAlice, &model.Task{Title: "x", Horizon: "daily", Period: tkToday(), ParentID: tkStr("nope")})
	tkWantErr(t, err, 400, "parent task not found")
	_, err = e.s.CreateTask(e.asAlice, &model.Task{Title: "x", Horizon: "daily", Period: tkToday(), TeamID: tkStr(e.team), AssigneeID: tkStr(e.admin.ID)})
	tkWantErr(t, err, 400, "assignee does not belong to team")
	// Stored NULL, not '' — the $exists parity the board depends on.
	var nulls int64
	e.s.db.Raw("SELECT count(*) FROM tasks WHERE id = ? AND due_date IS NULL AND week_of IS NULL AND notes IS NULL", p.ID).Scan(&nulls)
	if nulls != 1 {
		t.Fatal("empty NullStr fields must persist as NULL")
	}
}

// The status side-effect matrix from patchTaskOnce :891-962.
func TestTaskPatchStatusSideEffects(t *testing.T) {
	e := tkSetup(t)
	cases := []struct {
		from, to        string
		wantCompleted   bool
		wantWeekBumped  bool
		wantKeepPrevCmp bool // completedAt preserved from before
	}{
		{"todo", "done", true, true, false},
		{"todo", "cancelled", false, true, false},
		{"in_progress", "done", true, true, false},
		{"done", "cancelled", false, false, false}, // leaving done clears; terminal->terminal no bump
		{"done", "todo", false, false, false},
		{"cancelled", "done", true, false, false}, // into done sets; was terminal -> no bump
		{"cancelled", "todo", false, false, false},
		{"done", "done", true, false, true}, // no transition: preserved, no log
	}
	for _, c := range cases {
		t.Run(c.from+"->"+c.to, func(t *testing.T) {
			v := tkCreate(t, e.asAlice, e.s, "m", func(tk *model.Task) { tk.TeamID = tkStr(e.team); tk.Status = c.from })
			tkExec(t, e.s, "UPDATE tasks SET week_of = ? WHERE id = ?", tkOldWeek, v.ID)
			before, _ := e.s.getTaskRaw(e.noUser, v.ID)
			got := tkPatch(t, e.asAlice, e.s, v.ID, `{"status":"`+c.to+`"}`)
			if (got.CompletedAt != nil) != c.wantCompleted {
				t.Fatalf("completedAt=%v want set=%v", got.CompletedAt, c.wantCompleted)
			}
			if c.wantKeepPrevCmp && !got.CompletedAt.Equal(*before.CompletedAt) {
				t.Fatal("completedAt must be preserved when status is unchanged")
			}
			wantWeek := tkOldWeek
			if c.wantWeekBumped {
				wantWeek = tkThisWeek()
			}
			if string(got.WeekOf) != wantWeek {
				t.Fatalf("weekOf=%q want %q", got.WeekOf, wantWeek)
			}
			// The response must equal a fresh read (µs truncation).
			fresh, _ := e.s.getTaskRaw(e.noUser, v.ID)
			if !fresh.UpdatedAt.Equal(got.UpdatedAt) || (fresh.CompletedAt == nil) != (got.CompletedAt == nil) {
				t.Fatal("patch response diverges from stored row")
			}
			if c.from == c.to {
				if len(fresh.Activity) != 0 {
					t.Fatal("no-op status must not log")
				}
				return
			}
			if len(fresh.Activity) != 1 || fresh.Activity[0].Kind != "status" ||
				fresh.Activity[0].From != c.from || fresh.Activity[0].To != c.to ||
				fresh.Activity[0].By == nil || *fresh.Activity[0].By != e.alice.ID || fresh.Activity[0].ByName != "Alice" {
				t.Fatalf("status auto-log wrong: %+v", fresh.Activity)
			}
		})
	}
}

func TestTaskPatchTeamFlipAndWeekOfRules(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAdmin, e.s, "flip", func(tk *model.Task) { tk.TeamID = tkStr(e.team) })

	// USER may never flip nil-ness, nor touch weekOf under any key casing.
	_, err := e.s.PatchTask(e.asAlice, v.ID, []byte(`{"teamId":null}`))
	tkWantErr(t, err, 403, "only an admin can change a task's team")
	_, err = e.s.PatchTask(e.asAlice, v.ID, []byte(`{"WEEKOF":"`+tkOldWeek+`"}`))
	tkWantErr(t, err, 403, "only an admin can change weekOf")

	// ADMIN weekOf move primitive.
	got := tkPatch(t, e.asAdmin, e.s, v.ID, `{"weekof":"`+tkOldWeek+`"}`)
	if string(got.WeekOf) != tkOldWeek {
		t.Fatalf("admin weekOf move: %q", got.WeekOf)
	}
	_, err = e.s.PatchTask(e.asAdmin, v.ID, []byte(`{"weekOf":"garbage"}`))
	tkWantErr(t, err, 400, "")

	// team -> personal: ownerId = caller, weekOf cleared.
	got = tkPatch(t, e.asAdmin, e.s, v.ID, `{"teamId":null}`)
	if got.TeamID != nil || got.OwnerID == nil || *got.OwnerID != e.admin.ID || got.WeekOf != "" {
		t.Fatalf("team->personal: %+v", got.Task)
	}
	_, err = e.s.PatchTask(e.asAdmin, v.ID, []byte(`{"weekOf":"`+tkThisWeek()+`"}`))
	tkWantErr(t, err, 400, "weekOf is not valid on a personal task")

	// personal -> team: ownerId cleared, weekOf = current week.
	got = tkPatch(t, e.asAdmin, e.s, v.ID, `{"teamId":"`+e.otherTeam+`"}`)
	if got.OwnerID != nil || string(got.WeekOf) != tkThisWeek() {
		t.Fatalf("personal->team: %+v", got.Task)
	}
}

func TestTaskPatchActivityAndImmutables(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAlice, e.s, "a", nil)
	if _, err := e.s.AddNote(e.asAlice, v.ID, model.NoteInput{Text: "real"}); err != nil {
		t.Fatal(err)
	}
	got := tkPatch(t, e.asAlice, e.s, v.ID,
		`{"title":"b","id":"hijack","createdAt":"2000-01-01T00:00:00Z","activity":[{"id":"x","kind":"note","text":"fake"}]}`)
	if got.ID != v.ID || got.Title != "b" || !got.CreatedAt.Equal(v.CreatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("immutables not preserved: %+v", got.Task)
	}
	if len(got.Activity) != 1 || got.Activity[0].Text != "real" {
		t.Fatalf("client activity must be discarded: %+v", got.Activity)
	}
	// A rejected patch leaves no trace (no status log, no write).
	_, err := e.s.PatchTask(e.asAlice, v.ID, []byte(`{"status":"done","priority":"urgent"}`))
	tkWantErr(t, err, 400, `invalid priority "urgent"`)
	fresh, _ := e.s.getTaskRaw(e.noUser, v.ID)
	if fresh.Status != "todo" || len(fresh.Activity) != 1 {
		t.Fatalf("rejected patch leaked: %+v", fresh)
	}
	_, err = e.s.PatchTask(e.asAlice, "no-such-id", []byte(`{}`))
	tkWantErr(t, err, 404, "task not found")
	_, err = e.s.PatchTask(e.asAlice, v.ID, []byte(`{bad`))
	tkWantErr(t, err, 400, "")
}

func TestTaskPatchReanchorsOnlyOnFreqOrInterval(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAlice, e.s, "r", func(tk *model.Task) {
		tk.Horizon, tk.Period = model.HorizonWeekly, tkThisWeek()
		tk.Recurrence = &model.Recurrence{Freq: model.FreqWeekly, Anchor: "1999-W01"}
	})
	if v.Recurrence.Anchor != tkThisWeek() || v.SeriesID == nil || *v.SeriesID != v.ID {
		t.Fatalf("create anchor/series: %+v", v.Recurrence)
	}
	next, _ := model.NextPeriod(model.HorizonWeekly, tkThisWeek())
	got := tkPatch(t, e.asAlice, e.s, v.ID, `{"period":"`+next+`","recurrence":{"anchor":"1999-W01"}}`)
	if got.Recurrence.Anchor != tkThisWeek() {
		t.Fatalf("anchor must be kept without freq/interval change: %q", got.Recurrence.Anchor)
	}
	got = tkPatch(t, e.asAlice, e.s, v.ID, `{"recurrence":{"interval":2}}`)
	if got.Recurrence.Anchor != next {
		t.Fatalf("interval change must re-anchor to period: %q", got.Recurrence.Anchor)
	}
}

func TestTaskCascadeDeleteRestoreRoundTrip(t *testing.T) {
	e := tkSetup(t)
	root := tkCreate(t, e.asAlice, e.s, "root", func(tk *model.Task) {
		tk.TeamID = tkStr(e.team)
		tk.Horizon, tk.Period = model.HorizonMonthly, model.CurrentPeriod(model.HorizonMonthly)
	})
	kid := tkCreate(t, e.asAlice, e.s, "kid", func(tk *model.Task) {
		tk.TeamID, tk.ParentID, tk.AssigneeID = tkStr(e.team), tkStr(root.ID), tkStr(e.alice.ID)
		tk.Horizon, tk.Period = model.HorizonWeekly, tkThisWeek()
	})
	grand := tkCreate(t, e.asAlice, e.s, "grand", func(tk *model.Task) { tk.TeamID, tk.ParentID = tkStr(e.team), tkStr(kid.ID) })
	tkPatch(t, e.asAlice, e.s, grand.ID, `{"status":"done"}`)
	for _, txt := range []string{"n1", "n2", "n3"} {
		if _, err := e.s.AddNote(e.asAlice, kid.ID, model.NoteInput{Text: txt}); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string]*model.Task{}
	for _, id := range []string{root.ID, kid.ID, grand.ID} {
		before[id], _ = e.s.getTaskRaw(e.noUser, id)
	}

	deleted, err := e.s.DeleteTaskCascade(e.asAdmin, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ids := tkIDs(deleted); len(ids) != 3 || ids[0] != root.ID || ids[1] != kid.ID || ids[2] != grand.ID {
		t.Fatalf("delete order (root, then BFS): %v", ids)
	}
	if deleted[0].Progress != (model.Progress{Done: 0, Total: 1}) || deleted[1].Progress != (model.Progress{Done: 1, Total: 1}) {
		t.Fatalf("progress must be computed pre-delete: %+v %+v", deleted[0].Progress, deleted[1].Progress)
	}
	if len(deleted[1].Activity) != 3 {
		t.Fatalf("undo snapshot must carry activity: %+v", deleted[1].Activity)
	}
	var n int64
	e.s.db.Raw("SELECT count(*) FROM task_activity").Scan(&n)
	if n != 0 {
		t.Fatal("activity must cascade with the task")
	}

	// Round-trip through JSON (what the client sends back), children first to
	// prove restore orders parent-before-child itself.
	raw, _ := json.Marshal([]model.TaskView{deleted[2], deleted[1], deleted[0]})
	var payload []model.Task
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	cnt, err := e.s.RestoreTasks(e.asAdmin, payload)
	if err != nil || cnt != 3 {
		t.Fatalf("restore: %d %v", cnt, err)
	}
	for id, b := range before {
		a, err := e.s.getTaskRaw(e.noUser, id)
		if err != nil {
			t.Fatal(err)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) || !a.UpdatedAt.Equal(b.UpdatedAt) ||
			(b.CompletedAt != nil && !a.CompletedAt.Equal(*b.CompletedAt)) || a.WeekOf != b.WeekOf {
			t.Fatalf("timestamps not preserved for %s", id)
		}
		if len(a.Activity) != len(b.Activity) {
			t.Fatalf("activity not replayed for %s", id)
		}
		for i := range a.Activity {
			if a.Activity[i].ID != b.Activity[i].ID || a.Activity[i].Text != b.Activity[i].Text || !a.Activity[i].At.Equal(b.Activity[i].At) {
				t.Fatalf("activity order/content differs for %s at %d", id, i)
			}
		}
	}
	// Replay twice: duplicates skipped silently, nothing doubled.
	cnt, err = e.s.RestoreTasks(e.asAdmin, payload)
	if err != nil || cnt != 0 {
		t.Fatalf("second restore: %d %v", cnt, err)
	}
	e.s.db.Raw("SELECT count(*) FROM task_activity").Scan(&n)
	if n != 4 {
		t.Fatalf("activity doubled on replay: %d", n)
	}
}

func TestTaskRestoreRejectsDanglingRefs(t *testing.T) {
	e := tkSetup(t)
	now := time.Now()
	good := model.Task{ID: NewID(), Title: "ok", Horizon: "daily", Period: tkToday(), Status: "todo", OwnerID: tkStr(e.alice.ID), CreatedAt: now, UpdatedAt: now}
	bad := model.Task{ID: NewID(), Title: "bad", Horizon: "daily", Period: tkToday(), Status: "todo", TeamID: tkStr("gone-team"), CreatedAt: now, UpdatedAt: now}
	_, err := e.s.RestoreTasks(e.asAdmin, []model.Task{good, bad})
	tkWantErr(t, err, 400, "restore: task "+bad.ID+" references missing team gone-team")
	orphan := good
	orphan.ID, orphan.ParentID = NewID(), tkStr("gone-parent")
	_, err = e.s.RestoreTasks(e.asAdmin, []model.Task{orphan})
	tkWantErr(t, err, 400, "")
	var n int64
	e.s.db.Raw("SELECT count(*) FROM tasks").Scan(&n)
	if n != 0 {
		t.Fatal("a rejected restore must write nothing")
	}
}

func TestTaskRescheduleKeepsAnchorWart(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAlice, e.s, "r", func(tk *model.Task) {
		tk.Period = tkDaysAgo(3)
		tk.DueDate = model.NullStr(tkDaysAgo(3))
		tk.Recurrence = &model.Recurrence{Freq: model.FreqDaily}
	})
	foreign := tkCreate(t, e.asBob, e.s, "bob's", nil)
	n, err := e.s.Reschedule(e.asAlice, []string{v.ID, foreign.ID, "missing"})
	if err != nil || n != 1 {
		t.Fatalf("Reschedule: %d %v", n, err)
	}
	got, _ := e.s.getTaskRaw(e.noUser, v.ID)
	if got.Period != tkToday() || string(got.DueDate) != tkToday() {
		t.Fatalf("not rescheduled: %+v", got)
	}
	if got.Recurrence.Anchor != tkDaysAgo(3) {
		t.Fatalf("known wart: anchor must NOT move with period (got %q)", got.Recurrence.Anchor)
	}
}

func TestTaskScopingAdminCannotSeePersonal(t *testing.T) {
	e := tkSetup(t)
	p := tkCreate(t, e.asAlice, e.s, "alice-private", func(tk *model.Task) {
		tk.Period, tk.DueDate = tkDaysAgo(2), model.NullStr(tkDaysAgo(2))
	})
	_, err := e.s.GetTaskDetail(e.asAdmin, p.ID)
	tkWantErr(t, err, 403, "not allowed")
	_, err = e.s.PatchTask(e.asAdmin, p.ID, []byte(`{"title":"x"}`))
	tkWantErr(t, err, 403, "not allowed")
	_, err = e.s.AddNote(e.asAdmin, p.ID, model.NoteInput{Text: "x"})
	tkWantErr(t, err, 403, "not allowed")
	res, _ := e.s.Search(e.asAdmin, model.SearchParams{Q: "private"})
	overdue, _, _ := e.s.ViewAttention(e.asAdmin, nil)
	day, _, _ := e.s.ViewDay(e.asAdmin, tkDaysAgo(2), nil)
	if len(res)+len(overdue)+len(day) != 0 {
		t.Fatal("ADMIN must never see another user's personal task")
	}
	res, _ = e.s.Search(e.asAlice, model.SearchParams{Q: "private"})
	overdue, _, _ = e.s.ViewAttention(e.asAlice, nil)
	if len(res) != 1 || len(overdue) != 1 {
		t.Fatal("owner must see their own personal task")
	}
	// Team-less USER: IN (NULL) matches nothing, no error.
	loner := tkMember(t, e.s, "Loner", model.RoleUser)
	tkCreate(t, e.asAlice, e.s, "team-private", func(tk *model.Task) { tk.TeamID = tkStr(e.team) })
	res, err = e.s.Search(asUser(loner), model.SearchParams{Q: "private"})
	if err != nil || len(res) != 0 {
		t.Fatalf("team-less USER search: %v %v", res, err)
	}
	// Children filtered by access in the detail.
	parent := tkCreate(t, e.asAlice, e.s, "tp", func(tk *model.Task) { tk.TeamID = tkStr(e.team) })
	tkCreate(t, e.asAlice, e.s, "tc", func(tk *model.Task) { tk.TeamID, tk.ParentID = tkStr(e.team), tkStr(parent.ID) })
	tkExec(t, e.s, "INSERT INTO tasks (id,title,horizon,period,status,owner_id,parent_id,created_at,updated_at) VALUES ('pc','bob-private-child','daily',?, 'todo', ?, ?, now(), now())", tkToday(), e.bob.ID, parent.ID)
	d, err := e.s.GetTaskDetail(e.asAlice, parent.ID)
	if err != nil || len(d.Children) != 1 || d.Progress.Total != 2 {
		t.Fatalf("detail children scoping: %+v %v", d, err)
	}
}

// Goroutine A holds PatchTask's FOR UPDATE lock (hook sleeps inside the tx);
// goroutine B adds a note meanwhile. Both must land; neither clobbered.
func TestTaskPatchConcurrentNoteSurvives(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAlice, e.s, "c", func(tk *model.Task) { tk.TeamID = tkStr(e.team) })

	locked := make(chan struct{})
	var once sync.Once
	patchLockedHook = func(string) {
		once.Do(func() { close(locked) })
		time.Sleep(300 * time.Millisecond)
	}
	t.Cleanup(func() { patchLockedHook = nil })

	var wg sync.WaitGroup
	var patchErr, noteErr error
	var note *model.ActivityEntry
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, patchErr = e.s.PatchTask(e.asAlice, v.ID, []byte(`{"status":"in_progress","title":"patched"}`))
	}()
	go func() {
		defer wg.Done()
		<-locked
		note, noteErr = e.s.AddNote(e.asBob, v.ID, model.NoteInput{Text: "during patch"})
	}()
	wg.Wait()
	if patchErr != nil || noteErr != nil {
		t.Fatalf("patch=%v note=%v", patchErr, noteErr)
	}
	got, _ := e.s.getTaskRaw(e.noUser, v.ID)
	if got.Title != "patched" || got.Status != "in_progress" {
		t.Fatalf("patch lost: %+v", got)
	}
	if len(got.Activity) != 2 || got.Activity[0].Kind != "status" || got.Activity[1].ID != note.ID {
		t.Fatalf("want [status, note] in seq order, got %+v", got.Activity)
	}
	if !got.UpdatedAt.Equal(note.At) {
		t.Fatalf("note serialized behind the patch lock must own updatedAt: %v vs %v", got.UpdatedAt, note.At)
	}

	// Two concurrent patches: the second waits on the lock (no 409).
	patchLockedHook = func(string) { time.Sleep(100 * time.Millisecond) }
	errs := make(chan error, 2)
	for _, st := range []string{"done", "cancelled"} {
		go func(st string) {
			_, err := e.s.PatchTask(e.asAlice, v.ID, []byte(`{"status":"`+st+`"}`))
			errs <- err
		}(st)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent patch: %v", err)
		}
	}
	got, _ = e.s.getTaskRaw(e.noUser, v.ID)
	if len(got.Activity) != 4 {
		t.Fatalf("both status transitions must log: %+v", got.Activity)
	}
}
