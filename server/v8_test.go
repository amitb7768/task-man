package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// daysFromToday returns "today + n days" as a "YYYY-MM-DD" string, for
// building relative-to-now dueDate/period fixtures without hardcoding dates
// that would eventually go stale — ViewAttention compares against real
// time.Now(), not a fixed date, unlike some of the older tests' hardcoded
// 2026-07-09 fixtures (those don't care about "today").
func daysFromToday(n int) string {
	return time.Now().AddDate(0, 0, n).Format(dateLayout)
}

// TestV8AttentionClearsOnReschedule covers
// docs/DESIGN_V8_ATTENTION_REASSIGN.md's "Server — regression tests only"
// item 1: a personal task with a stale period (no dueDate) shows up in
// ViewAttention's "slipped" bucket; PATCHing its period into the future
// (same horizon) clears it. Separately, a personal task with a past dueDate
// shows up in "overdue"; PATCHing dueDate into the future clears it. This is
// the server-side guarantee the "Schedule for…" bulk action leans on.
func TestV8AttentionClearsOnReschedule(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	// Genuinely team-less (TeamIDs nil) — taskScopeFilter now normalizes the
	// resulting {"teamId": {"$in": nil}} to an empty slice (B2 fix), so this
	// no longer needs a dodge-team fixture to avoid a 500.
	directMember(t, store, Member{
		Name: "V8 Attention User", Email: "v8-attention-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "v8-attention-user@example.com", "userpass1")

	type attentionBody struct {
		Overdue []TaskView `json:"overdue"`
		Slipped []TaskView `json:"slipped"`
	}
	getAttention := func() attentionBody {
		t.Helper()
		resp, err := c.do("GET", "/api/views/attention", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("attention status = %d, want 200", resp.StatusCode)
		}
		return decodeJSON[attentionBody](t, resp.Body)
	}
	hasTask := func(views []TaskView, id bson.ObjectID) bool {
		for _, v := range views {
			if v.ID == id {
				return true
			}
		}
		return false
	}

	t.Run("slipped personal task clears when period moves to the future", func(t *testing.T) {
		stalePeriod := daysFromToday(-5)
		createResp, err := c.do("POST", "/api/tasks", fmt.Sprintf(
			`{"title":"slipped daily","horizon":"daily","period":%q}`, stalePeriod))
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		before := getAttention()
		if !hasTask(before.Slipped, created.ID) {
			t.Fatalf("task missing from slipped before reschedule")
		}

		futurePeriod := daysFromToday(10)
		patchResp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), fmt.Sprintf(`{"period":%q}`, futurePeriod))
		if err != nil {
			t.Fatal(err)
		}
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusOK {
			t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
		}

		after := getAttention()
		if hasTask(after.Slipped, created.ID) || hasTask(after.Overdue, created.ID) {
			t.Fatalf("task still present in attention after period reschedule to the future")
		}
	})

	t.Run("overdue personal task clears when dueDate moves to the future", func(t *testing.T) {
		today := currentPeriod(HorizonDaily)
		pastDue := daysFromToday(-3)
		createResp, err := c.do("POST", "/api/tasks", fmt.Sprintf(
			`{"title":"overdue daily","horizon":"daily","period":%q,"dueDate":%q}`, today, pastDue))
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		before := getAttention()
		if !hasTask(before.Overdue, created.ID) {
			t.Fatalf("task missing from overdue before reschedule")
		}

		futureDue := daysFromToday(10)
		patchResp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), fmt.Sprintf(`{"dueDate":%q}`, futureDue))
		if err != nil {
			t.Fatal(err)
		}
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusOK {
			t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
		}

		after := getAttention()
		if hasTask(after.Slipped, created.ID) || hasTask(after.Overdue, created.ID) {
			t.Fatalf("task still present in attention after dueDate reschedule to the future")
		}
	})
}

