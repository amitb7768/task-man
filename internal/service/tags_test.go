package service

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"taskman/internal/model"
)

// tgTags sets tags on a tkCreate mutation.
func tgTags(tags ...string) func(*model.Task) {
	return func(tk *model.Task) { tk.Tags = tags }
}

// tgStored reads tasks.tags straight from the row.
func tgStored(t *testing.T, e *tkEnv, id string) model.Tags {
	t.Helper()
	var raw string
	if err := e.s.db.Raw("SELECT tags::text FROM tasks WHERE id = ?", id).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	var got model.Tags
	if err := got.Scan(raw); err != nil {
		t.Fatalf("stored tags %q: %v", raw, err)
	}
	return got
}

func tgWantTags(t *testing.T, label string, got model.Tags, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got == nil || !reflect.DeepEqual([]string(got), want) {
		t.Fatalf("%s: tags = %#v, want %#v", label, got, want)
	}
}

// tgSet asserts ids is exactly want (order-insensitive).
func tgSet(t *testing.T, label string, vs []model.TaskView, want ...string) {
	t.Helper()
	got := tkIDs(vs)
	slices.Sort(got)
	w := slices.Clone(want)
	slices.Sort(w)
	if !slices.Equal(got, w) {
		t.Fatalf("%s: got %v, want %v", label, got, w)
	}
}

func TestTagsCreatePatchRestore(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "backend", "ui", "ops", "x", "y", "a", "b", "one", "two", "three", "four", "p", "q", "r")

	v := tkCreate(t, e.asAlice, e.s, "tagged", tgTags("  Backend", "backend", "UI", ""))
	tgWantTags(t, "create response", v.Tags, "backend", "ui")
	tgWantTags(t, "create stored", tgStored(t, e, v.ID), "backend", "ui")

	plain := tkCreate(t, e.asAlice, e.s, "plain", nil)
	tgWantTags(t, "nil tags response", plain.Tags)
	var raw string
	e.s.db.Raw("SELECT tags::text FROM tasks WHERE id = ?", plain.ID).Scan(&raw)
	if raw != "[]" {
		t.Fatalf("nil tags must persist as '[]', got %q", raw)
	}

	_, err := e.s.CreateTask(e.asAlice, &model.Task{Title: "bad", Horizon: model.HorizonDaily, Period: tkToday(), Tags: []string{"a b"}})
	tkWantErr(t, err, 400, `invalid tag "a b"`)

	// PATCH: absent = unchanged.
	p := tkPatch(t, e.asAlice, e.s, v.ID, `{"title":"renamed"}`)
	tgWantTags(t, "absent", p.Tags, "backend", "ui")
	tgWantTags(t, "absent stored", tgStored(t, e, v.ID), "backend", "ui")
	// array = replace-all (normalised).
	p = tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":["Ops","ops","x"]}`)
	tgWantTags(t, "replace", p.Tags, "ops", "x")
	tgWantTags(t, "replace stored", tgStored(t, e, v.ID), "ops", "x")
	// invalid → 400, row unchanged.
	_, err = e.s.PatchTask(e.asAlice, v.ID, []byte(`{"tags":["ok","no,pe"]}`))
	tkWantErr(t, err, 400, `invalid tag "no,pe"`)
	tgWantTags(t, "after rejected patch", tgStored(t, e, v.ID), "ops", "x")
	// null and [] both clear.
	p = tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":null}`)
	tgWantTags(t, "null", p.Tags)
	tgWantTags(t, "null stored", tgStored(t, e, v.ID))
	tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":["a","b"]}`)
	p = tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":[]}`)
	tgWantTags(t, "[]", p.Tags)
	tgWantTags(t, "[] stored", tgStored(t, e, v.ID))

	// Aliasing guard: a tags patch decoded over a task that already had
	// tags, combined with a status change (FSM write path) — the row, the
	// response and the status log all reflect the new values, and a
	// shorter replacement list leaves no tail of the old one behind.
	tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":["one","two","three"]}`)
	p = tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":["Four"],"status":"done"}`)
	tgWantTags(t, "fsm response", p.Tags, "four")
	tgWantTags(t, "fsm stored", tgStored(t, e, v.ID), "four")
	if p.Status != model.StatusDone || len(p.Activity) != 1 || p.Activity[0].To != model.StatusDone {
		t.Fatalf("fsm path: status=%s activity=%+v", p.Status, p.Activity)
	}
	// And the same-status path, longer list over shorter.
	p = tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":["p","q","r"]}`)
	tgWantTags(t, "grow", p.Tags, "p", "q", "r")
	tgWantTags(t, "grow stored", tgStored(t, e, v.ID), "p", "q", "r")

	// Restore normalises a hand-crafted payload, and rejects invalid tags.
	now := plain.CreatedAt
	n, err := e.s.RestoreTasks(e.asAdmin, []model.Task{{ID: NewID(), Title: "r", Horizon: model.HorizonDaily,
		Period: tkToday(), Status: model.StatusTodo, OwnerID: tkStr(e.alice.ID), CreatedAt: now, UpdatedAt: now,
		Tags: model.Tags{" X", "x", "Y"}}})
	if err != nil || n != 1 {
		t.Fatalf("restore: n=%d err=%v", n, err)
	}
	var rid string
	e.s.db.Raw("SELECT id FROM tasks WHERE title = 'r'").Scan(&rid)
	tgWantTags(t, "restore stored", tgStored(t, e, rid), "x", "y")
	badID := NewID()
	_, err = e.s.RestoreTasks(e.asAdmin, []model.Task{{ID: badID, Title: "r2", Horizon: model.HorizonDaily,
		Period: tkToday(), Status: model.StatusTodo, OwnerID: tkStr(e.alice.ID), CreatedAt: now, UpdatedAt: now,
		Tags: model.Tags{"#bad"}}})
	tkWantErr(t, err, 400, `restore: task `+badID+`: invalid tag "#bad"`)
	var cnt int64
	e.s.db.Raw("SELECT count(*) FROM tasks WHERE id = ?", badID).Scan(&cnt)
	if cnt != 0 {
		t.Fatal("rejected restore must write nothing")
	}
}

