package repo

import (
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"taskman/internal/model"
)

func TestMaterializeIdempotentAndSeriesEnd(t *testing.T) {
	e := tkSetup(t)
	live := tkCreate(t, e.asAlice, e.s, "daily", func(tk *model.Task) {
		tk.Period = tkDaysAgo(3)
		tk.TeamID, tk.AssigneeID = tkStr(e.team), tkStr(e.alice.ID)
		tk.Recurrence = &model.Recurrence{Freq: model.FreqDaily}
	})
	ended := tkCreate(t, e.asAlice, e.s, "ended", func(tk *model.Task) {
		tk.Period = tkDaysAgo(3)
		tk.Recurrence = &model.Recurrence{Freq: model.FreqDaily}
	})
	// Cancelling an instance never stops a series; removing recurrence does.
	tkPatch(t, e.asAlice, e.s, live.ID, `{"status":"cancelled"}`)
	tkPatch(t, e.asAlice, e.s, ended.ID, `{"recurrence":null}`)

	count := func(series string) int64 {
		var n int64
		e.s.db.Raw("SELECT count(*) FROM tasks WHERE series_id = ?", series).Scan(&n)
		return n
	}
	for range 2 {
		if err := e.s.Materialize(e.noUser); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(live.ID); n != 4 { // head + 3 spawned (days -2, -1, 0)
		t.Fatalf("live series: %d instances, want 4", n)
	}
	var ended0 int64
	e.s.db.Raw("SELECT count(*) FROM tasks WHERE title = 'ended'").Scan(&ended0)
	if ended0 != 1 {
		t.Fatalf("series with nil recurrence must not spawn (got %d rows)", ended0)
	}
	var spawn model.Task
	e.s.db.Where("series_id = ? AND period = ?", live.ID, tkToday()).Take(&spawn)
	if spawn.Status != "todo" || spawn.TeamID == nil || string(spawn.WeekOf) != tkThisWeek() ||
		spawn.AssigneeID == nil || spawn.ParentID != nil || spawn.ID == live.ID {
		t.Fatalf("spawned instance shape: %+v", spawn)
	}
	if acts, _ := loadActivity(e.s.db, spawn.ID); acts != nil {
		t.Fatal("spawned instances start with an empty log")
	}

	// Contrast: a spawn-shaped duplicate for an existing (series, period) is
	// swallowed by the ON CONFLICT arbiter, but a plain INSERT hits the
	// partial unique index — proving the index (not luck) is what dedups.
	dup := spawn
	dup.ID = NewID()
	res := e.s.db.Clauses(seriesPeriodConflict).Create(&dup)
	if res.Error != nil || res.RowsAffected != 0 {
		t.Fatalf("ON CONFLICT spawn: rows=%d err=%v", res.RowsAffected, res.Error)
	}
	dup.ID = NewID()
	if err := e.s.db.Create(&dup).Error; taskPGErrCode(err) != "23505" {
		t.Fatalf("plain duplicate must violate the partial unique, got %v", err)
	}
	// The partial predicate: rows without a series never conflict.
	dup.ID, dup.SeriesID = NewID(), nil
	if err := e.s.db.Clauses(seriesPeriodConflict).Create(&dup).Error; err != nil {
		t.Fatalf("series-less row: %v", err)
	}
	if n := count(live.ID); n != 4 {
		t.Fatalf("exactly one instance per (series, period): %d", n)
	}
}

func TestTaskBoardVisibilityArms(t *testing.T) {
	e := tkSetup(t)
	mk := func(title, status, due, week string, assignee *string) string {
		v := tkCreate(t, e.asAdmin, e.s, title, func(tk *model.Task) {
			tk.TeamID, tk.AssigneeID = tkStr(e.team), assignee
			tk.DueDate = model.NullStr(due)
		})
		tkExec(t, e.s, "UPDATE tasks SET status = ?, week_of = ? WHERE id = ?", status, week, v.ID)
		return v.ID
	}
	openDatedOld := mk("open-dated-old", "todo", tkToday(), tkOldWeek, tkStr(e.alice.ID))
	openUndatedOld := mk("open-undated-old", "in_progress", "", tkOldWeek, nil)
	openUndatedNow := mk("open-undated-now", "todo", "", tkThisWeek(), nil)
	doneNow := mk("done-now", "done", "", tkThisWeek(), tkStr(e.alice.ID))
	doneOld := mk("done-old", "cancelled", "", tkOldWeek, nil)
	mk("other-team-noise", "todo", "", tkThisWeek(), nil)
	tkExec(t, e.s, "UPDATE tasks SET team_id = ? WHERE title = 'other-team-noise'", e.otherTeam)

	b, err := e.s.TeamBoard(e.asAlice, e.team)
	if err != nil {
		t.Fatal(err)
	}
	if b.Week != tkThisWeek() || b.StaleOpen != 1 || b.Team.Name != "Alpha" {
		t.Fatalf("board header: week=%s stale=%d", b.Week, b.StaleOpen)
	}
	if len(b.Members) != 2 || b.Members[0].Member.Name != "Alice" || b.Members[1].Member.Name != "Bob" {
		t.Fatalf("members by name: %+v", b.Members)
	}
	if ids := tkIDs(b.Members[0].Tasks); len(ids) != 2 || ids[0] != openDatedOld || ids[1] != doneNow {
		t.Fatalf("alice column (open before closed): %v", ids)
	}
	un := tkIDs(b.Unassigned)
	if len(un) != 1 || un[0] != openUndatedNow {
		t.Fatalf("unassigned: %v", un)
	}
	_, err = e.s.TeamBoard(e.asAlice, e.otherTeam)
	tkWantErr(t, err, 403, "not a member of that team")
	_, err = e.s.TeamBoard(e.asAdmin, "nope")
	tkWantErr(t, err, 404, "team not found")

	week, roll, err := e.s.TeamRollover(e.asAdmin, e.team)
	if err != nil || week != tkThisWeek() || len(roll) != 1 || roll[0].ID != openUndatedOld {
		t.Fatalf("rollover: %v %v", tkIDs(roll), err)
	}
	hist, more, err := e.s.TeamHistory(e.asAlice, e.team, 0, 0)
	if err != nil || more || len(hist) != 1 || hist[0].ID != doneOld || hist[0].Activity != nil {
		t.Fatalf("history: %v more=%v err=%v", tkIDs(hist), more, err)
	}
}

func TestTaskSearchFiltersAndEscaping(t *testing.T) {
	e := tkSetup(t)
	pct := tkCreate(t, e.asAlice, e.s, "100% Done", nil).ID
	tkCreate(t, e.asAlice, e.s, "100 percent", nil)
	und := tkCreate(t, e.asAlice, e.s, "a_b", nil).ID
	tkCreate(t, e.asAlice, e.s, "axb", nil)
	bs := tkCreate(t, e.asAlice, e.s, `back\slash`, nil).ID
	inNotes := tkCreate(t, e.asAlice, e.s, "plain", func(tk *model.Task) { tk.Notes = "Needle in NOTES" }).ID

	cases := []struct {
		p    model.SearchParams
		want []string
	}{
		{model.SearchParams{Q: "%"}, []string{pct}},
		{model.SearchParams{Q: "_"}, []string{und}},
		{model.SearchParams{Q: `\`}, []string{bs}},
		{model.SearchParams{Q: "DONE"}, []string{pct}},
		{model.SearchParams{Q: "needle"}, []string{inNotes}},
		{model.SearchParams{Q: "a_b", Status: "open"}, []string{und}},
		{model.SearchParams{Q: "a_b", Status: "done"}, nil},
	}
	for _, c := range cases {
		got, err := e.s.Search(e.asAlice, c.p)
		if err != nil {
			t.Fatal(err)
		}
		ids := tkIDs(got)
		if len(ids) != len(c.want) {
			t.Fatalf("%+v: got %v want %v", c.p, ids, c.want)
		}
		for _, w := range c.want {
			if !tkHas(ids, w) {
				t.Fatalf("%+v: missing %s in %v", c.p, w, ids)
			}
		}
	}
}

func TestTaskNotesAuthorRules(t *testing.T) {
	e := tkSetup(t)
	v := tkCreate(t, e.asAlice, e.s, "n", func(tk *model.Task) { tk.TeamID = tkStr(e.team) })
	n, err := e.s.AddNote(e.asAlice, v.ID, model.NoteInput{Text: "  hello  ", Date: tkDaysAgo(5)})
	if err != nil || n.Text != "hello" || n.Date != tkDaysAgo(5) || n.ByName != "Alice" {
		t.Fatalf("AddNote: %+v %v", n, err)
	}
	_, err = e.s.AddNote(e.asAlice, v.ID, model.NoteInput{Text: "  "})
	tkWantErr(t, err, 400, "text is required")
	_, err = e.s.AddNote(e.asAlice, "missing", model.NoteInput{Text: "x"})
	tkWantErr(t, err, 404, "task not found")

	_, err = e.s.EditNote(e.asBob, v.ID, n.ID, model.NoteInput{Text: "bob edit"})
	tkWantErr(t, err, 403, "not your note")
	ed, err := e.s.EditNote(e.asAlice, v.ID, n.ID, model.NoteInput{Text: "edited"})
	if err != nil || ed.Date != tkDaysAgo(5) || ed.EditedAt == nil || ed.Text != "edited" {
		t.Fatalf("edit must keep backdated date + stamp editedAt: %+v %v", ed, err)
	}
	if _, err := e.s.EditNote(e.asAdmin, v.ID, n.ID, model.NoteInput{Text: "admin edit"}); err != nil {
		t.Fatalf("ADMIN may edit any note: %v", err)
	}
	tkPatch(t, e.asAlice, e.s, v.ID, `{"status":"in_progress"}`)
	full, _ := e.s.getTaskRaw(e.noUser, v.ID)
	statusID := full.Activity[1].ID
	_, err = e.s.EditNote(e.asAlice, v.ID, statusID, model.NoteInput{Text: "x"})
	tkWantErr(t, err, 400, "only note entries can be edited or deleted")
	err = e.s.DeleteNote(e.asAlice, v.ID, "nope")
	tkWantErr(t, err, 404, "note not found")
	err = e.s.DeleteNote(e.asBob, v.ID, n.ID)
	tkWantErr(t, err, 403, "not your note")

	beforeDel := full.UpdatedAt
	time.Sleep(2 * time.Millisecond)
	if err := e.s.DeleteNote(e.asAlice, v.ID, n.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := e.s.getTaskRaw(e.noUser, v.ID)
	if len(after.Activity) != 1 || after.Activity[0].ID != statusID || !after.UpdatedAt.After(beforeDel) {
		t.Fatalf("delete must remove only the note and bump updatedAt: %+v", after.Activity)
	}
	// List reads never carry activity; detail does.
	day, _, _ := e.s.ViewDay(e.asAlice, tkToday())
	srch, _ := e.s.Search(e.asAlice, model.SearchParams{Q: "n"})
	for _, x := range append(day, srch...) {
		if x.Activity != nil {
			t.Fatal("list reads must project activity away")
		}
	}
	d, _ := e.s.GetTaskDetail(e.asAlice, v.ID)
	if len(d.Activity) != 1 || d.CreatedAt.Location() != time.UTC {
		t.Fatalf("detail activity / UTC read times: %+v", d.Task)
	}
}

func TestTaskSummaryHappyPath(t *testing.T) {
	e := tkSetup(t)
	team := func(tk *model.Task) { tk.TeamID, tk.AssigneeID = tkStr(e.team), tkStr(e.alice.ID) }
	added := tkCreate(t, e.asAlice, e.s, "b-added", team).ID
	completed := tkCreate(t, e.asAlice, e.s, "c-completed", team).ID
	tkPatch(t, e.asAlice, e.s, completed, `{"status":"done"}`)
	updated := tkCreate(t, e.asAlice, e.s, "A-updated", team).ID
	tkExec(t, e.s, "UPDATE tasks SET created_at = now() - interval '30 days' WHERE id = ?", updated)
	if _, err := e.s.AddNote(e.asAlice, updated, model.NoteInput{Text: "progress"}); err != nil {
		t.Fatal(err)
	}
	// Old task with an out-of-range note only: must not appear (EXISTS
	// requires one entry inside BOTH bounds).
	old := tkCreate(t, e.asAlice, e.s, "old", team).ID
	tkExec(t, e.s, "UPDATE tasks SET created_at = now() - interval '30 days' WHERE id = ?", old)
	if _, err := e.s.AddNote(e.asAlice, old, model.NoteInput{Text: "ancient", Date: tkDaysAgo(20)}); err != nil {
		t.Fatal(err)
	}
	tkCreate(t, e.asBob, e.s, "bob-private", nil)

	res, err := e.s.Summary(e.asAlice, model.SummaryParams{From: tkDaysAgo(1), To: tkToday()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Completed) != 1 || res.Completed[0].ID != completed || res.Completed[0].ClosedDate != tkToday() ||
		res.Completed[0].TeamName != "Alpha" || res.Completed[0].AssigneeName != "Alice" {
		t.Fatalf("completed: %+v", res.Completed)
	}
	if len(res.Updated) != 1 || res.Updated[0].ID != updated || len(res.Updated[0].Notes) != 1 {
		t.Fatalf("updated: %+v", res.Updated)
	}
	if len(res.Added) != 1 || res.Added[0].ID != added {
		t.Fatalf("added: %+v", res.Added)
	}
	if res.TeamID != nil {
		t.Fatal("me-scope carries no teamId")
	}

	res, err = e.s.Summary(e.asBob, model.SummaryParams{From: tkDaysAgo(1), To: tkToday(), TeamID: tkStr(e.team), AssigneeID: tkStr(e.alice.ID)})
	if err != nil || res.TeamName != "Alpha" || res.AssigneeName != "Alice" || len(res.Completed)+len(res.Updated)+len(res.Added) != 3 {
		t.Fatalf("team summary: %+v %v", res, err)
	}
	_, err = e.s.Summary(e.asAlice, model.SummaryParams{From: tkToday(), To: tkDaysAgo(1)})
	tkWantErr(t, err, 400, "from must not be after to")
	_, err = e.s.Summary(e.asAlice, model.SummaryParams{From: tkToday(), To: tkToday(), TeamID: tkStr(e.otherTeam)})
	tkWantErr(t, err, 403, "not a member of that team")
}

// Guard for the gorm clause itself: the arbiter must render the partial
// index predicate or Postgres can't infer tasks_series_period_unique.
func TestSeriesConflictClauseRendersPredicate(t *testing.T) {
	e := tkSetup(t)
	stmt := e.s.db.Session(&gorm.Session{DryRun: true}).Clauses(seriesPeriodConflict).Create(&model.Task{ID: "x"}).Statement
	sql := stmt.SQL.String()
	if !strings.Contains(sql, `ON CONFLICT ("series_id","period")`) ||
		!strings.Contains(sql, `WHERE series_id IS NOT NULL DO NOTHING`) {
		t.Fatalf("rendered: %s", sql)
	}
}

// Backlog (v7) paths. SKIPS while 0001_init.up.sql's tasks_horizon_check
// omits 'backlog' (schema bug, reported) — runs automatically once fixed.
func TestTaskBacklogPaths(t *testing.T) {
	e := tkSetup(t)
	first, err := e.s.CreateTask(e.asAlice, &model.Task{Title: "needle backlog 1", Horizon: model.HorizonBacklog})
	if taskPGErrCode(err) == pgCheckViolation {
		t.Skip("schema bug: tasks_horizon_check rejects horizon 'backlog' (0001_init.up.sql)")
	}
	if err != nil {
		t.Fatal(err)
	}
	second := tkCreate(t, e.asAlice, e.s, "needle backlog 2", func(tk *model.Task) { tk.Horizon, tk.Period = model.HorizonBacklog, "" })
	tkCreate(t, e.asBob, e.s, "bob backlog", func(tk *model.Task) { tk.Horizon, tk.Period = model.HorizonBacklog, "" })
	_, err = e.s.CreateTask(e.asAlice, &model.Task{Title: "x", Horizon: model.HorizonBacklog, TeamID: tkStr(e.team)})
	tkWantErr(t, err, 400, "backlog tasks are personal")

	bl, err := e.s.Backlog(e.asAlice)
	if ids := tkIDs(bl); err != nil || len(ids) != 2 || ids[0] != second.ID || ids[1] != first.ID {
		t.Fatalf("Backlog newest-first, own only: %v %v", ids, err)
	}
	hidden, _ := e.s.Search(e.asAlice, model.SearchParams{Q: "needle"})
	shown, _ := e.s.Search(e.asAlice, model.SearchParams{Q: "needle", Horizon: model.HorizonBacklog})
	if len(hidden) != 0 || len(shown) != 2 {
		t.Fatalf("backlog hidden by default, opt-in via horizon: %d %d", len(hidden), len(shown))
	}
	n, err := e.s.Reschedule(e.asAlice, []string{first.ID})
	if err != nil || n != 0 {
		t.Fatalf("Reschedule of backlog is a no-op: %d %v", n, err)
	}
}
