package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestBacklogCreateValidation covers docs/DESIGN_V7_BACKLOG.md's create-time
// invariants: horizon "backlog" requires an empty period, no teamId, no
// dueDate, no recurrence. Creation itself is deliberately NOT role-gated —
// a plain USER can create one (it's just an invisible personal task).
func TestBacklogCreateValidation(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Backlog Validation Team")
	user := directMember(t, store, Member{
		Name: "Backlog User", Email: "backlog-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		// Membership in team matters only for the "teamId is 400" subtest
		// below: without it, CreateTask's own-team-membership gate would
		// 403 before validateTaskFields ever runs the backlog invariant,
		// masking the check this test wants to exercise.
		TeamIDs: []bson.ObjectID{team.ID},
	})
	c := newJSONClient(srv)
	loginAs(t, c, "backlog-user@example.com", "userpass1")

	t.Run("ok: title/priority only, empty period", func(t *testing.T) {
		resp, err := c.do("POST", "/api/tasks", `{"title":"plan Q3 roadmap","priority":"high","horizon":"backlog","period":""}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		v := decodeJSON[TaskView](t, resp.Body)
		if v.Horizon != HorizonBacklog {
			t.Fatalf("horizon = %q, want %q", v.Horizon, HorizonBacklog)
		}
		if v.Period != "" {
			t.Fatalf("period = %q, want empty", v.Period)
		}
		if v.OwnerID == nil || *v.OwnerID != user.ID {
			t.Fatalf("ownerId = %v, want creator %s", v.OwnerID, user.ID.Hex())
		}
		if v.WeekOf != "" {
			t.Fatalf("weekOf = %q, want empty on a backlog task", v.WeekOf)
		}
	})

	t.Run("non-empty period is 400", func(t *testing.T) {
		resp, err := c.do("POST", "/api/tasks", `{"title":"x","horizon":"backlog","period":"2026-07-28"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("dueDate is 400", func(t *testing.T) {
		resp, err := c.do("POST", "/api/tasks", `{"title":"x","horizon":"backlog","period":"","dueDate":"2026-08-01"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("recurrence is 400", func(t *testing.T) {
		resp, err := c.do("POST", "/api/tasks", `{"title":"x","horizon":"backlog","period":"","recurrence":{"freq":"daily"}}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("teamId is 400", func(t *testing.T) {
		resp, err := c.do("POST", "/api/tasks", fmt.Sprintf(`{"title":"x","horizon":"backlog","period":"","teamId":%q}`, team.ID.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

// TestBacklogPatchValidation covers the same docs/DESIGN_V7_BACKLOG.md
// invariants as TestBacklogCreateValidation above, but on the PATCH path:
// validateTaskFields's backlog checks are final-state (cover create AND
// patch), yet only the create path had direct coverage until now. Uses an
// ADMIN caller throughout so the third subtest's teamId patch clears the
// team/personal flip's admin gate (PatchTask) before reaching the backlog
// invariant this test actually wants to exercise.
func TestBacklogPatchValidation(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Backlog Patch Team")
	directMember(t, store, Member{
		Name: "Backlog Patch Admin", Email: "backlog-patch-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "backlog-patch-admin@example.com", "adminpass1")

	t.Run("daily task with dueDate patched to backlog is 400", func(t *testing.T) {
		today := currentPeriod(HorizonDaily)
		createResp, err := c.do("POST", "/api/tasks", fmt.Sprintf(`{"title":"has a due date","horizon":"daily","period":%q,"dueDate":%q}`, today, today))
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		resp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), `{"horizon":"backlog","period":""}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("backlog task patched with dueDate is 400", func(t *testing.T) {
		createResp, err := c.do("POST", "/api/tasks", `{"title":"parked","horizon":"backlog","period":""}`)
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		resp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), `{"dueDate":"2026-08-01"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	// PATCHing teamId alone (horizon stays "backlog") is the "backlog tasks
	// are personal" branch of validateTaskFields, not the earlier
	// team-membership/team-not-found checks — a valid team confirms that.
	t.Run("backlog task patched with teamId alone is 400", func(t *testing.T) {
		createResp, err := c.do("POST", "/api/tasks", `{"title":"parked2","horizon":"backlog","period":""}`)
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		resp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), fmt.Sprintf(`{"teamId":%q}`, team.ID.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

// TestBacklogEndpoint covers GET /api/backlog's role gate (ADMIN only) and
// per-admin isolation (each admin sees only their own backlog, sorted
// createdAt desc).
func TestBacklogEndpoint(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	adminA := directMember(t, store, Member{
		Name: "Backlog Admin A", Email: "backlog-admin-a@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	directMember(t, store, Member{
		Name: "Backlog Admin B", Email: "backlog-admin-b@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	directMember(t, store, Member{
		Name: "Backlog Plain User", Email: "backlog-plain-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
	})

	cAdminA := newJSONClient(srv)
	loginAs(t, cAdminA, "backlog-admin-a@example.com", "adminpass1")
	cAdminB := newJSONClient(srv)
	loginAs(t, cAdminB, "backlog-admin-b@example.com", "adminpass1")
	cUser := newJSONClient(srv)
	loginAs(t, cUser, "backlog-plain-user@example.com", "userpass1")

	// Two backlog items for adminA, with controlled createdAt so we can
	// assert the createdAt-desc sort; one for adminB, to assert isolation.
	directTask(t, store, Task{
		Title: "adminA older", Horizon: HorizonBacklog, Period: "",
		OwnerID: &adminA.ID, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	directTask(t, store, Task{
		Title: "adminA newer", Horizon: HorizonBacklog, Period: "",
		OwnerID: &adminA.ID, CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	})
	adminBTaskResp, err := cAdminB.do("POST", "/api/tasks", `{"title":"adminB own","horizon":"backlog","period":""}`)
	if err != nil {
		t.Fatal(err)
	}
	defer adminBTaskResp.Body.Close()

	type backlogBody struct {
		Tasks []TaskView `json:"tasks"`
	}

	t.Run("USER is forbidden", func(t *testing.T) {
		resp, err := cUser.do("GET", "/api/backlog", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("ADMIN sees only own backlog, newest first", func(t *testing.T) {
		resp, err := cAdminA.do("GET", "/api/backlog", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[backlogBody](t, resp.Body)
		if len(body.Tasks) != 2 {
			t.Fatalf("len(tasks) = %d, want 2: %+v", len(body.Tasks), body.Tasks)
		}
		if body.Tasks[0].Title != "adminA newer" || body.Tasks[1].Title != "adminA older" {
			t.Fatalf("unexpected order: %q, %q", body.Tasks[0].Title, body.Tasks[1].Title)
		}
		for _, tv := range body.Tasks {
			if tv.Title == "adminB own" {
				t.Fatalf("adminB's backlog task leaked into adminA's list")
			}
		}
	})
}

// TestBacklogAssignmentFlipAndUndo covers the personal<->team flip that
// staffs a backlog task (docs/DESIGN_V7_BACKLOG.md): assignment clears
// ownerId, stamps the current week, and the task becomes visible on the
// team board; undoing it (a second admin, since the task is now a team task
// visible to any admin) sends it back to backlog with ownerId = the
// patcher, not the original creator.
func TestBacklogAssignmentFlipAndUndo(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Assign Team")
	adminA := directMember(t, store, Member{
		Name: "Assign Admin A", Email: "assign-admin-a@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	adminB := directMember(t, store, Member{
		Name: "Assign Admin B", Email: "assign-admin-b@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	teamMember := directMember(t, store, Member{
		Name: "Assign Member", Email: "assign-member@example.com",
		TeamIDs: []bson.ObjectID{team.ID},
	})
	// Belongs to no team — used below to assert the assign PATCH still
	// enforces assignee-belongs-to-team even through the backlog flip.
	outsider := directMember(t, store, Member{
		Name: "Assign Outsider", Email: "assign-outsider@example.com",
	})

	cAdminA := newJSONClient(srv)
	loginAs(t, cAdminA, "assign-admin-a@example.com", "adminpass1")
	cAdminB := newJSONClient(srv)
	loginAs(t, cAdminB, "assign-admin-b@example.com", "adminpass1")

	createResp, err := cAdminA.do("POST", "/api/tasks", `{"title":"staff me","horizon":"backlog","period":""}`)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	created := decodeJSON[TaskView](t, createResp.Body)

	today := currentPeriod(HorizonDaily)
	W := currentPeriod(HorizonWeekly)

	// assigneeId not belonging to the target team is still a 400, even
	// through the backlog->team flip (docs/DESIGN_V7_BACKLOG.md's assignment
	// PATCH is the existing personal->team flip, no new validation path).
	badAssignBody := fmt.Sprintf(`{"teamId":%q,"horizon":"daily","period":%q,"assigneeId":%q}`,
		team.ID.Hex(), today, outsider.ID.Hex())
	badAssignResp, err := cAdminA.do("PATCH", "/api/tasks/"+created.ID.Hex(), badAssignBody)
	if err != nil {
		t.Fatal(err)
	}
	defer badAssignResp.Body.Close()
	if badAssignResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("assign with non-team assignee status = %d, want 400", badAssignResp.StatusCode)
	}

	assignBody := fmt.Sprintf(`{"teamId":%q,"horizon":"daily","period":%q,"assigneeId":%q}`,
		team.ID.Hex(), today, teamMember.ID.Hex())
	assignResp, err := cAdminA.do("PATCH", "/api/tasks/"+created.ID.Hex(), assignBody)
	if err != nil {
		t.Fatal(err)
	}
	defer assignResp.Body.Close()
	if assignResp.StatusCode != http.StatusOK {
		t.Fatalf("assign status = %d, want 200", assignResp.StatusCode)
	}
	assigned := decodeJSON[TaskView](t, assignResp.Body)
	if assigned.OwnerID != nil {
		t.Fatalf("ownerId = %v after assignment, want nil", assigned.OwnerID)
	}
	if assigned.WeekOf != W {
		t.Fatalf("weekOf = %q after assignment, want current week %q", assigned.WeekOf, W)
	}
	if assigned.Horizon != HorizonDaily || assigned.Period != today {
		t.Fatalf("horizon/period = %q/%q, want daily/%q", assigned.Horizon, assigned.Period, today)
	}

	boardResp, err := cAdminA.do("GET", "/api/teams/"+team.ID.Hex()+"/board", "")
	if err != nil {
		t.Fatal(err)
	}
	defer boardResp.Body.Close()
	board := decodeJSON[Board](t, boardResp.Body)
	found := false
	for _, bmt := range board.Members {
		if bmt.Member.ID != teamMember.ID {
			continue
		}
		for _, tv := range bmt.Tasks {
			if tv.Title == "staff me" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("assigned task did not appear on the team board under its assignee")
	}

	const undoBody = `{"teamId":null,"assigneeId":null,"horizon":"backlog","period":"","dueDate":""}`
	undoResp, err := cAdminB.do("PATCH", "/api/tasks/"+created.ID.Hex(), undoBody)
	if err != nil {
		t.Fatal(err)
	}
	defer undoResp.Body.Close()
	if undoResp.StatusCode != http.StatusOK {
		t.Fatalf("undo status = %d, want 200", undoResp.StatusCode)
	}
	undone := decodeJSON[TaskView](t, undoResp.Body)
	if undone.Horizon != HorizonBacklog || undone.Period != "" {
		t.Fatalf("horizon/period after undo = %q/%q, want backlog/empty", undone.Horizon, undone.Period)
	}
	if undone.TeamID != nil {
		t.Fatalf("teamId after undo = %v, want nil", undone.TeamID)
	}
	if undone.OwnerID == nil || *undone.OwnerID != adminB.ID {
		t.Fatalf("ownerId after undo = %v, want patcher (adminB) %s", undone.OwnerID, adminB.ID.Hex())
	}
	if undone.OwnerID != nil && *undone.OwnerID == adminA.ID {
		t.Fatalf("ownerId after undo reverted to the original creator, want the patcher")
	}
}

// TestBacklogSearchExclusion covers docs/DESIGN_V7_BACKLOG.md's Search
// opt-in: backlog tasks are absent from a default (no horizon param) search,
// and present only when horizon=backlog is passed explicitly.
func TestBacklogSearchExclusion(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Search Admin", Email: "search-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "search-admin@example.com", "adminpass1")

	createResp, err := c.do("POST", "/api/tasks", `{"title":"hidden from search","horizon":"backlog","period":""}`)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", createResp.StatusCode)
	}

	type searchBody struct {
		Tasks []TaskView `json:"tasks"`
	}

	t.Run("default search excludes backlog", func(t *testing.T) {
		resp, err := c.do("GET", "/api/search?status=open", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body := decodeJSON[searchBody](t, resp.Body)
		for _, tv := range body.Tasks {
			if tv.Title == "hidden from search" {
				t.Fatalf("backlog task leaked into default search results")
			}
		}
	})

	t.Run("horizon=backlog opts in", func(t *testing.T) {
		resp, err := c.do("GET", "/api/search?status=open&horizon=backlog", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body := decodeJSON[searchBody](t, resp.Body)
		found := false
		for _, tv := range body.Tasks {
			if tv.Title == "hidden from search" {
				found = true
			}
		}
		if !found {
			t.Fatalf("backlog task missing from horizon=backlog search results")
		}
	})
}

// TestViewMonthDailyRollup covers ViewMonth's daily-task rollup
// (docs/DESIGN_V7_BACKLOG.md): a daily task lands in its week's bucket; for
// a month-spanning ISO week (2026-W31 = Jul 27 - Aug 2), a daily task
// appears ONLY in the month its own date belongs to, even though the week
// bucket itself shows up in both months' weeksInMonth; a weekly task's
// bucket membership is an unaffected regression guard.
func TestViewMonthDailyRollup(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Month Admin", Email: "month-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "month-admin@example.com", "adminpass1")

	mustCreate := func(body string) TaskView {
		t.Helper()
		resp, err := c.do("POST", "/api/tasks", body)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201: %s", resp.StatusCode, body)
		}
		return decodeJSON[TaskView](t, resp.Body)
	}

	// 2026-W29 (Jul 13-19) sits entirely inside July: regression guard for
	// the weekly bucket, plus a same-week daily task.
	mustCreate(`{"title":"weekly in W29","horizon":"weekly","period":"2026-W29"}`)
	mustCreate(`{"title":"daily mid-July","horizon":"daily","period":"2026-07-15"}`)

	// 2026-W31 (Jul 27 - Aug 2) spans the July/August boundary.
	mustCreate(`{"title":"daily July side","horizon":"daily","period":"2026-07-31"}`)
	mustCreate(`{"title":"daily August side","horizon":"daily","period":"2026-08-01"}`)

	type monthBody struct {
		Tasks []TaskView            `json:"tasks"`
		Weeks map[string][]TaskView `json:"weeks"`
	}
	titles := func(tasks []TaskView) map[string]bool {
		out := map[string]bool{}
		for _, tv := range tasks {
			out[tv.Title] = true
		}
		return out
	}

	t.Run("July view", func(t *testing.T) {
		resp, err := c.do("GET", "/api/views/month?month=2026-07", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[monthBody](t, resp.Body)

		w29 := titles(body.Weeks["2026-W29"])
		if !w29["weekly in W29"] {
			t.Fatalf("weekly task missing from its own week bucket (regression)")
		}
		if !w29["daily mid-July"] {
			t.Fatalf("daily task missing from its week bucket in July")
		}

		w31 := titles(body.Weeks["2026-W31"])
		if !w31["daily July side"] {
			t.Fatalf("July-side daily task missing from July's W31 bucket")
		}
		if w31["daily August side"] {
			t.Fatalf("August-side daily task leaked into July's W31 bucket")
		}
	})

	t.Run("August view", func(t *testing.T) {
		resp, err := c.do("GET", "/api/views/month?month=2026-08", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[monthBody](t, resp.Body)

		w31 := titles(body.Weeks["2026-W31"])
		if !w31["daily August side"] {
			t.Fatalf("August-side daily task missing from August's W31 bucket")
		}
		if w31["daily July side"] {
			t.Fatalf("July-side daily task leaked into August's W31 bucket")
		}
	})
}