func TestTagsPersonalViewFilters(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "a", "b")
	a := tkCreate(t, e.asAlice, e.s, "A", tgTags("a")).ID
	ab := tkCreate(t, e.asAlice, e.s, "AB", tgTags("b", "a")).ID
	b := tkCreate(t, e.asAlice, e.s, "B", tgTags("b")).ID
	none := tkCreate(t, e.asAlice, e.s, "none", nil).ID
	wa := tkCreate(t, e.asAlice, e.s, "Wa", func(tk *model.Task) {
		tk.Horizon, tk.Period, tk.Tags = model.HorizonWeekly, tkThisWeek(), []string{"a"}
	}).ID
	wb := tkCreate(t, e.asAlice, e.s, "Wb", func(tk *model.Task) {
		tk.Horizon, tk.Period, tk.Tags = model.HorizonWeekly, tkThisWeek(), []string{"b"}
	}).ID
	ma := tkCreate(t, e.asAlice, e.s, "Ma", func(tk *model.Task) {
		tk.Horizon, tk.Period, tk.Tags = model.HorizonMonthly, model.CurrentPeriod(model.HorizonMonthly), []string{"a"}
	}).ID
	mn := tkCreate(t, e.asAlice, e.s, "Mn", func(tk *model.Task) {
		tk.Horizon, tk.Period = model.HorizonMonthly, model.CurrentPeriod(model.HorizonMonthly)
	}).ID
	bla := tkCreate(t, e.asAlice, e.s, "BLa", func(tk *model.Task) {
		tk.Horizon, tk.Period, tk.Tags = model.HorizonBacklog, "", []string{"a"}
	}).ID
	blab := tkCreate(t, e.asAlice, e.s, "BLab", func(tk *model.Task) {
		tk.Horizon, tk.Period, tk.Tags = model.HorizonBacklog, "", []string{"a", "b"}
	}).ID
	bln := tkCreate(t, e.asAlice, e.s, "BLn", func(tk *model.Task) { tk.Horizon, tk.Period = model.HorizonBacklog, "" }).ID
	// Overdue (attention) tasks.
	oa := tkCreate(t, e.asAlice, e.s, "Oa", func(tk *model.Task) {
		tk.Period, tk.DueDate, tk.Tags = tkDaysAgo(3), model.NullStr(tkDaysAgo(1)), []string{"a", "b"}
	}).ID
	on := tkCreate(t, e.asAlice, e.s, "On", func(tk *model.Task) {
		tk.Period, tk.DueDate = tkDaysAgo(3), model.NullStr(tkDaysAgo(1))
	}).ID
	sa := tkCreate(t, e.asAlice, e.s, "Sa", func(tk *model.Task) { tk.Period, tk.Tags = tkDaysAgo(2), []string{"a"} }).ID
	sn := tkCreate(t, e.asAlice, e.s, "Sn", func(tk *model.Task) { tk.Period = tkDaysAgo(2) }).ID

	// Unfiltered = today's behaviour.
	day, wctx, err := e.s.ViewDay(e.asAlice, tkToday(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "day unfiltered", day, a, ab, b, none)
	tgSet(t, "weekctx unfiltered", wctx, wa, wb)

	day, wctx, _ = e.s.ViewDay(e.asAlice, tkToday(), []string{"a"})
	tgSet(t, "day a", day, a, ab)
	tgSet(t, "weekctx a", wctx, wa)
	day, wctx, _ = e.s.ViewDay(e.asAlice, tkToday(), []string{"a", "b"})
	tgSet(t, "day a+b", day, ab)
	tgSet(t, "weekctx a+b", wctx)

	wk, days, mctx, err := e.s.ViewWeek(e.asAlice, tkThisWeek(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "week a", wk, wa)
	tgSet(t, "week day bucket a", days[tkToday()], a, ab)
	tgSet(t, "monthctx a", mctx, ma)
	wk, days, mctx, _ = e.s.ViewWeek(e.asAlice, tkThisWeek(), []string{"b", "a"})
	tgSet(t, "week a+b", wk)
	tgSet(t, "week day bucket a+b", days[tkToday()], ab)
	tgSet(t, "monthctx a+b", mctx)
	_, days, mctx, _ = e.s.ViewWeek(e.asAlice, tkThisWeek(), nil)
	tgSet(t, "week day bucket unfiltered", days[tkToday()], a, ab, b, none)
	tgSet(t, "monthctx unfiltered", mctx, ma, mn)

	mo, weeks, err := e.s.ViewMonth(e.asAlice, model.CurrentPeriod(model.HorizonMonthly), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "month a", mo, ma)
	wbucket := tkIDs(weeks[tkThisWeek()])
	for _, id := range []string{a, ab, wa} {
		if !tkHas(wbucket, id) {
			t.Fatalf("month week bucket a: missing %s in %v", id, wbucket)
		}
	}
	for _, id := range []string{b, none, wb} {
		if tkHas(wbucket, id) {
			t.Fatalf("month week bucket a: unexpected %s", id)
		}
	}
	_, weeks, _ = e.s.ViewMonth(e.asAlice, model.CurrentPeriod(model.HorizonMonthly), []string{"a", "b"})
	wbucket = tkIDs(weeks[tkThisWeek()])
	if !tkHas(wbucket, ab) {
		t.Fatalf("month week bucket a+b: missing %s in %v", ab, wbucket)
	}
	for _, id := range []string{a, b, none, wa, wb} {
		if tkHas(wbucket, id) {
			t.Fatalf("month week bucket a+b: unexpected %s", id)
		}
	}

	bl, err := e.s.Backlog(e.asAlice, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "backlog a", bl, bla, blab)
	bl, _ = e.s.Backlog(e.asAlice, []string{"a", "b"})
	tgSet(t, "backlog a+b", bl, blab)
	bl, _ = e.s.Backlog(e.asAlice, nil)
	tgSet(t, "backlog unfiltered", bl, bla, blab, bln)

	srch, err := e.s.Search(e.asAlice, model.SearchParams{Tags: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "search a", srch, a, ab, wa, ma, oa, sa)
	srch, _ = e.s.Search(e.asAlice, model.SearchParams{Tags: []string{"a", "b"}})
	tgSet(t, "search a+b", srch, ab, oa)
	srch, _ = e.s.Search(e.asAlice, model.SearchParams{Tags: []string{"a", "b"}, Horizon: model.HorizonBacklog})
	tgSet(t, "search a+b backlog", srch, blab)

	over, slip, err := e.s.ViewAttention(e.asAlice, nil)
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "overdue unfiltered", over, oa, on)
	tgSet(t, "slipped unfiltered", slip, sa, sn)
	over, slip, _ = e.s.ViewAttention(e.asAlice, []string{"a"})
	tgSet(t, "overdue a", over, oa)
	tgSet(t, "slipped a", slip, sa)
	over, slip, _ = e.s.ViewAttention(e.asAlice, []string{"a", "b"})
	tgSet(t, "overdue a+b", over, oa)
	tgSet(t, "slipped a+b", slip)
}

func TestTagsTeamFilters(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "a", "b")
	mk := func(title string, tags ...string) string {
		return tkCreate(t, e.asAdmin, e.s, title, func(tk *model.Task) {
			tk.TeamID, tk.Tags = tkStr(e.team), tags
		}).ID
	}
	ta := mk("Ta", "a")
	tab := mk("Tab", "a", "b")
	tb := mk("Tb", "b")
	stale := mk("stale") // open, undated, old weekOf → staleOpen, never on the board
	tkExec(t, e.s, "UPDATE tasks SET week_of = ? WHERE id = ?", tkOldWeek, stale)
	staleA := mk("staleA", "a")
	tkExec(t, e.s, "UPDATE tasks SET week_of = ? WHERE id = ?", tkOldWeek, staleA)

	board, err := e.s.TeamBoard(e.asAlice, e.team, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "board a", board.Unassigned, ta, tab)
	if board.StaleOpen != 2 {
		t.Fatalf("staleOpen must ignore the tag filter: %d", board.StaleOpen)
	}
	board, _ = e.s.TeamBoard(e.asAlice, e.team, []string{"a", "b"})
	tgSet(t, "board a+b", board.Unassigned, tab)
	if board.StaleOpen != 2 {
		t.Fatalf("staleOpen (a+b): %d", board.StaleOpen)
	}
	board, _ = e.s.TeamBoard(e.asAlice, e.team, nil)
	tgSet(t, "board unfiltered", board.Unassigned, ta, tab, tb)

	// History: terminal + weekOf before this week.
	for _, id := range []string{ta, tab, tb} {
		tkExec(t, e.s, "UPDATE tasks SET status = 'done', week_of = ? WHERE id = ?", tkOldWeek, id)
	}
	hist, _, err := e.s.TeamHistory(e.asAlice, e.team, 0, 0, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	tgSet(t, "history a", hist, ta, tab)
	hist, _, _ = e.s.TeamHistory(e.asAlice, e.team, 0, 0, []string{"a", "b"})
	tgSet(t, "history a+b", hist, tab)
	hist, _, _ = e.s.TeamHistory(e.asAlice, e.team, 0, 0, nil)
	tgSet(t, "history unfiltered", hist, ta, tab, tb)
}

func TestTagsMaterializeInherits(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "ritual", "team")
	head := tkCreate(t, e.asAlice, e.s, "standup", func(tk *model.Task) {
		tk.Period = tkDaysAgo(2)
		tk.Recurrence = &model.Recurrence{Freq: model.FreqDaily}
		tk.Tags = []string{"Ritual", "team"}
	})
	if err := e.s.Materialize(e.noUser); err != nil {
		t.Fatal(err)
	}
	var ids []string
	e.s.db.Raw("SELECT id FROM tasks WHERE series_id = ? AND id <> ?", head.ID, head.ID).Scan(&ids)
	if len(ids) != 2 {
		t.Fatalf("spawned %d instances, want 2", len(ids))
	}
	for _, id := range ids {
		tgWantTags(t, "spawned "+id, tgStored(t, e, id), "ritual", "team")
	}
	day, _, _ := e.s.ViewDay(e.asAlice, tkToday(), []string{"ritual"})
	if len(day) != 1 || day[0].SeriesID == nil || *day[0].SeriesID != head.ID {
		t.Fatalf("today's instance filtered by inherited tag: %v", tkIDs(day))
	}
}

