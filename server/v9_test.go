package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ---- helpers ----

// activityOf refetches a task's detail and returns its activity log.
func activityOf(t *testing.T, c *jsonClient, taskID bson.ObjectID) []ActivityEntry {
	t.Helper()
	resp, err := c.do("GET", "/api/tasks/"+taskID.Hex(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get task status = %d, want 200", resp.StatusCode)
	}
	return decodeJSON[TaskDetail](t, resp.Body).Activity
}

func mustStatus(t *testing.T, c *jsonClient, method, path, body string, want int) *http.Response {
	t.Helper()
	resp, err := c.do(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		defer resp.Body.Close()
		t.Fatalf("%s %s status = %d, want %d", method, path, resp.StatusCode, want)
	}
	return resp
}

// rowOf finds a summary section row by title, or nil.
func rowOf(rows []SummaryTask, title string) *SummaryTask {
	for i := range rows {
		if rows[i].Title == title {
			return &rows[i]
		}
	}
	return nil
}

func titlesOf(rows []SummaryTask) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

func sameTitles(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestNoteAdd covers docs/DESIGN_V9_NOTES_SUMMARY.md's add-note rules: author
// and date are server-derived (backdating allowed), validation rejects empty
// text and malformed dates, the detail read carries the log, and list reads
// project it away.
func TestNoteAdd(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	user := directMember(t, store, Member{
		Name: "Note User", Email: "note-user@example.com",
		PasswordHash: mustHash(t, "userpass1"), SystemRole: RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "note-user@example.com", "userpass1")

	today := currentPeriod(HorizonDaily)
	createResp := mustStatus(t, c, "POST", "/api/tasks",
		fmt.Sprintf(`{"title":"note target","horizon":"daily","period":%q}`, today), http.StatusCreated)
	defer createResp.Body.Close()
	task := decodeJSON[TaskView](t, createResp.Body)
	notesPath := "/api/tasks/" + task.ID.Hex() + "/notes"

	t.Run("defaults to today and denormalizes the author", func(t *testing.T) {
		resp := mustStatus(t, c, "POST", notesPath, `{"text":"  wrote the doc  "}`, http.StatusCreated)
		defer resp.Body.Close()
		e := decodeJSON[ActivityEntry](t, resp.Body)
		if e.Kind != ActivityNote {
			t.Fatalf("kind = %q, want %q", e.Kind, ActivityNote)
		}
		if e.Date != today {
			t.Fatalf("date = %q, want today %q", e.Date, today)
		}
		if e.By == nil || *e.By != user.ID {
			t.Fatalf("by = %v, want %s", e.By, user.ID.Hex())
		}
		if e.ByName != "Note User" {
			t.Fatalf("byName = %q, want %q", e.ByName, "Note User")
		}
		if e.Text != "wrote the doc" {
			t.Fatalf("text = %q, want it trimmed", e.Text)
		}
	})

	t.Run("explicit backdate is honored", func(t *testing.T) {
		resp := mustStatus(t, c, "POST", notesPath, `{"text":"friday note","date":"2026-08-28"}`, http.StatusCreated)
		defer resp.Body.Close()
		if e := decodeJSON[ActivityEntry](t, resp.Body); e.Date != "2026-08-28" {
			t.Fatalf("date = %q, want the backdated 2026-08-28", e.Date)
		}
	})

	t.Run("blank text is 400", func(t *testing.T) {
		mustStatus(t, c, "POST", notesPath, `{"text":"   "}`, http.StatusBadRequest).Body.Close()
	})

	t.Run("malformed date is 400", func(t *testing.T) {
		mustStatus(t, c, "POST", notesPath, `{"text":"x","date":"2026-13-40"}`, http.StatusBadRequest).Body.Close()
	})

	t.Run("malformed body is 400", func(t *testing.T) {
		for _, body := range []string{`not json`, `[]`, `{"text":123}`} {
			mustStatus(t, c, "POST", notesPath, body, http.StatusBadRequest).Body.Close()
		}
	})

	t.Run("detail read carries the log", func(t *testing.T) {
		if got := activityOf(t, c, task.ID); len(got) != 2 {
			t.Fatalf("activity len = %d, want 2", len(got))
		}
	})

	// findViews projects activity away so board/view/search payloads don't
	// carry every task's log.
	t.Run("list reads carry no activity key", func(t *testing.T) {
		resp := mustStatus(t, c, "GET", "/api/views/day?date="+today, "", http.StatusOK)
		defer resp.Body.Close()
		body := decodeJSON[struct {
			Tasks []map[string]json.RawMessage `json:"tasks"`
		}](t, resp.Body)
		if len(body.Tasks) == 0 {
			t.Fatalf("no tasks in day view; fixture is wrong")
		}
		for _, row := range body.Tasks {
			if _, ok := row["activity"]; ok {
				t.Fatalf("day-view row carries an activity key: %v", row)
			}
		}
	})

	// Last: this one adds a third entry, so it must run after the counting
	// subtests above. maxNoteLen is a RUNE cap, not a byte cap.
	t.Run("text length is capped at maxNoteLen runes", func(t *testing.T) {
		body := func(n int) string {
			b, err := json.Marshal(map[string]string{"text": strings.Repeat("é", n)})
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		mustStatus(t, c, "POST", notesPath, body(maxNoteLen), http.StatusCreated).Body.Close()
		mustStatus(t, c, "POST", notesPath, body(maxNoteLen+1), http.StatusBadRequest).Body.Close()
	})
}

// TestNotePermissions covers the edit/delete permission matrix: own entry or
// ADMIN, note entries only, and canAccessTask gating the whole surface (so a
// foreign USER *and* ADMIN are both locked out of someone's personal task).
func TestNotePermissions(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Notes Team")
	directMember(t, store, Member{
		Name: "Member A", Email: "note-a@example.com",
		PasswordHash: mustHash(t, "userpass1"), SystemRole: RoleUser,
		TeamIDs: []bson.ObjectID{team.ID},
	})
	directMember(t, store, Member{
		Name: "Member B", Email: "note-b@example.com",
		PasswordHash: mustHash(t, "userpass2"), SystemRole: RoleUser,
		TeamIDs: []bson.ObjectID{team.ID},
	})
	directMember(t, store, Member{
		Name: "Notes Admin", Email: "note-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"), SystemRole: RoleAdmin,
	})

	cA, cB, cAdmin := newJSONClient(srv), newJSONClient(srv), newJSONClient(srv)
	loginAs(t, cA, "note-a@example.com", "userpass1")
	loginAs(t, cB, "note-b@example.com", "userpass2")
	loginAs(t, cAdmin, "note-admin@example.com", "adminpass1")

	teamResp := mustStatus(t, cA, "POST", "/api/tasks", fmt.Sprintf(
		`{"title":"shared task","horizon":"daily","period":"2026-07-09","teamId":%q}`, team.ID.Hex()), http.StatusCreated)
	defer teamResp.Body.Close()
	teamTask := decodeJSON[TaskView](t, teamResp.Body)
	teamNotes := "/api/tasks/" + teamTask.ID.Hex() + "/notes"

	addResp := mustStatus(t, cA, "POST", teamNotes, `{"text":"A's note"}`, http.StatusCreated)
	defer addResp.Body.Close()
	note := decodeJSON[ActivityEntry](t, addResp.Body)
	notePath := teamNotes + "/" + note.ID.Hex()

	t.Run("teammate cannot edit or delete someone else's note", func(t *testing.T) {
		mustStatus(t, cB, "PUT", notePath, `{"text":"hijacked"}`, http.StatusForbidden).Body.Close()
		mustStatus(t, cB, "DELETE", notePath, "", http.StatusForbidden).Body.Close()
	})

	t.Run("author edits own note and gets editedAt", func(t *testing.T) {
		resp := mustStatus(t, cA, "PUT", notePath, `{"text":"A's edited note"}`, http.StatusOK)
		defer resp.Body.Close()
		e := decodeJSON[ActivityEntry](t, resp.Body)
		if e.EditedAt == nil {
			t.Fatalf("editedAt not set on an edited note")
		}
		if e.Text != "A's edited note" {
			t.Fatalf("text = %q, want the edited text", e.Text)
		}
		// An omitted date must not silently re-date a note.
		if e.Date != note.Date {
			t.Fatalf("date = %q, want it unchanged at %q", e.Date, note.Date)
		}
	})

	t.Run("unknown note id is 404", func(t *testing.T) {
		mustStatus(t, cA, "DELETE", teamNotes+"/"+bson.NewObjectID().Hex(), "", http.StatusNotFound).Body.Close()
		mustStatus(t, cA, "DELETE", teamNotes+"/nonsense", "", http.StatusBadRequest).Body.Close()
	})

	t.Run("status entries are not editable", func(t *testing.T) {
		resp := mustStatus(t, cA, "PATCH", "/api/tasks/"+teamTask.ID.Hex(), `{"status":"in_progress"}`, http.StatusOK)
		defer resp.Body.Close()
		v := decodeJSON[TaskView](t, resp.Body)
		var statusID bson.ObjectID
		for _, e := range v.Activity {
			if e.Kind == ActivityStatus {
				statusID = e.ID
			}
		}
		if statusID.IsZero() {
			t.Fatalf("no status entry appended by PATCH; activity = %+v", v.Activity)
		}
		mustStatus(t, cA, "PUT", teamNotes+"/"+statusID.Hex(), `{"text":"nope"}`, http.StatusBadRequest).Body.Close()
		mustStatus(t, cA, "DELETE", teamNotes+"/"+statusID.Hex(), "", http.StatusBadRequest).Body.Close()
	})

	t.Run("ADMIN deletes any note", func(t *testing.T) {
		mustStatus(t, cAdmin, "DELETE", notePath, "", http.StatusNoContent).Body.Close()
		for _, e := range activityOf(t, cA, teamTask.ID) {
			if e.ID == note.ID {
				t.Fatalf("note survived the ADMIN delete")
			}
		}
	})

	// Personal-task privacy governs the note surface exactly as it governs
	// the task: nobody but the owner gets in, ADMIN included.
	t.Run("personal task is closed to everyone but its owner", func(t *testing.T) {
		resp := mustStatus(t, cA, "POST", "/api/tasks",
			`{"title":"A's private task","horizon":"daily","period":"2026-07-09"}`, http.StatusCreated)
		defer resp.Body.Close()
		personal := decodeJSON[TaskView](t, resp.Body)
		path := "/api/tasks/" + personal.ID.Hex() + "/notes"
		mustStatus(t, cB, "POST", path, `{"text":"peeking"}`, http.StatusForbidden).Body.Close()
		mustStatus(t, cAdmin, "POST", path, `{"text":"peeking"}`, http.StatusForbidden).Body.Close()

		// The owner's own note is not a way in either: canAccessTask gates the
		// whole surface, so PUT/DELETE 403 before the note is even looked up.
		own := mustStatus(t, cA, "POST", path, `{"text":"mine alone"}`, http.StatusCreated)
		defer own.Body.Close()
		ownPath := path + "/" + decodeJSON[ActivityEntry](t, own.Body).ID.Hex()
		for _, other := range []*jsonClient{cB, cAdmin} {
			mustStatus(t, other, "PUT", ownPath, `{"text":"hijacked"}`, http.StatusForbidden).Body.Close()
			mustStatus(t, other, "DELETE", ownPath, "", http.StatusForbidden).Body.Close()
		}
	})

	t.Run("unknown task id is 404 on every note route", func(t *testing.T) {
		gone := "/api/tasks/" + bson.NewObjectID().Hex() + "/notes"
		noteID := "/" + bson.NewObjectID().Hex()
		mustStatus(t, cA, "POST", gone, `{"text":"x"}`, http.StatusNotFound).Body.Close()
		mustStatus(t, cA, "PUT", gone+noteID, `{"text":"x"}`, http.StatusNotFound).Body.Close()
		mustStatus(t, cA, "DELETE", gone+noteID, "", http.StatusNotFound).Body.Close()
	})

	// All four v9 routes sit behind requireAuth.
	t.Run("no session is 401", func(t *testing.T) {
		anon := newJSONClient(srv)
		notePath := teamNotes + "/" + note.ID.Hex()
		mustStatus(t, anon, "POST", teamNotes, `{"text":"x"}`, http.StatusUnauthorized).Body.Close()
		mustStatus(t, anon, "PUT", notePath, `{"text":"x"}`, http.StatusUnauthorized).Body.Close()
		mustStatus(t, anon, "DELETE", notePath, "", http.StatusUnauthorized).Body.Close()
		mustStatus(t, anon, "GET", "/api/summary?from=2026-07-06&to=2026-07-12", "", http.StatusUnauthorized).Body.Close()
	})
}

// TestPatchActivityIsSystemManaged covers the auto-log and the
// never-client-settable rule: a status change appends an entry, any other
// patch appends nothing, and a client-sent "activity" array is discarded
// without corrupting the real log.
func TestPatchActivityIsSystemManaged(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Patch Noter", Email: "patch-noter@example.com",
		PasswordHash: mustHash(t, "userpass1"), SystemRole: RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "patch-noter@example.com", "userpass1")

	createResp := mustStatus(t, c, "POST", "/api/tasks",
		`{"title":"auto log","horizon":"daily","period":"2026-07-09"}`, http.StatusCreated)
	defer createResp.Body.Close()
	task := decodeJSON[TaskView](t, createResp.Body)
	if len(task.Activity) != 0 {
		t.Fatalf("CreateTask seeded activity: %+v", task.Activity)
	}
	path := "/api/tasks/" + task.ID.Hex()

	t.Run("status change appends a status entry", func(t *testing.T) {
		resp := mustStatus(t, c, "PATCH", path, `{"status":"in_progress"}`, http.StatusOK)
		defer resp.Body.Close()
		v := decodeJSON[TaskView](t, resp.Body)
		if len(v.Activity) != 1 {
			t.Fatalf("activity len = %d, want 1: %+v", len(v.Activity), v.Activity)
		}
		e := v.Activity[0]
		if e.Kind != ActivityStatus || e.From != StatusTodo || e.To != StatusInProgress {
			t.Fatalf("entry = %+v, want status todo -> in_progress", e)
		}
		if e.Date != currentPeriod(HorizonDaily) {
			t.Fatalf("date = %q, want today %q", e.Date, currentPeriod(HorizonDaily))
		}
		if e.ByName != "Patch Noter" {
			t.Fatalf("byName = %q, want %q", e.ByName, "Patch Noter")
		}
	})

	t.Run("a non-status patch appends nothing", func(t *testing.T) {
		resp := mustStatus(t, c, "PATCH", path, `{"title":"auto log renamed"}`, http.StatusOK)
		defer resp.Body.Close()
		if v := decodeJSON[TaskView](t, resp.Body); len(v.Activity) != 1 {
			t.Fatalf("activity len = %d, want it unchanged at 1", len(v.Activity))
		}
	})

	// The nil-before-unmarshal / restore-after dance: an empty array must not
	// truncate the log, and a populated one must not overwrite its elements.
	t.Run("client-sent activity is discarded", func(t *testing.T) {
		resp := mustStatus(t, c, "PATCH", path, `{"activity":[]}`, http.StatusOK)
		defer resp.Body.Close()
		if v := decodeJSON[TaskView](t, resp.Body); len(v.Activity) != 1 {
			t.Fatalf("empty activity array truncated the log to %d entries", len(v.Activity))
		}

		resp2 := mustStatus(t, c, "PATCH", path,
			`{"activity":[{"kind":"note","date":"2020-01-01","text":"injected"}]}`, http.StatusOK)
		defer resp2.Body.Close()
		v := decodeJSON[TaskView](t, resp2.Body)
		if len(v.Activity) != 1 {
			t.Fatalf("activity len = %d, want it unchanged at 1", len(v.Activity))
		}
		if v.Activity[0].Kind != ActivityStatus || v.Activity[0].Text == "injected" {
			t.Fatalf("client-sent entry corrupted the log: %+v", v.Activity[0])
		}
	})

	// The auto-log is appended after every validation, so a patch that is
	// rejected downstream of the status merge leaves no trace.
	t.Run("a rejected patch appends no status entry", func(t *testing.T) {
		mustStatus(t, c, "PATCH", path, `{"status":"done","priority":"urgent"}`, http.StatusBadRequest).Body.Close()
		if got := activityOf(t, c, task.ID); len(got) != 1 {
			t.Fatalf("activity len = %d, want it unchanged at 1: %+v", len(got), got)
		}
	})
}

// TestActivityRestoreRoundTrip: the log lives on the task doc, so cascade
// delete carries it out and restore replays it back in.
func TestActivityRestoreRoundTrip(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Restore Admin", Email: "restore-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"), SystemRole: RoleAdmin,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "restore-admin@example.com", "adminpass1")

	createResp := mustStatus(t, c, "POST", "/api/tasks",
		`{"title":"round trip","horizon":"daily","period":"2026-07-09"}`, http.StatusCreated)
	defer createResp.Body.Close()
	task := decodeJSON[TaskView](t, createResp.Body)

	addResp := mustStatus(t, c, "POST", "/api/tasks/"+task.ID.Hex()+"/notes",
		`{"text":"survives a delete","date":"2026-07-09"}`, http.StatusCreated)
	defer addResp.Body.Close()
	note := decodeJSON[ActivityEntry](t, addResp.Body)

	delResp := mustStatus(t, c, "DELETE", "/api/tasks/"+task.ID.Hex(), "", http.StatusOK)
	defer delResp.Body.Close()
	deleted := decodeJSON[struct {
		Deleted []TaskView `json:"deleted"`
	}](t, delResp.Body)
	if len(deleted.Deleted) != 1 || len(deleted.Deleted[0].Activity) != 1 {
		t.Fatalf("deleted docs lost the activity log: %+v", deleted.Deleted)
	}

	body, err := json.Marshal(map[string]any{"tasks": deleted.Deleted})
	if err != nil {
		t.Fatal(err)
	}
	restoreResp := mustStatus(t, c, "POST", "/api/tasks/restore", string(body), http.StatusOK)
	defer restoreResp.Body.Close()
	if n := decodeJSON[map[string]int](t, restoreResp.Body)["restored"]; n != 1 {
		t.Fatalf("restored = %d, want 1", n)
	}

	got := activityOf(t, c, task.ID)
	if len(got) != 1 || got[0].ID != note.ID || got[0].Text != "survives a delete" {
		t.Fatalf("activity did not round-trip: %+v", got)
	}
}

// TestSummaryClassification covers section assignment over one fixture week,
// including first-match-wins, the cancelled closedDate rule, the drops
// (backlog / untouched / closed-before-range), title sorting and overdue.
func TestSummaryClassification(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	user := directMember(t, store, Member{
		Name: "Summary User", Email: "summary-user@example.com",
		PasswordHash: mustHash(t, "userpass1"), SystemRole: RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "summary-user@example.com", "userpass1")

	const from, to = "2026-05-04", "2026-05-10"
	day := func(d, h int) time.Time { return time.Date(2026, 5, d, h, 0, 0, 0, time.Local) }
	before := time.Date(2026, 4, 1, 9, 0, 0, 0, time.Local)
	owner := user.ID
	note := func(date, text string, at time.Time) ActivityEntry {
		return ActivityEntry{ID: bson.NewObjectID(), Kind: ActivityNote, Date: date, At: at,
			By: &owner, ByName: user.Name, Text: text}
	}
	statusEntry := func(date, fromStatus, toStatus string, at time.Time) ActivityEntry {
		return ActivityEntry{ID: bson.NewObjectID(), Kind: ActivityStatus, Date: date, At: at,
			By: &owner, ByName: user.Name, From: fromStatus, To: toStatus}
	}
	doneAt := day(6, 10)

	// A: done in range.
	directTask(t, store, Task{Title: "zulu done", Horizon: HorizonDaily, Period: "2026-05-06",
		Status: StatusDone, OwnerID: &owner, CreatedAt: before, UpdatedAt: doneAt, CompletedAt: &doneAt})
	// B: open, note in range, overdue as of the range end.
	directTask(t, store, Task{Title: "bravo updated", Horizon: HorizonDaily, Period: "2026-05-05",
		Status: StatusTodo, DueDate: "2026-05-01", OwnerID: &owner, CreatedAt: before, UpdatedAt: day(5, 12),
		Activity: []ActivityEntry{note("2026-05-05", "made progress", day(5, 12))}})
	// C: created in range, no activity.
	directTask(t, store, Task{Title: "charlie new", Horizon: HorizonDaily, Period: "2026-05-05",
		Status: StatusTodo, OwnerID: &owner, CreatedAt: day(5, 9), UpdatedAt: day(5, 9)})
	// D: backlog created in range -> never a "new" row.
	directTask(t, store, Task{Title: "delta backlog", Horizon: HorizonBacklog,
		Status: StatusTodo, OwnerID: &owner, CreatedAt: day(5, 9), UpdatedAt: day(5, 9)})
	// E: created before the range and untouched.
	directTask(t, store, Task{Title: "echo untouched", Horizon: HorizonDaily, Period: "2026-04-02",
		Status: StatusTodo, OwnerID: &owner, CreatedAt: before, UpdatedAt: before})
	// F: done before the range.
	directTask(t, store, Task{Title: "foxtrot old done", Horizon: HorizonDaily, Period: "2026-04-02",
		Status: StatusDone, OwnerID: &owner, CreatedAt: before, UpdatedAt: before, CompletedAt: &before})
	// G: cancelled in range — closedDate comes from the status entry.
	directTask(t, store, Task{Title: "alpha cancelled", Horizon: HorizonDaily, Period: "2026-05-04",
		Status: StatusCancelled, OwnerID: &owner, CreatedAt: before, UpdatedAt: day(7, 15),
		Activity: []ActivityEntry{statusEntry("2026-05-07", StatusTodo, StatusCancelled, day(7, 15))}})
	// H: done in range AND noted in range -> completed wins, notes carry both.
	hDone := day(8, 11)
	directTask(t, store, Task{Title: "Mike done", Horizon: HorizonDaily, Period: "2026-05-06",
		Status: StatusDone, OwnerID: &owner, CreatedAt: before, UpdatedAt: hDone, CompletedAt: &hDone,
		Activity: []ActivityEntry{
			note("2026-05-06", "did a thing", day(6, 9)),
			statusEntry("2026-05-08", StatusTodo, StatusDone, hDone),
		}})

	resp := mustStatus(t, c, "GET", fmt.Sprintf("/api/summary?from=%s&to=%s", from, to), "", http.StatusOK)
	defer resp.Body.Close()
	res := decodeJSON[SummaryResult](t, resp.Body)

	if res.From != from || res.To != to {
		t.Fatalf("range echoed back as %s..%s", res.From, res.To)
	}
	// Sorted by lower-cased title: a naive byte sort would put "Mike done" first.
	if got := titlesOf(res.Completed); !sameTitles(got, "alpha cancelled", "Mike done", "zulu done") {
		t.Fatalf("completed = %v", got)
	}
	if got := titlesOf(res.Updated); !sameTitles(got, "bravo updated") {
		t.Fatalf("updated = %v", got)
	}
	if got := titlesOf(res.Added); !sameTitles(got, "charlie new") {
		t.Fatalf("added = %v", got)
	}

	closedBy := map[string]string{}
	for _, r := range res.Completed {
		closedBy[r.Title] = r.ClosedDate
	}
	for title, want := range map[string]string{
		"alpha cancelled": "2026-05-07", // last status->cancelled entry
		"Mike done":       "2026-05-08", // completedAt
		"zulu done":       "2026-05-06",
	} {
		if closedBy[title] != want {
			t.Fatalf("closedDate[%s] = %q, want %q", title, closedBy[title], want)
		}
	}

	for _, r := range res.Completed {
		if r.Title != "Mike done" {
			continue
		}
		if len(r.Notes) != 2 {
			t.Fatalf("Mike done notes = %d, want both the note and the status entry", len(r.Notes))
		}
		if r.Notes[0].Date != "2026-05-06" || r.Notes[1].Date != "2026-05-08" {
			t.Fatalf("notes not sorted by (date, at): %+v", r.Notes)
		}
	}

	if !res.Updated[0].Overdue {
		t.Fatalf("bravo updated should be overdue (due 2026-05-01, still open)")
	}
	if res.Added[0].Overdue {
		t.Fatalf("charlie new has no dueDate and must not be overdue")
	}
	if res.Added[0].Notes == nil {
		t.Fatalf("notes must serialize as [] not null")
	}

	// These two seed extra fixtures, so they run after the whole-section
	// assertions above and match their own rows by title.
	summarize := func(t *testing.T, from, to string) SummaryResult {
		t.Helper()
		resp := mustStatus(t, c, "GET", fmt.Sprintf("/api/summary?from=%s&to=%s", from, to), "", http.StatusOK)
		defer resp.Body.Close()
		return decodeJSON[SummaryResult](t, resp.Body)
	}

	// The updatedAt fallback for a cancelled task holds only while the task has
	// NO activity: every note bumps updatedAt, so one comment on a legacy
	// cancelled task must not re-date the cancel into the current week.
	t.Run("a note moves a legacy cancelled task from completed to updated", func(t *testing.T) {
		legacy := directTask(t, store, Task{Title: "legacy cancelled", Horizon: HorizonDaily, Period: "2026-05-06",
			Status: StatusCancelled, OwnerID: &owner, CreatedAt: before, UpdatedAt: day(6, 14)})

		// While it has no log at all the updatedAt fallback still dates it.
		res := summarize(t, from, to)
		row := rowOf(res.Completed, "legacy cancelled")
		if row == nil {
			t.Fatalf("a log-less cancelled task must fall back to updatedAt; completed = %v", titlesOf(res.Completed))
		}
		if row.ClosedDate != "2026-05-06" {
			t.Fatalf("closedDate = %q, want updatedAt's local date 2026-05-06", row.ClosedDate)
		}

		// One comment today bumps updatedAt to today. The fallback must NOT
		// re-read that as "cancelled today" and drop the task into this week's
		// Completed — it belongs in Updated.
		mustStatus(t, c, "POST", "/api/tasks/"+legacy.ID.Hex()+"/notes",
			`{"text":"still relevant"}`, http.StatusCreated).Body.Close()

		today := currentPeriod(HorizonDaily)
		res = summarize(t, today, today)
		if row := rowOf(res.Completed, "legacy cancelled"); row != nil {
			t.Fatalf("one note teleported the cancel into today's completed (closedDate %q)", row.ClosedDate)
		}
		row = rowOf(res.Updated, "legacy cancelled")
		if row == nil {
			t.Fatalf("expected it in updated; updated = %v", titlesOf(res.Updated))
		}
		if row.ClosedDate != "" {
			t.Fatalf("closedDate = %q, want it empty", row.ClosedDate)
		}
	})

	// overdue is measured against min(to, today), so a range ending in the
	// future must not flag a task that is merely due later.
	t.Run("overdue is measured against min(to, today)", func(t *testing.T) {
		now := time.Now()
		today := currentPeriod(HorizonDaily)
		dateIn := func(days int) string { return now.AddDate(0, 0, days).Format("2006-01-02") }
		directTask(t, store, Task{Title: "due later", Horizon: HorizonDaily, Period: today,
			Status: StatusTodo, DueDate: dateIn(5), OwnerID: &owner, CreatedAt: now, UpdatedAt: now})
		directTask(t, store, Task{Title: "due already", Horizon: HorizonDaily, Period: today,
			Status: StatusTodo, DueDate: dateIn(-3), OwnerID: &owner, CreatedAt: now, UpdatedAt: now})

		res := summarize(t, today, dateIn(10))
		later, already := rowOf(res.Added, "due later"), rowOf(res.Added, "due already")
		if later == nil || already == nil {
			t.Fatalf("added = %v, want both fixtures", titlesOf(res.Added))
		}
		if later.Overdue {
			t.Fatalf("a task due %s must not be overdue just because to is in the future", later.DueDate)
		}
		if !already.Overdue {
			t.Fatalf("a task due %s is overdue as of today", already.DueDate)
		}
	})
}

// TestSummaryScope covers the scope rules: team scope follows the team-board
// membership check, me-scope is own personal + assigned-to-me, and the two
// query-shape 400s.
func TestSummaryScope(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	teamX := directTeam(t, store, "Scope Team X")
	teamY := directTeam(t, store, "Scope Team Y")
	memberA := directMember(t, store, Member{
		Name: "Scope A", Email: "scope-a@example.com",
		PasswordHash: mustHash(t, "userpass1"), SystemRole: RoleUser,
		TeamIDs: []bson.ObjectID{teamX.ID},
	})
	memberB := directMember(t, store, Member{
		Name: "Scope B", Email: "scope-b@example.com",
		PasswordHash: mustHash(t, "userpass2"), SystemRole: RoleUser,
		TeamIDs: []bson.ObjectID{teamX.ID},
	})

	const from, to = "2026-05-04", "2026-05-10"
	made := time.Date(2026, 5, 6, 10, 0, 0, 0, time.Local)
	directTask(t, store, Task{Title: "team task for a", Horizon: HorizonDaily, Period: "2026-05-06",
		Status: StatusTodo, TeamID: &teamX.ID, AssigneeID: &memberA.ID, CreatedAt: made, UpdatedAt: made})
	directTask(t, store, Task{Title: "team task for b", Horizon: HorizonDaily, Period: "2026-05-06",
		Status: StatusTodo, TeamID: &teamX.ID, AssigneeID: &memberB.ID, CreatedAt: made, UpdatedAt: made})
	directTask(t, store, Task{Title: "personal of a", Horizon: HorizonDaily, Period: "2026-05-06",
		Status: StatusTodo, OwnerID: &memberA.ID, CreatedAt: made, UpdatedAt: made})

	c := newJSONClient(srv)
	loginAs(t, c, "scope-a@example.com", "userpass1")
	base := fmt.Sprintf("/api/summary?from=%s&to=%s", from, to)

	get := func(t *testing.T, path string) SummaryResult {
		t.Helper()
		resp := mustStatus(t, c, "GET", path, "", http.StatusOK)
		defer resp.Body.Close()
		return decodeJSON[SummaryResult](t, resp.Body)
	}

	t.Run("team scope covers the whole team", func(t *testing.T) {
		res := get(t, base+"&teamId="+teamX.ID.Hex())
		if got := titlesOf(res.Added); !sameTitles(got, "team task for a", "team task for b") {
			t.Fatalf("added = %v", got)
		}
		if res.TeamName != "Scope Team X" {
			t.Fatalf("teamName = %q", res.TeamName)
		}
		if res.Added[0].AssigneeName != "Scope A" {
			t.Fatalf("assigneeName = %q, want the resolved member name", res.Added[0].AssigneeName)
		}
		if res.Added[0].TeamName != "Scope Team X" {
			t.Fatalf("row teamName = %q", res.Added[0].TeamName)
		}
	})

	t.Run("assignee filter narrows the team", func(t *testing.T) {
		res := get(t, base+"&teamId="+teamX.ID.Hex()+"&assigneeId="+memberA.ID.Hex())
		if got := titlesOf(res.Added); !sameTitles(got, "team task for a") {
			t.Fatalf("added = %v", got)
		}
		if res.AssigneeName != "Scope A" {
			t.Fatalf("top-level assigneeName = %q", res.AssigneeName)
		}
	})

	t.Run("USER on a foreign team is 403", func(t *testing.T) {
		mustStatus(t, c, "GET", base+"&teamId="+teamY.ID.Hex(), "", http.StatusForbidden).Body.Close()
	})

	// Me-scope: own personal plus team tasks assigned to me — never a
	// teammate's task, even in a team I belong to.
	t.Run("me-scope is personal plus assigned-to-me", func(t *testing.T) {
		res := get(t, base)
		if got := titlesOf(res.Added); !sameTitles(got, "personal of a", "team task for a") {
			t.Fatalf("added = %v", got)
		}
		if res.TeamID != nil || res.TeamName != "" {
			t.Fatalf("me-scope must not echo a team: %+v", res)
		}
	})

	t.Run("from after to is 400", func(t *testing.T) {
		mustStatus(t, c, "GET", "/api/summary?from=2026-05-10&to=2026-05-04", "", http.StatusBadRequest).Body.Close()
	})

	t.Run("assigneeId without teamId is 400", func(t *testing.T) {
		mustStatus(t, c, "GET", base+"&assigneeId="+memberA.ID.Hex(), "", http.StatusBadRequest).Body.Close()
	})

	t.Run("missing range is 400", func(t *testing.T) {
		mustStatus(t, c, "GET", "/api/summary", "", http.StatusBadRequest).Body.Close()
	})
}

// TestNoteConcurrentAppend guards the "atomic $push, no read-modify-write"
// rule: notes written genuinely in parallel must all survive.
func TestNoteConcurrentAppend(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Race User", Email: "race-user@example.com",
		PasswordHash: mustHash(t, "userpass1"), SystemRole: RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "race-user@example.com", "userpass1")

	createResp := mustStatus(t, c, "POST", "/api/tasks",
		`{"title":"race","horizon":"daily","period":"2026-07-09"}`, http.StatusCreated)
	defer createResp.Body.Close()
	task := decodeJSON[TaskView](t, createResp.Body)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const adds = 8
	errs := make([]error, adds)
	var wg sync.WaitGroup
	for i := 0; i < adds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = store.AddNote(ctx, task.ID, NoteInput{Text: fmt.Sprintf("note %d", i)})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("AddNote %d: %v", i, err)
		}
	}
	if got := activityOf(t, c, task.ID); len(got) != adds {
		t.Fatalf("activity len = %d, want %d", len(got), adds)
	}
}

