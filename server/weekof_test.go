package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestWeekOfOnCreate covers docs/DESIGN_V6_WEEK_ROLLOVER.md's create rule:
// team tasks get weekOf = isoWeek(now); personal tasks never carry weekOf.
func TestWeekOfOnCreate(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Create Team")
	directMember(t, store, Member{
		Name: "Create Admin", Email: "create-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "create-admin@example.com", "adminpass1")

	W := currentPeriod(HorizonWeekly)

	t.Run("team task gets current week", func(t *testing.T) {
		resp, err := c.do("POST", "/api/tasks", fmt.Sprintf(
			`{"title":"team task","horizon":"daily","period":"2026-07-09","teamId":%q}`, team.ID.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		v := decodeJSON[TaskView](t, resp.Body)
		if v.WeekOf != W {
			t.Fatalf("weekOf = %q, want %q", v.WeekOf, W)
		}
	})

	t.Run("personal task has no weekOf", func(t *testing.T) {
		resp, err := c.do("POST", "/api/tasks", `{"title":"personal task","horizon":"daily","period":"2026-07-09"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		v := decodeJSON[TaskView](t, resp.Body)
		if v.WeekOf != "" {
			t.Fatalf("weekOf = %q, want empty on a personal task", v.WeekOf)
		}
	})
}

// TestTerminalTransitionBumpsWeekOf covers the "weekOf is the week a team
// task last mattered" rule: a stale team task freshly finished this week
// jumps back into the current week's completed fold, overriding whatever
// weekOf it carried before.
func TestTerminalTransitionBumpsWeekOf(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Terminal Team")
	directMember(t, store, Member{
		Name: "Terminal Admin", Email: "terminal-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "terminal-admin@example.com", "adminpass1")

	createResp, err := c.do("POST", "/api/tasks", fmt.Sprintf(
		`{"title":"terminal transition","horizon":"daily","period":"2026-07-09","teamId":%q}`, team.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	created := decodeJSON[TaskView](t, createResp.Body)

	W := currentPeriod(HorizonWeekly)

	// Simulate a task that has sat stale since an old week.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := store.tasks.UpdateOne(ctx, bson.M{"_id": created.ID}, bson.M{"$set": bson.M{"weekOf": "2020-W01"}}); err != nil {
		t.Fatalf("seed old weekOf: %v", err)
	}

	patchResp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), `{"status":"done"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", patchResp.StatusCode)
	}
	patched := decodeJSON[TaskView](t, patchResp.Body)
	if patched.WeekOf != W {
		t.Fatalf("weekOf after terminal transition = %q, want current week %q", patched.WeekOf, W)
	}

	// Cancelling from a still-stale weekOf must bump it too (terminal =
	// done OR cancelled), even though cancelled never sets completedAt.
	if _, err := store.tasks.UpdateOne(ctx, bson.M{"_id": created.ID},
		bson.M{
			"$set":   bson.M{"status": StatusTodo, "weekOf": "2020-W02"},
			"$unset": bson.M{"completedAt": ""},
		}); err != nil {
		t.Fatalf("reset to stale open: %v", err)
	}
	cancelResp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), `{"status":"cancelled"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	cancelled := decodeJSON[TaskView](t, cancelResp.Body)
	if cancelled.WeekOf != W {
		t.Fatalf("weekOf after cancel transition = %q, want current week %q", cancelled.WeekOf, W)
	}
	if cancelled.CompletedAt != nil {
		t.Fatalf("cancelled task must not gain completedAt")
	}
}

// TestPatchWeekOf covers the ADMIN-only rollover "move" primitive: ADMIN may
// set weekOf directly on a team task to a valid ISO week key; USER may
// never; malformed values and personal tasks are rejected.
func TestPatchWeekOf(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Patch Team")
	directMember(t, store, Member{
		Name: "Patch Admin", Email: "patch-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	directMember(t, store, Member{
		Name: "Patch User", Email: "patch-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{team.ID},
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "patch-admin@example.com", "adminpass1")
	cUser := newJSONClient(srv)
	loginAs(t, cUser, "patch-user@example.com", "userpass1")

	teamTaskResp, err := cAdmin.do("POST", "/api/tasks", fmt.Sprintf(
		`{"title":"team task","horizon":"daily","period":"2026-07-09","teamId":%q}`, team.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer teamTaskResp.Body.Close()
	teamTask := decodeJSON[TaskView](t, teamTaskResp.Body)

	personalTaskResp, err := cAdmin.do("POST", "/api/tasks", `{"title":"personal task","horizon":"daily","period":"2026-07-09"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer personalTaskResp.Body.Close()
	personalTask := decodeJSON[TaskView](t, personalTaskResp.Body)

	target := "2025-W05"

	t.Run("ADMIN can move weekOf on a team task", func(t *testing.T) {
		resp, err := cAdmin.do("PATCH", "/api/tasks/"+teamTask.ID.Hex(), fmt.Sprintf(`{"weekOf":%q}`, target))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		v := decodeJSON[TaskView](t, resp.Body)
		if v.WeekOf != target {
			t.Fatalf("weekOf = %q, want %q", v.WeekOf, target)
		}
	})

	t.Run("USER is forbidden from patching weekOf, even on own team's task", func(t *testing.T) {
		resp, err := cUser.do("PATCH", "/api/tasks/"+teamTask.ID.Hex(), `{"weekOf":"2025-W06"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	// json.Unmarshal matches struct field names case-insensitively, so a
	// non-canonical key like "weekof" still lands in Task.WeekOf even though
	// a case-sensitive raw-JSON presence check would miss it — that gap let
	// a USER bypass the 403 above (and the ISO-week validation) entirely.
	t.Run("USER is forbidden from patching weekOf via a non-canonical key", func(t *testing.T) {
		resp, err := cUser.do("PATCH", "/api/tasks/"+teamTask.ID.Hex(), `{"weekof":"2025-W06"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var tk Task
		if err := store.tasks.FindOne(ctx, bson.M{"_id": teamTask.ID}).Decode(&tk); err != nil {
			t.Fatalf("fetch task: %v", err)
		}
		if tk.WeekOf == "2025-W06" {
			t.Fatalf("weekOf was mutated to %q via non-canonical key by a USER", tk.WeekOf)
		}
	})

	t.Run("malformed ISO week is 400", func(t *testing.T) {
		resp, err := cAdmin.do("PATCH", "/api/tasks/"+teamTask.ID.Hex(), `{"weekOf":"not-a-week"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("weekOf on a personal task is 400", func(t *testing.T) {
		resp, err := cAdmin.do("PATCH", "/api/tasks/"+personalTask.ID.Hex(), `{"weekOf":"2025-W05"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

// TestBoardVisibility covers docs/DESIGN_V6_WEEK_ROLLOVER.md's board
// visibility table: undated open tasks and terminal tasks are filtered by
// weekOf == current week; dated open tasks are always visible regardless of
// weekOf. Also checks the new week/staleOpen response fields.
func TestBoardVisibility(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Board Team")
	directMember(t, store, Member{
		Name: "Board Admin", Email: "board-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "board-admin@example.com", "adminpass1")

	W := currentPeriod(HorizonWeekly)
	const oldWeek = "2020-W01"

	directTask(t, store, Task{
		Title: "undated open old", Horizon: HorizonDaily, Period: "2026-07-09",
		Status: StatusTodo, TeamID: &team.ID, WeekOf: oldWeek,
	})
	directTask(t, store, Task{
		Title: "dated open old", Horizon: HorizonDaily, Period: "2026-07-09",
		Status: StatusTodo, TeamID: &team.ID, WeekOf: oldWeek, DueDate: "2026-08-01",
	})
	directTask(t, store, Task{
		Title: "terminal old", Horizon: HorizonDaily, Period: "2026-07-09",
		Status: StatusDone, TeamID: &team.ID, WeekOf: oldWeek,
	})
	directTask(t, store, Task{
		Title: "terminal current", Horizon: HorizonDaily, Period: "2026-07-09",
		Status: StatusDone, TeamID: &team.ID, WeekOf: W,
	})
	directTask(t, store, Task{
		Title: "undated open current", Horizon: HorizonDaily, Period: "2026-07-09",
		Status: StatusInProgress, TeamID: &team.ID, WeekOf: W,
	})

	resp, err := c.do("GET", "/api/teams/"+team.ID.Hex()+"/board", "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	board := decodeJSON[Board](t, resp.Body)

	if board.Week != W {
		t.Fatalf("board.Week = %q, want %q", board.Week, W)
	}
	if board.StaleOpen != 1 {
		t.Fatalf("board.StaleOpen = %d, want 1 (only the undated-open-old task)", board.StaleOpen)
	}

	visible := map[string]bool{}
	for _, tv := range board.Unassigned {
		visible[tv.Title] = true
	}
	if visible["undated open old"] {
		t.Fatalf("stale undated open task should be hidden from the board")
	}
	if !visible["dated open old"] {
		t.Fatalf("open task with a dueDate should stay visible regardless of weekOf")
	}
	if visible["terminal old"] {
		t.Fatalf("stale terminal task should be hidden from the board")
	}
	if !visible["terminal current"] {
		t.Fatalf("current-week terminal task should be visible in the completed fold")
	}
	if !visible["undated open current"] {
		t.Fatalf("current-week undated open task should be visible")
	}
}

// TestTeamRollover covers the ADMIN-only rollover list: only open, undated,
// past-week tasks qualify, sorted weekOf asc then createdAt asc; USER gets
// 403 regardless of team membership.
func TestTeamRollover(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Rollover Team")
	directMember(t, store, Member{
		Name: "Rollover Admin", Email: "rollover-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	directMember(t, store, Member{
		Name: "Rollover User", Email: "rollover-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{team.ID},
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "rollover-admin@example.com", "adminpass1")
	cUser := newJSONClient(srv)
	loginAs(t, cUser, "rollover-user@example.com", "userpass1")

	W := currentPeriod(HorizonWeekly)

	directTask(t, store, Task{
		Title: "eligible earliest", Horizon: HorizonDaily, Period: "2026-07-01",
		Status: StatusTodo, TeamID: &team.ID, WeekOf: "2019-W01",
		CreatedAt: time.Date(2019, 1, 10, 0, 0, 0, 0, time.UTC),
	})
	directTask(t, store, Task{
		Title: "eligible later", Horizon: HorizonDaily, Period: "2026-07-01",
		Status: StatusInProgress, TeamID: &team.ID, WeekOf: "2020-W01",
		CreatedAt: time.Date(2020, 1, 10, 0, 0, 0, 0, time.UTC),
	})
	directTask(t, store, Task{ // excluded: has a dueDate
		Title: "has due date", Horizon: HorizonDaily, Period: "2026-07-01",
		Status: StatusTodo, TeamID: &team.ID, WeekOf: "2019-W01", DueDate: "2026-08-01",
	})
	directTask(t, store, Task{ // excluded: current week
		Title: "current week open", Horizon: HorizonDaily, Period: "2026-07-01",
		Status: StatusTodo, TeamID: &team.ID, WeekOf: W,
	})
	directTask(t, store, Task{ // excluded: terminal
		Title: "terminal old", Horizon: HorizonDaily, Period: "2026-07-01",
		Status: StatusDone, TeamID: &team.ID, WeekOf: "2019-W01",
	})

	t.Run("USER is forbidden", func(t *testing.T) {
		resp, err := cUser.do("GET", "/api/teams/"+team.ID.Hex()+"/rollover", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("ADMIN sees only eligible tasks, sorted weekOf then createdAt", func(t *testing.T) {
		resp, err := cAdmin.do("GET", "/api/teams/"+team.ID.Hex()+"/rollover", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[struct {
			Week  string     `json:"week"`
			Tasks []TaskView `json:"tasks"`
		}](t, resp.Body)
		if body.Week != W {
			t.Fatalf("week = %q, want %q", body.Week, W)
		}
		if len(body.Tasks) != 2 {
			t.Fatalf("len(tasks) = %d, want 2: %+v", len(body.Tasks), body.Tasks)
		}
		if body.Tasks[0].Title != "eligible earliest" || body.Tasks[1].Title != "eligible later" {
			t.Fatalf("unexpected order: %q, %q", body.Tasks[0].Title, body.Tasks[1].Title)
		}
	})
}

// TestTeamHistory covers the per-team paginated completed-task history:
// offset/limit with hasMore at both boundaries, newest-first by createdAt,
// current-week completions excluded (they live in the board's fold
// instead), and the same team-scoping as the board (USER only own teams).
func TestTeamHistory(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	teamA := directTeam(t, store, "History Team A")
	teamB := directTeam(t, store, "History Team B")
	directMember(t, store, Member{
		Name: "History Admin", Email: "history-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	directMember(t, store, Member{
		Name: "History User", Email: "history-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{teamA.ID},
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "history-admin@example.com", "adminpass1")
	cUser := newJSONClient(srv)
	loginAs(t, cUser, "history-user@example.com", "userpass1")

	W := currentPeriod(HorizonWeekly)

	directTask(t, store, Task{ // excluded: current-week completion
		Title: "current week done", Horizon: HorizonDaily, Period: "2026-07-01",
		Status: StatusDone, TeamID: &teamA.ID, WeekOf: W,
		CreatedAt: time.Now(),
	})

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		directTask(t, store, Task{
			Title:     fmt.Sprintf("history task %d", i),
			Horizon:   HorizonDaily,
			Period:    "2026-07-01",
			Status:    StatusDone,
			TeamID:    &teamA.ID,
			WeekOf:    "2019-W01",
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		})
	}

	t.Run("USER on a non-member team is forbidden", func(t *testing.T) {
		resp, err := cUser.do("GET", "/api/teams/"+teamB.ID.Hex()+"/history", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	type historyBody struct {
		Tasks   []TaskView `json:"tasks"`
		HasMore bool       `json:"hasMore"`
	}

	t.Run("first page: hasMore true, sorted createdAt desc, excludes current week", func(t *testing.T) {
		resp, err := cAdmin.do("GET", "/api/teams/"+teamA.ID.Hex()+"/history?limit=2", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[historyBody](t, resp.Body)
		if len(body.Tasks) != 2 {
			t.Fatalf("len(tasks) = %d, want 2", len(body.Tasks))
		}
		if !body.HasMore {
			t.Fatalf("hasMore = false, want true")
		}
		if body.Tasks[0].Title != "history task 2" || body.Tasks[1].Title != "history task 1" {
			t.Fatalf("unexpected order: %q, %q", body.Tasks[0].Title, body.Tasks[1].Title)
		}
		for _, tv := range body.Tasks {
			if tv.Title == "current week done" {
				t.Fatalf("current-week completion leaked into history")
			}
		}
	})

	t.Run("second page: hasMore false at the tail boundary", func(t *testing.T) {
		resp, err := cAdmin.do("GET", "/api/teams/"+teamA.ID.Hex()+"/history?offset=2&limit=2", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[historyBody](t, resp.Body)
		if len(body.Tasks) != 1 {
			t.Fatalf("len(tasks) = %d, want 1", len(body.Tasks))
		}
		if body.HasMore {
			t.Fatalf("hasMore = true, want false")
		}
		if body.Tasks[0].Title != "history task 0" {
			t.Fatalf("unexpected task at tail: %q", body.Tasks[0].Title)
		}
	})
}

// TestBackfillWeekOf covers the startup catch-up: team tasks missing weekOf
// get isoWeek(completedAt ?? createdAt); personal tasks and tasks that
// already carry weekOf are left untouched; a second run is a no-op.
func TestBackfillWeekOf(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	team := directTeam(t, store, "Backfill Team")

	completedAt := time.Date(2021, 6, 15, 12, 0, 0, 0, time.Local)
	createdAtWithCompletion := time.Date(2021, 1, 10, 9, 0, 0, 0, time.Local)
	createdAtNoCompletion := time.Date(2021, 3, 20, 9, 0, 0, 0, time.Local)
	createdAtPersonal := time.Date(2021, 3, 20, 9, 0, 0, 0, time.Local)

	withCompleted := directTask(t, store, Task{
		Title: "has completedAt", Horizon: HorizonDaily, Period: "2021-06-15",
		Status: StatusDone, TeamID: &team.ID,
		CreatedAt: createdAtWithCompletion, CompletedAt: &completedAt,
	})
	withoutCompleted := directTask(t, store, Task{
		Title: "no completedAt", Horizon: HorizonDaily, Period: "2021-03-20",
		Status: StatusTodo, TeamID: &team.ID,
		CreatedAt: createdAtNoCompletion,
	})
	personalTask := directTask(t, store, Task{
		Title: "personal, no teamId", Horizon: HorizonDaily, Period: "2021-03-20",
		Status: StatusTodo, CreatedAt: createdAtPersonal,
	})
	alreadySet := directTask(t, store, Task{
		Title: "already has weekOf", Horizon: HorizonDaily, Period: "2021-03-20",
		Status: StatusTodo, TeamID: &team.ID, WeekOf: "2099-W01",
		CreatedAt: createdAtNoCompletion,
	})

	if err := store.BackfillWeekOf(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	getWeekOf := func(id bson.ObjectID) string {
		t.Helper()
		var tk Task
		if err := store.tasks.FindOne(ctx, bson.M{"_id": id}).Decode(&tk); err != nil {
			t.Fatalf("fetch task: %v", err)
		}
		return tk.WeekOf
	}

	if got, want := getWeekOf(withCompleted.ID), isoWeekString(completedAt); got != want {
		t.Fatalf("withCompleted weekOf = %q, want %q (from completedAt)", got, want)
	}
	if got, want := getWeekOf(withoutCompleted.ID), isoWeekString(createdAtNoCompletion); got != want {
		t.Fatalf("withoutCompleted weekOf = %q, want %q (from createdAt)", got, want)
	}
	if got := getWeekOf(personalTask.ID); got != "" {
		t.Fatalf("personal task backfilled weekOf = %q, want left empty (untouched)", got)
	}
	if got := getWeekOf(alreadySet.ID); got != "2099-W01" {
		t.Fatalf("already-set weekOf changed to %q, want unchanged 2099-W01", got)
	}

	// Idempotency: a second run must not alter anything (no doc still
	// matches the "weekOf missing" filter).
	if err := store.BackfillWeekOf(ctx); err != nil {
		t.Fatalf("second backfill run: %v", err)
	}
	if got, want := getWeekOf(withCompleted.ID), isoWeekString(completedAt); got != want {
		t.Fatalf("weekOf changed on second backfill run: got %q want %q", got, want)
	}
}