func TestTagsListTags(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "a", "secret", "closed", "wip", "bobonly", "adminonly", "team", "other", "unused")
	tkCreate(t, e.asAlice, e.s, "p1", tgTags("a", "secret"))
	tkCreate(t, e.asAlice, e.s, "p2", tgTags("a"))
	closed := tkCreate(t, e.asAlice, e.s, "p3", tgTags("closed", "a")).ID
	tkPatch(t, e.asAlice, e.s, closed, `{"status":"done"}`)
	canc := tkCreate(t, e.asAlice, e.s, "p4", tgTags("closed")).ID
	tkPatch(t, e.asAlice, e.s, canc, `{"status":"cancelled"}`)
	tkCreate(t, e.asAlice, e.s, "p5", tgTags("wip"))
	tkExec(t, e.s, "UPDATE tasks SET status = 'in_progress' WHERE title = 'p5'")
	tkCreate(t, e.asBob, e.s, "bob private", tgTags("bobonly", "a"))
	tkCreate(t, e.asAdmin, e.s, "admin private", tgTags("adminonly"))
	tkCreate(t, e.asAdmin, e.s, "team1", func(tk *model.Task) { tk.TeamID, tk.Tags = tkStr(e.team), []string{"a", "team"} })
	tkCreate(t, e.asAdmin, e.s, "other1", func(tk *model.Task) { tk.TeamID, tk.Tags = tkStr(e.otherTeam), []string{"other"} })

	want := func(label string, got []model.TagCount, err error, want ...model.TagCount) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if want == nil {
			want = []model.TagCount{}
		}
		if got == nil {
			got = []model.TagCount{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %+v, want %+v", label, got, want)
		}
	}
	// v11: every catalog tag, name asc, count 0 included; counts stay
	// scope-bound. all(...) expands the non-zero counts to the full catalog.
	catalog := []string{"a", "adminonly", "bobonly", "closed", "other", "secret", "team", "unused", "wip"}
	all := func(nz map[string]int) []model.TagCount {
		out := make([]model.TagCount, 0, len(catalog))
		for _, n := range catalog {
			out = append(out, model.TagCount{Tag: n, Count: nz[n]})
		}
		return out
	}

	got, err := e.s.ListTags(e.asAlice, nil, false)
	want("alice", got, err, all(map[string]int{"a": 3, "secret": 1, "team": 1, "wip": 1})...)
	got, err = e.s.ListTags(e.asBob, nil, false)
	want("bob", got, err, all(map[string]int{"a": 2, "bobonly": 1, "team": 1})...)
	got, err = e.s.ListTags(e.asAdmin, nil, false)
	want("admin (no other user's personal counts)", got, err,
		all(map[string]int{"a": 1, "adminonly": 1, "other": 1, "team": 1})...)
	got, err = e.s.ListTags(e.asAlice, tkStr(e.team), false)
	want("alice teamId", got, err, all(map[string]int{"a": 1, "team": 1})...)
	got, err = e.s.ListTags(e.asAlice, tkStr(e.otherTeam), false)
	want("alice foreign team", got, err, all(nil)...)
	got, err = e.s.ListTags(e.asAdmin, tkStr(e.otherTeam), false)
	want("admin other team", got, err, all(map[string]int{"other": 1})...)
	got, err = e.s.ListTags(e.asAdmin, tkStr("nope"), false)
	want("unknown team", got, err, all(nil)...)

	// closed: done + cancelled only — open-only tags (secret, wip) count 0.
	got, err = e.s.ListTags(e.asAlice, nil, true)
	want("alice closed", got, err, all(map[string]int{"closed": 2, "a": 1})...)

	// Empty catalog → empty (non-nil handled by the handler's orEmpty).
	tkExec(t, e.s, "UPDATE tasks SET tags = '[]'::jsonb")
	tkExec(t, e.s, "DELETE FROM tags")
	got, err = e.s.ListTags(e.asAlice, nil, false)
	want("empty catalog", got, err)
}

func TestTagsDeleteRestoreReplay(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "keep", "me", "child")
	root := tkCreate(t, e.asAlice, e.s, "root", tgTags("keep", "me"))
	kid := tkCreate(t, e.asAlice, e.s, "kid", func(tk *model.Task) {
		tk.ParentID, tk.Tags = tkStr(root.ID), []string{"child"}
	})
	deleted, err := e.s.DeleteTaskCascade(e.asAdmin, root.ID)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("delete: n=%d err=%v", len(deleted), err)
	}
	// Replay the undo snapshot through JSON, exactly as the client does.
	raw, _ := json.Marshal(deleted)
	var payload []model.Task
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if n, err := e.s.RestoreTasks(e.asAdmin, payload); err != nil || n != 2 {
		t.Fatalf("restore: n=%d err=%v", n, err)
	}
	tgWantTags(t, "restored root", tgStored(t, e, root.ID), "keep", "me")
	tgWantTags(t, "restored kid", tgStored(t, e, kid.ID), "child")
}