// TestV8RecurringInstancePeriodPatchPreservesAnchor covers regression test 2:
// PATCHing only the period of a recurring instance is a 200, and
// Recurrence.Anchor stays byte-identical to what it was before the patch.
// server/CLAUDE.md documents this as deliberate: "PatchTask re-anchors only
// on freq/interval change... A raw UpdateOne on period desyncs recurrence
// (Reschedule already has this wart — don't spread it)." The v8 "Schedule
// for…" date picker's period-only PATCH must not accidentally re-anchor.
func TestV8RecurringInstancePeriodPatchPreservesAnchor(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "V8 Recur User", Email: "v8-recur-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "v8-recur-user@example.com", "userpass1")

	today := currentPeriod(HorizonDaily)
	createResp, err := c.do("POST", "/api/tasks", fmt.Sprintf(
		`{"title":"daily recurrence","horizon":"daily","period":%q,"recurrence":{"freq":"daily"}}`, today))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", createResp.StatusCode)
	}
	created := decodeJSON[TaskView](t, createResp.Body)
	if created.Recurrence == nil || created.Recurrence.Anchor == "" {
		t.Fatalf("created recurring task missing anchor: %+v", created.Recurrence)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var before Task
	if err := store.tasks.FindOne(ctx, bson.M{"_id": created.ID}).Decode(&before); err != nil {
		t.Fatalf("fetch task before patch: %v", err)
	}
	if before.Recurrence == nil {
		t.Fatalf("stored task missing recurrence before patch")
	}
	beforeAnchor := before.Recurrence.Anchor

	futurePeriod := daysFromToday(20)
	patchResp, err := c.do("PATCH", "/api/tasks/"+created.ID.Hex(), fmt.Sprintf(`{"period":%q}`, futurePeriod))
	if err != nil {
		t.Fatal(err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
	}

	var after Task
	if err := store.tasks.FindOne(ctx, bson.M{"_id": created.ID}).Decode(&after); err != nil {
		t.Fatalf("fetch task after patch: %v", err)
	}
	if after.Period != futurePeriod {
		t.Fatalf("period after patch = %q, want %q", after.Period, futurePeriod)
	}
	if after.Recurrence == nil {
		t.Fatalf("recurrence disappeared after period-only patch")
	}
	if after.Recurrence.Anchor != beforeAnchor {
		t.Fatalf("anchor changed on a period-only patch: before %q, after %q", beforeAnchor, after.Recurrence.Anchor)
	}
}

// TestV8ParkPersonalTask covers regression test 3: a personal task PATCHed
// to {horizon:"backlog", period:"", dueDate:""} lands in the patcher's own
// GET /api/backlog when the patcher is ADMIN; a USER gets the same 200 on
// the patch but is 403'd by /api/backlog itself (ADMIN-only endpoint per
// docs/AUTH_FEATURES.md) — their parked task is still discoverable via
// GET /api/search?horizon=backlog, scoped to their own ownerId.
func TestV8ParkPersonalTask(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "V8 Park Admin", Email: "v8-park-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	// Genuinely team-less (TeamIDs nil) — taskScopeFilter now normalizes the
	// resulting {"teamId": {"$in": nil}} to an empty slice (B2 fix), so this
	// no longer needs a dodge-team fixture to avoid a 500 on the
	// GET /api/search below.
	directMember(t, store, Member{
		Name: "V8 Park User", Email: "v8-park-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "v8-park-admin@example.com", "adminpass1")
	cUser := newJSONClient(srv)
	loginAs(t, cUser, "v8-park-user@example.com", "userpass1")

	const parkBody = `{"horizon":"backlog","period":"","dueDate":""}`

	type tasksBody struct {
		Tasks []TaskView `json:"tasks"`
	}

	t.Run("ADMIN parks personal task into own backlog", func(t *testing.T) {
		today := currentPeriod(HorizonDaily)
		createResp, err := cAdmin.do("POST", "/api/tasks", fmt.Sprintf(
			`{"title":"admin park me","horizon":"daily","period":%q}`, today))
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		patchResp, err := cAdmin.do("PATCH", "/api/tasks/"+created.ID.Hex(), parkBody)
		if err != nil {
			t.Fatal(err)
		}
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusOK {
			t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
		}

		backlogResp, err := cAdmin.do("GET", "/api/backlog", "")
		if err != nil {
			t.Fatal(err)
		}
		defer backlogResp.Body.Close()
		if backlogResp.StatusCode != http.StatusOK {
			t.Fatalf("backlog status = %d, want 200", backlogResp.StatusCode)
		}
		body := decodeJSON[tasksBody](t, backlogResp.Body)
		found := false
		for _, tv := range body.Tasks {
			if tv.ID == created.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("parked task missing from admin's backlog")
		}
	})

	t.Run("USER parks personal task: 403 on /api/backlog, discoverable via search", func(t *testing.T) {
		today := currentPeriod(HorizonDaily)
		createResp, err := cUser.do("POST", "/api/tasks", fmt.Sprintf(
			`{"title":"user park me","horizon":"daily","period":%q}`, today))
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		patchResp, err := cUser.do("PATCH", "/api/tasks/"+created.ID.Hex(), parkBody)
		if err != nil {
			t.Fatal(err)
		}
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusOK {
			t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
		}

		backlogResp, err := cUser.do("GET", "/api/backlog", "")
		if err != nil {
			t.Fatal(err)
		}
		defer backlogResp.Body.Close()
		if backlogResp.StatusCode != http.StatusForbidden {
			t.Fatalf("USER /api/backlog status = %d, want 403", backlogResp.StatusCode)
		}

		searchResp, err := cUser.do("GET", "/api/search?horizon=backlog", "")
		if err != nil {
			t.Fatal(err)
		}
		defer searchResp.Body.Close()
		if searchResp.StatusCode != http.StatusOK {
			t.Fatalf("search status = %d, want 200", searchResp.StatusCode)
		}
		body := decodeJSON[tasksBody](t, searchResp.Body)
		found := false
		for _, tv := range body.Tasks {
			if tv.ID == created.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("parked task missing from USER's own horizon=backlog search")
		}
	})
}

// TestV8ParkTeamTask covers regression test 4: ADMIN parking a team task via
// a single PATCH {teamId:null, assigneeId:null, horizon:"backlog",
// period:"", dueDate:""} un-teams it into the patching ADMIN's own backlog
// (ownerId == that admin); a USER attempting the identical patch on their
// own team's task is 403 — the team/personal flip is ADMIN-only, per
// docs/DESIGN_V8_ATTENTION_REASSIGN.md's "Move to backlog" eligibility rule
// (store.go's existing "only an admin can change a task's team" gate).
func TestV8ParkTeamTask(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "V8 Park Team")
	admin := directMember(t, store, Member{
		Name: "V8 Park Team Admin", Email: "v8-park-team-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	directMember(t, store, Member{
		Name: "V8 Park Team User", Email: "v8-park-team-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{team.ID},
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "v8-park-team-admin@example.com", "adminpass1")
	cUser := newJSONClient(srv)
	loginAs(t, cUser, "v8-park-team-user@example.com", "userpass1")

	const parkBody = `{"teamId":null,"assigneeId":null,"horizon":"backlog","period":"","dueDate":""}`

	t.Run("ADMIN parks a team task into own backlog", func(t *testing.T) {
		today := currentPeriod(HorizonDaily)
		createResp, err := cAdmin.do("POST", "/api/tasks", fmt.Sprintf(
			`{"title":"admin park team task","horizon":"daily","period":%q,"teamId":%q}`, today, team.ID.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		patchResp, err := cAdmin.do("PATCH", "/api/tasks/"+created.ID.Hex(), parkBody)
		if err != nil {
			t.Fatal(err)
		}
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusOK {
			t.Fatalf("patch status = %d, want 200", patchResp.StatusCode)
		}
		parked := decodeJSON[TaskView](t, patchResp.Body)
		if parked.TeamID != nil {
			t.Fatalf("teamId after park = %v, want nil", parked.TeamID)
		}
		if parked.Horizon != HorizonBacklog || parked.Period != "" {
			t.Fatalf("horizon/period after park = %q/%q, want backlog/empty", parked.Horizon, parked.Period)
		}
		if parked.OwnerID == nil || *parked.OwnerID != admin.ID {
			t.Fatalf("ownerId after park = %v, want patching admin %s", parked.OwnerID, admin.ID.Hex())
		}

		backlogResp, err := cAdmin.do("GET", "/api/backlog", "")
		if err != nil {
			t.Fatal(err)
		}
		defer backlogResp.Body.Close()
		body := decodeJSON[struct {
			Tasks []TaskView `json:"tasks"`
		}](t, backlogResp.Body)
		found := false
		for _, tv := range body.Tasks {
			if tv.ID == created.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("parked team task missing from admin's backlog")
		}
	})

	t.Run("USER cannot park their own team's task", func(t *testing.T) {
		today := currentPeriod(HorizonDaily)
		createResp, err := cAdmin.do("POST", "/api/tasks", fmt.Sprintf(
			`{"title":"user cannot park","horizon":"daily","period":%q,"teamId":%q}`, today, team.ID.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want 201", createResp.StatusCode)
		}
		created := decodeJSON[TaskView](t, createResp.Body)

		patchResp, err := cUser.do("PATCH", "/api/tasks/"+created.ID.Hex(), parkBody)
		if err != nil {
			t.Fatal(err)
		}
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusForbidden {
			t.Fatalf("USER park status = %d, want 403", patchResp.StatusCode)
		}
		errBody := decodeJSON[map[string]string](t, patchResp.Body)
		const wantMsg = "only an admin can change a task's team"
		if !strings.Contains(errBody["error"], wantMsg) {
			t.Fatalf("error message = %q, want to contain %q", errBody["error"], wantMsg)
		}
	})
}

// TestV8UnparkRestoresWeekOf covers regression test 5: ADMIN parks a
// team task that was first stamped with a deliberately non-current weekOf
// (via the existing ADMIN-only weekOf-move primitive), then restores it in
// ONE PATCH carrying {teamId, assigneeId, horizon, period, dueDate,
// weekOf:<original>}.
//
// docs/DESIGN_V8_ATTENTION_REASSIGN.md's undo contract requires that
// explicit weekOf win over PatchTask's personal->team flip stamp: PatchTask
// captures the client's explicit weekOf right after json.Unmarshal and
// reapplies it inside the weekOfPatched block, after the personal/team-flip
// switch has had its say (see store.go's PatchTask).
func TestV8UnparkRestoresWeekOf(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "V8 Unpark Team")
	directMember(t, store, Member{
		Name: "V8 Unpark Admin", Email: "v8-unpark-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	member := directMember(t, store, Member{
		Name: "V8 Unpark Member", Email: "v8-unpark-member@example.com",
		TeamIDs: []bson.ObjectID{team.ID},
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "v8-unpark-admin@example.com", "adminpass1")

	const origHorizon = HorizonDaily
	origPeriod := currentPeriod(HorizonDaily)
	createResp, err := cAdmin.do("POST", "/api/tasks", fmt.Sprintf(
		`{"title":"unpark roundtrip","horizon":%q,"period":%q,"teamId":%q,"assigneeId":%q}`,
		origHorizon, origPeriod, team.ID.Hex(), member.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", createResp.StatusCode)
	}
	created := decodeJSON[TaskView](t, createResp.Body)

	// Give it a deliberately non-current weekOf (the create-time weekOf is
	// always "this week" already, which would make the final assertion
	// below pass trivially regardless of whether the explicit patch or the
	// flip stamp "won" — this ADMIN-only move makes them distinguishable).
	const originalWeekOf = "2020-W05"
	weekOfSetupResp, err := cAdmin.do("PATCH", "/api/tasks/"+created.ID.Hex(), fmt.Sprintf(`{"weekOf":%q}`, originalWeekOf))
	if err != nil {
		t.Fatal(err)
	}
	defer weekOfSetupResp.Body.Close()
	if weekOfSetupResp.StatusCode != http.StatusOK {
		t.Fatalf("weekOf setup patch status = %d, want 200", weekOfSetupResp.StatusCode)
	}
	stamped := decodeJSON[TaskView](t, weekOfSetupResp.Body)
	if stamped.WeekOf != originalWeekOf {
		t.Fatalf("setup: weekOf = %q, want %q", stamped.WeekOf, originalWeekOf)
	}

	// Park: ADMIN single PATCH per docs/DESIGN_V8_ATTENTION_REASSIGN.md.
	const parkBody = `{"teamId":null,"assigneeId":null,"horizon":"backlog","period":"","dueDate":""}`
	parkResp, err := cAdmin.do("PATCH", "/api/tasks/"+created.ID.Hex(), parkBody)
	if err != nil {
		t.Fatal(err)
	}
	defer parkResp.Body.Close()
	if parkResp.StatusCode != http.StatusOK {
		t.Fatalf("park status = %d, want 200", parkResp.StatusCode)
	}

	// Restore: ONE PATCH carrying every original field, including the
	// explicit original weekOf — this is the shape the "Move to backlog"
	// undo toast builds (docs/DESIGN_V8_ATTENTION_REASSIGN.md).
	restoreBody := fmt.Sprintf(
		`{"teamId":%q,"assigneeId":%q,"horizon":%q,"period":%q,"dueDate":"","weekOf":%q}`,
		team.ID.Hex(), member.ID.Hex(), origHorizon, origPeriod, originalWeekOf)
	restoreResp, err := cAdmin.do("PATCH", "/api/tasks/"+created.ID.Hex(), restoreBody)
	if err != nil {
		t.Fatal(err)
	}
	defer restoreResp.Body.Close()
	if restoreResp.StatusCode != http.StatusOK {
		t.Fatalf("restore status = %d, want 200", restoreResp.StatusCode)
	}
	restored := decodeJSON[TaskView](t, restoreResp.Body)

	if restored.WeekOf != originalWeekOf {
		t.Fatalf("weekOf after single-PATCH restore = %q, want original %q "+
			"(explicit weekOf in the restore PATCH must win over the "+
			"personal->team flip stamp)", restored.WeekOf, originalWeekOf)
	}
}

// TestV8ReassignWithinTeam covers regression test 6: a USER who is a member
// of a team may PATCH a team task's assigneeId to another member of that
// same team (200, no special role gate needed — docs/AUTH_FEATURES.md
// decision #5); PATCHing assigneeId to a member outside the team still hits
// the existing "assignee does not belong to team" 400 validator, unchanged.
func TestV8ReassignWithinTeam(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "V8 Reassign Team")
	directMember(t, store, Member{
		Name: "V8 Reassign Admin", Email: "v8-reassign-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	memberA := directMember(t, store, Member{
		Name: "V8 Reassign Member A", Email: "v8-reassign-member-a@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{team.ID},
	})
	memberB := directMember(t, store, Member{
		Name: "V8 Reassign Member B", Email: "v8-reassign-member-b@example.com",
		TeamIDs: []bson.ObjectID{team.ID},
	})
	outsider := directMember(t, store, Member{
		Name: "V8 Reassign Outsider", Email: "v8-reassign-outsider@example.com",
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "v8-reassign-admin@example.com", "adminpass1")
	cMemberA := newJSONClient(srv)
	loginAs(t, cMemberA, "v8-reassign-member-a@example.com", "userpass1")

	today := currentPeriod(HorizonDaily)
	createResp, err := cAdmin.do("POST", "/api/tasks", fmt.Sprintf(
		`{"title":"reassign me","horizon":"daily","period":%q,"teamId":%q,"assigneeId":%q}`,
		today, team.ID.Hex(), memberA.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", createResp.StatusCode)
	}
	created := decodeJSON[TaskView](t, createResp.Body)

	t.Run("USER member reassigns to another member of the same team: 200", func(t *testing.T) {
		resp, err := cMemberA.do("PATCH", "/api/tasks/"+created.ID.Hex(), fmt.Sprintf(`{"assigneeId":%q}`, memberB.ID.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		v := decodeJSON[TaskView](t, resp.Body)
		if v.AssigneeID == nil || *v.AssigneeID != memberB.ID {
			t.Fatalf("assigneeId = %v, want %s", v.AssigneeID, memberB.ID.Hex())
		}
	})

	t.Run("USER member reassigns to a non-member: 400", func(t *testing.T) {
		resp, err := cMemberA.do("PATCH", "/api/tasks/"+created.ID.Hex(), fmt.Sprintf(`{"assigneeId":%q}`, outsider.ID.Hex()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		errBody := decodeJSON[map[string]string](t, resp.Body)
		const wantMsg = "assignee does not belong to team"
		if !strings.Contains(errBody["error"], wantMsg) {
			t.Fatalf("error message = %q, want to contain %q", errBody["error"], wantMsg)
		}
	})
}

// TestV8TeamlessUserScopedReads covers the B2 fix: taskScopeFilter's USER
// branch builds {"teamId": {"$in": u.TeamIDs}}; a genuinely team-less USER
// has TeamIDs == nil, which marshals to BSON null, and Mongo used to reject
// that with "(BadValue) $in needs an array" (500) on every scoped read
// (attention/search/reschedule). taskScopeFilter now normalizes a nil
// TeamIDs to an empty slice so the query is well-formed and simply matches
// no team tasks — this replaces the dodge-team fixtures other v8 tests used
// to work around the bug (see TestV8AttentionClearsOnReschedule,
// TestV8ParkPersonalTask).
func TestV8TeamlessUserScopedReads(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	// Genuinely team-less: TeamIDs left at its zero value (nil).
	directMember(t, store, Member{
		Name: "V8 Teamless User", Email: "v8-teamless-user@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
	})
	c := newJSONClient(srv)
	loginAs(t, c, "v8-teamless-user@example.com", "userpass1")

	// Personal task with a stale period: should land in "slipped".
	stalePeriod := daysFromToday(-5)
	createResp, err := c.do("POST", "/api/tasks", fmt.Sprintf(
		`{"title":"teamless slipped","horizon":"daily","period":%q}`, stalePeriod))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", createResp.StatusCode)
	}
	created := decodeJSON[TaskView](t, createResp.Body)

	// A team task belonging to a team this user has never joined: must
	// never appear in either view, whether or not the nil-TeamIDs case is
	// handled correctly (an empty $in must match nothing, not everything).
	otherTeam := directTeam(t, store, "V8 Teamless Other Team")
	otherTask := directTask(t, store, Task{
		Title:   "other team's task",
		Horizon: HorizonDaily,
		Period:  daysFromToday(-5),
		Status:  StatusTodo,
		TeamID:  &otherTeam.ID,
	})

	type attentionBody struct {
		Overdue []TaskView `json:"overdue"`
		Slipped []TaskView `json:"slipped"`
	}
	hasTask := func(views []TaskView, id bson.ObjectID) bool {
		for _, v := range views {
			if v.ID == id {
				return true
			}
		}
		return false
	}

	attResp, err := c.do("GET", "/api/views/attention", "")
	if err != nil {
		t.Fatal(err)
	}
	defer attResp.Body.Close()
	if attResp.StatusCode != http.StatusOK {
		t.Fatalf("attention status = %d, want 200 (teamless USER must not 500 on $in:null)", attResp.StatusCode)
	}
	att := decodeJSON[attentionBody](t, attResp.Body)
	if !hasTask(att.Slipped, created.ID) {
		t.Fatalf("teamless USER's slipped personal task missing from attention")
	}
	if hasTask(att.Slipped, otherTask.ID) || hasTask(att.Overdue, otherTask.ID) {
		t.Fatalf("other team's task leaked into teamless USER's attention view")
	}

	searchResp, err := c.do("GET", "/api/search", "")
	if err != nil {
		t.Fatal(err)
	}
	defer searchResp.Body.Close()
	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("search status = %d, want 200 (teamless USER must not 500 on $in:null)", searchResp.StatusCode)
	}
	type tasksBody struct {
		Tasks []TaskView `json:"tasks"`
	}
	search := decodeJSON[tasksBody](t, searchResp.Body)
	found, leaked := false, false
	for _, tv := range search.Tasks {
		if tv.ID == created.ID {
			found = true
		}
		if tv.ID == otherTask.ID {
			leaked = true
		}
	}
	if !found {
		t.Fatalf("teamless USER's own personal task missing from search")
	}
	if leaked {
		t.Fatalf("other team's task leaked into teamless USER's search results")
	}
}
