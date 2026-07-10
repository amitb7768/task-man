package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// mustHash is a test-only helper: bcrypt-hash a password or fail the test.
func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := hashPassword(pw)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	return h
}

func decodeJSON[T any](t *testing.T, r io.Reader) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return v
}

// TestAuthFlow covers docs/AUTH_FEATURES.md's core session lifecycle:
// login -> me -> logout -> me(401), plus change-password's mustChangePassword
// gate and its "kill other sessions, keep mine" behavior.
func TestAuthFlow(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	admin := directMember(t, store, Member{
		Name: "Ada Admin", Email: "ada@example.com",
		PasswordHash: mustHash(t, "correcthorse1"),
		SystemRole:   RoleAdmin,
	})

	client := newJSONClient(srv)

	t.Run("login wrong password fails", func(t *testing.T) {
		resp, err := client.do("POST", "/api/auth/login", `{"email":"ada@example.com","password":"nope"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("me before login is 401", func(t *testing.T) {
		fresh := newJSONClient(srv)
		resp, err := fresh.do("GET", "/api/auth/me", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("login succeeds and sets cookie", func(t *testing.T) {
		resp, err := client.do("POST", "/api/auth/login", `{"email":"ada@example.com","password":"correcthorse1"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
		}
		var body struct {
			User MeUser `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.User.Email != "ada@example.com" || body.User.SystemRole != RoleAdmin {
			t.Fatalf("unexpected user in login response: %+v", body.User)
		}
		found := false
		for _, c := range resp.Cookies() {
			if c.Name == sessionCookieName {
				found = true
			}
		}
		if !found {
			t.Fatalf("no session cookie set on login response")
		}
	})

	t.Run("me after login reflects the session", func(t *testing.T) {
		resp, err := client.do("GET", "/api/auth/me", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[struct {
			User MeUser `json:"user"`
		}](t, resp.Body)
		if body.User.ID != admin.ID.Hex() {
			t.Fatalf("me id = %q, want %q", body.User.ID, admin.ID.Hex())
		}
	})

	t.Run("logout then me is 401", func(t *testing.T) {
		resp, err := client.do("POST", "/api/auth/logout", "")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("logout status = %d, want 204", resp.StatusCode)
		}
		resp2, err := client.do("GET", "/api/auth/me", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusUnauthorized {
			t.Fatalf("me-after-logout status = %d, want 401", resp2.StatusCode)
		}
	})
}

// TestMustChangePasswordGate covers: mutations blocked except
// change-password while mustChangePassword is set; reads still allowed;
// change-password clears the flag, keeps the current session, and kills
// other sessions for the same user.
func TestMustChangePasswordGate(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "New User", Email: "newbie@example.com",
		PasswordHash:       mustHash(t, "temppass1"),
		SystemRole:         RoleUser,
		MustChangePassword: true,
	})

	sessionA := newJSONClient(srv)
	loginResp, err := sessionA.do("POST", "/api/auth/login", `{"email":"newbie@example.com","password":"temppass1"}`)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}

	// A second concurrent session for the same user (e.g. another browser).
	sessionB := newJSONClient(srv)
	loginResp2, err := sessionB.do("POST", "/api/auth/login", `{"email":"newbie@example.com","password":"temppass1"}`)
	if err != nil {
		t.Fatal(err)
	}
	loginResp2.Body.Close()

	t.Run("reads allowed while mustChangePassword", func(t *testing.T) {
		resp, err := sessionA.do("GET", "/api/auth/me", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decodeJSON[struct {
			User MeUser `json:"user"`
		}](t, resp.Body)
		if !body.User.MustChangePassword {
			t.Fatalf("mustChangePassword = false, want true")
		}
	})

	t.Run("mutation blocked while mustChangePassword", func(t *testing.T) {
		resp, err := sessionA.do("POST", "/api/tasks", `{"title":"x","horizon":"daily","period":"2026-07-09"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("change-password with wrong current is rejected", func(t *testing.T) {
		resp, err := sessionA.do("POST", "/api/auth/change-password", `{"current":"wrong","new":"newpassword1"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("change-password with short new password is rejected", func(t *testing.T) {
		resp, err := sessionA.do("POST", "/api/auth/change-password", `{"current":"temppass1","new":"short"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("change-password succeeds, clears flag, keeps this session, kills the other", func(t *testing.T) {
		resp, err := sessionA.do("POST", "/api/auth/change-password", `{"current":"temppass1","new":"newpassword1"}`)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}

		// sessionA (the one that changed the password) survives.
		meResp, err := sessionA.do("GET", "/api/auth/me", "")
		if err != nil {
			t.Fatal(err)
		}
		defer meResp.Body.Close()
		if meResp.StatusCode != http.StatusOK {
			t.Fatalf("session A me status = %d, want 200", meResp.StatusCode)
		}
		body := decodeJSON[struct {
			User MeUser `json:"user"`
		}](t, meResp.Body)
		if body.User.MustChangePassword {
			t.Fatalf("mustChangePassword still true after change-password")
		}

		// the mutation gate should now be lifted.
		taskResp, err := sessionA.do("POST", "/api/tasks", `{"title":"x","horizon":"daily","period":"2026-07-09"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer taskResp.Body.Close()
		if taskResp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(taskResp.Body)
			t.Fatalf("create task status = %d, want 201: %s", taskResp.StatusCode, body)
		}

		// sessionB (the other, untouched session) must be invalidated.
		meResp2, err := sessionB.do("GET", "/api/auth/me", "")
		if err != nil {
			t.Fatal(err)
		}
		defer meResp2.Body.Close()
		if meResp2.StatusCode != http.StatusUnauthorized {
			t.Fatalf("other session status = %d, want 401 (invalidated)", meResp2.StatusCode)
		}
	})
}

// TestGatingMatrixSpotChecks exercises the specific IDOR/role scenarios
// called out in the build instructions: a USER hitting an admin-only route,
// a USER patching another user's personal task, and a USER reading a board
// for a team they don't belong to.
func TestGatingMatrixSpotChecks(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	teamA := directTeam(t, store, "Team A")
	teamB := directTeam(t, store, "Team B")

	user1 := directMember(t, store, Member{
		Name: "User One", Email: "user1@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{teamA.ID},
	})
	user2 := directMember(t, store, Member{
		Name: "User Two", Email: "user2@example.com",
		PasswordHash: mustHash(t, "userpass2"),
		SystemRole:   RoleUser,
	})
	_ = user1

	c1 := newJSONClient(srv)
	loginAs(t, c1, "user1@example.com", "userpass1")
	c2 := newJSONClient(srv)
	loginAs(t, c2, "user2@example.com", "userpass2")

	t.Run("USER hitting admin-only endpoint gets 403", func(t *testing.T) {
		resp, err := c1.do("POST", "/api/teams", `{"name":"Should Not Be Created"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("USER cannot patch another user's personal task", func(t *testing.T) {
		// user2 creates a personal task.
		createResp, err := c2.do("POST", "/api/tasks", `{"title":"user2 private","horizon":"daily","period":"2026-07-09"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(createResp.Body)
			t.Fatalf("create status = %d: %s", createResp.StatusCode, body)
		}
		created := decodeJSON[TaskView](t, createResp.Body)
		if created.OwnerID == nil || *created.OwnerID != user2.ID {
			t.Fatalf("ownerId = %v, want %s", created.OwnerID, user2.ID.Hex())
		}

		// user1 tries to patch it.
		patchResp, err := c1.do("PATCH", "/api/tasks/"+created.ID.Hex(), `{"title":"hijacked"}`)
		if err != nil {
			t.Fatal(err)
		}
		defer patchResp.Body.Close()
		if patchResp.StatusCode != http.StatusForbidden && patchResp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 403 or 404", patchResp.StatusCode)
		}
	})

	t.Run("USER cannot read another team's board", func(t *testing.T) {
		resp, err := c1.do("GET", fmt.Sprintf("/api/teams/%s/board", teamB.ID.Hex()), "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("USER can read own team's board", func(t *testing.T) {
		resp, err := c1.do("GET", fmt.Sprintf("/api/teams/%s/board", teamA.ID.Hex()), "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("USER personal task invisible to another USER's personal views", func(t *testing.T) {
		// user2's task created above must not show up in user1's search.
		resp, err := c1.do("GET", "/api/search?q=user2", "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body := decodeJSON[struct {
			Tasks []TaskView `json:"tasks"`
		}](t, resp.Body)
		for _, tv := range body.Tasks {
			if tv.Title == "user2 private" {
				t.Fatalf("user1's search leaked user2's personal task")
			}
		}
	})
}

// TestChildAccessFiltering covers the Opus-review BLOCKER: GetTaskDetail's
// children list must be access-filtered per canAccessTask, not just the
// root. Attaching a personal task under a team task you can see is a
// legitimate, allowed action — but the resulting personal child must still
// stay invisible to everyone but its owner, including ADMIN (decision #4).
func TestChildAccessFiltering(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	teamA := directTeam(t, store, "Team A")
	directMember(t, store, Member{
		Name: "Ada Admin", Email: "cf-admin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	directMember(t, store, Member{
		Name: "User One", Email: "cf-user1@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{teamA.ID},
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "cf-admin@example.com", "adminpass1")
	c1 := newJSONClient(srv)
	loginAs(t, c1, "cf-user1@example.com", "userpass1")

	// user1 creates a team task in their own team — legitimate.
	teamTaskResp, err := c1.do("POST", "/api/tasks", fmt.Sprintf(`{"title":"team task","horizon":"daily","period":"2026-07-09","teamId":%q}`, teamA.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer teamTaskResp.Body.Close()
	if teamTaskResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(teamTaskResp.Body)
		t.Fatalf("create team task status = %d: %s", teamTaskResp.StatusCode, body)
	}
	teamTask := decodeJSON[TaskView](t, teamTaskResp.Body)

	// user1 parents a PERSONAL task under it — allowed, since user1 can
	// access the parent (own team). The child stays theirs alone.
	childResp, err := c1.do("POST", "/api/tasks", fmt.Sprintf(`{"title":"user1 private child","horizon":"daily","period":"2026-07-09","parentId":%q}`, teamTask.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer childResp.Body.Close()
	if childResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(childResp.Body)
		t.Fatalf("create personal child status = %d: %s", childResp.StatusCode, body)
	}

	t.Run("admin GET of the team task omits the foreign personal child", func(t *testing.T) {
		resp, err := cAdmin.do("GET", "/api/tasks/"+teamTask.ID.Hex(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		detail := decodeJSON[TaskDetail](t, resp.Body)
		for _, c := range detail.Children {
			if c.Title == "user1 private child" {
				t.Fatalf("admin's GET leaked user1's personal child task")
			}
		}
	})

	t.Run("owner's own GET still includes their own child", func(t *testing.T) {
		resp, err := c1.do("GET", "/api/tasks/"+teamTask.ID.Hex(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		detail := decodeJSON[TaskDetail](t, resp.Body)
		found := false
		for _, c := range detail.Children {
			if c.Title == "user1 private child" {
				found = true
			}
		}
		if !found {
			t.Fatalf("owner's own GET should still show their own personal child")
		}
	})
}

// TestParentIDInjectionBlocked covers the other half of the BLOCKER: a
// caller must be authorized to access the intended PARENT before a task can
// be attached under it, else a USER could inject a child into someone
// else's private subtree purely via parentId.
func TestParentIDInjectionBlocked(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Priv One", Email: "priv1@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
	})
	directMember(t, store, Member{
		Name: "Priv Two", Email: "priv2@example.com",
		PasswordHash: mustHash(t, "userpass2"),
		SystemRole:   RoleUser,
	})

	c1 := newJSONClient(srv)
	loginAs(t, c1, "priv1@example.com", "userpass1")
	c2 := newJSONClient(srv)
	loginAs(t, c2, "priv2@example.com", "userpass2")

	privResp, err := c1.do("POST", "/api/tasks", `{"title":"user1 secret","horizon":"daily","period":"2026-07-09"}`)
	if err != nil {
		t.Fatal(err)
	}
	defer privResp.Body.Close()
	priv := decodeJSON[TaskView](t, privResp.Body)

	resp, err := c2.do("POST", "/api/tasks", fmt.Sprintf(`{"title":"injected child","horizon":"daily","period":"2026-07-09","parentId":%q}`, priv.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
	}
}

// TestUserCannotChangeTaskTeamBoundary covers the MAJOR review finding: a
// USER must not be able to move a task across the team/personal boundary
// (team->personal escapes admin oversight; the matrix reserves this for
// ADMIN only).
func TestUserCannotChangeTaskTeamBoundary(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	teamA := directTeam(t, store, "Team Alpha")
	directMember(t, store, Member{
		Name: "Bound User", Email: "bound1@example.com",
		PasswordHash: mustHash(t, "userpass1"),
		SystemRole:   RoleUser,
		TeamIDs:      []bson.ObjectID{teamA.ID},
	})

	c1 := newJSONClient(srv)
	loginAs(t, c1, "bound1@example.com", "userpass1")

	createResp, err := c1.do("POST", "/api/tasks", fmt.Sprintf(`{"title":"team task","horizon":"daily","period":"2026-07-09","teamId":%q}`, teamA.ID.Hex()))
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	created := decodeJSON[TaskView](t, createResp.Body)

	resp, err := c1.do("PATCH", "/api/tasks/"+created.ID.Hex(), `{"teamId":null}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 403: %s", resp.StatusCode, body)
	}
}

// TestDeleteMemberLoginEnabledConflict covers decision #8: a login-enabled
// member cannot be hard-deleted (409, disable instead); an assignable-only
// member (never enabled login) keeps the existing hard-delete behavior.
func TestDeleteMemberLoginEnabledConflict(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Del Admin", Email: "delAdmin@example.com",
		PasswordHash: mustHash(t, "adminpass1"),
		SystemRole:   RoleAdmin,
	})
	loginMember := directMember(t, store, Member{
		Name: "Login Member", Email: "loginmember@example.com",
		PasswordHash: mustHash(t, "somepass1"),
		SystemRole:   RoleUser,
	})
	assignableOnly := directMember(t, store, Member{
		Name: "Assignable Only",
	})

	cAdmin := newJSONClient(srv)
	loginAs(t, cAdmin, "delAdmin@example.com", "adminpass1")

	t.Run("login-enabled member delete is 409", func(t *testing.T) {
		resp, err := cAdmin.do("DELETE", "/api/members/"+loginMember.ID.Hex(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 409: %s", resp.StatusCode, body)
		}
	})

	t.Run("assignable-only member delete still succeeds", func(t *testing.T) {
		resp, err := cAdmin.do("DELETE", "/api/members/"+assignableOnly.ID.Hex(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 204: %s", resp.StatusCode, body)
		}
	})
}

// TestLogoutAllowedUnderMustChangePassword covers the MINOR review finding:
// a forced-change session must be able to sign out, not just change-password.
func TestLogoutAllowedUnderMustChangePassword(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	directMember(t, store, Member{
		Name: "Forced User", Email: "forced@example.com",
		PasswordHash:       mustHash(t, "temppass1"),
		SystemRole:         RoleUser,
		MustChangePassword: true,
	})

	c := newJSONClient(srv)
	loginAs(t, c, "forced@example.com", "temppass1")

	resp, err := c.do("POST", "/api/auth/logout", "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204: %s", resp.StatusCode, body)
	}

	meResp, err := c.do("GET", "/api/auth/me", "")
	if err != nil {
		t.Fatal(err)
	}
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %d, want 401", meResp.StatusCode)
	}
}

func loginAs(t *testing.T, c *jsonClient, email, password string) {
	t.Helper()
	resp, err := c.do("POST", "/api/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, password))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login(%s) status = %d: %s", email, resp.StatusCode, body)
	}
}