// TestPatchNoteRace is the regression for PatchTask's whole-doc ReplaceOne:
// it is a read-modify-write of activity, so a note landing between the
// getTaskRaw and the replace used to be silently erased. The replace is now a
// compare-and-swap on updatedAt with a bounded retry, so every note survives
// however the two writes interleave.
func TestPatchNoteRace(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Patch Race", Email: "patch-race@example.com",
		PasswordHash: mustHash(t, "userpass1"), SystemRole: RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "patch-race@example.com", "userpass1")

	createResp := mustStatus(t, c, "POST", "/api/tasks",
		`{"title":"patch vs note","horizon":"daily","period":"2026-07-09"}`, http.StatusCreated)
	defer createResp.Body.Close()
	task := decodeJSON[TaskView](t, createResp.Body)
	taskPath := "/api/tasks/" + task.ID.Hex()
	notesPath := taskPath + "/notes"

	const rounds = 20
	statuses := [2]string{StatusInProgress, StatusTodo}
	var mu sync.Mutex
	var failures []string
	fail := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		failures = append(failures, fmt.Sprintf(format, args...))
	}
	// One round = a status PATCH and a note POST fired at the same task at the
	// same time. Alternating the status guarantees every PATCH really is a
	// transition, so every PATCH appends to the same array the POST is pushing to.
	send := func(method, path, body string, want int) {
		resp, err := c.do(method, path, body)
		if err != nil {
			fail("%s %s: %v", method, path, err)
			return
		}
		code := resp.StatusCode
		resp.Body.Close()
		if code != want {
			fail("%s %s status = %d, want %d", method, path, code, want)
		}
	}
	for i := 0; i < rounds; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			send("PATCH", taskPath, fmt.Sprintf(`{"status":%q}`, statuses[i%2]), http.StatusOK)
		}()
		go func() {
			defer wg.Done()
			send("POST", notesPath, fmt.Sprintf(`{"text":"note %d"}`, i), http.StatusCreated)
		}()
		wg.Wait()
	}
	if len(failures) > 0 {
		t.Fatalf("concurrent writes failed:\n%s", strings.Join(failures, "\n"))
	}

	notes := 0
	for _, e := range activityOf(t, c, task.ID) {
		if e.Kind == ActivityNote {
			notes++
		}
	}
	if notes != rounds {
		t.Fatalf("note entries = %d, want %d — a concurrent PATCH clobbered the log", notes, rounds)
	}
}
