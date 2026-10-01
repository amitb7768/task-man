package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTagsHTTP(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Alpha")
	directMember(t, store, Member{
		Name: "Ada Admin", Email: "ada@example.com",
		PasswordHash: mustHash(t, "correcthorse1"),
		SystemRole:   RoleAdmin, TeamIDs: []string{team.ID},
	})

	t.Run("GET /api/tags unauthenticated is 401", func(t *testing.T) {
		mustStatus(t, newJSONClient(srv), "GET", "/api/tags", "", http.StatusUnauthorized).Body.Close()
	})

	c := newJSONClient(srv)
	loginAs(t, c, "ada@example.com", "correcthorse1")

	t.Run("empty tags list is [] not null", func(t *testing.T) {
		resp := mustStatus(t, c, "GET", "/api/tags", "", http.StatusOK)
		defer resp.Body.Close()
		body := decodeJSON[map[string]json.RawMessage](t, resp.Body)
		if string(body["tags"]) != "[]" {
			t.Fatalf(`tags = %s, want []`, body["tags"])
		}
	})

	t.Run("task JSON always carries tags", func(t *testing.T) {
		resp := mustStatus(t, c, "POST", "/api/tasks", `{"title":"untagged","horizon":"daily","period":"`+currentPeriod(HorizonDaily)+`"}`, http.StatusCreated)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), `"tags":[]`) {
			t.Fatalf("create response lacks \"tags\":[]: %s", b)
		}
	})

	directTags(t, store, "alpha", "b")
	mustStatus(t, c, "POST", "/api/tasks", `{"title":"both","horizon":"daily","period":"`+currentPeriod(HorizonDaily)+`","tags":["Alpha","b"]}`, http.StatusCreated).Body.Close()
	mustStatus(t, c, "POST", "/api/tasks", `{"title":"only-a","horizon":"daily","period":"`+currentPeriod(HorizonDaily)+`","tags":["alpha"]}`, http.StatusCreated).Body.Close()
	mustStatus(t, c, "POST", "/api/tasks", `{"title":"team-a","horizon":"daily","period":"`+currentPeriod(HorizonDaily)+`","teamId":"`+team.ID+`","tags":["alpha"]}`, http.StatusCreated).Body.Close()
	resp := mustStatus(t, c, "POST", "/api/tasks", `{"title":"bad","horizon":"daily","period":"`+currentPeriod(HorizonDaily)+`","tags":["a b"]}`, http.StatusBadRequest)
	if e := decodeJSON[map[string]string](t, resp.Body); e["error"] != `invalid tag "a b"` {
		t.Fatalf("create invalid tag error = %q", e["error"])
	}
	resp.Body.Close()

	t.Run("search ?tag=A&tag=b normalises and ANDs", func(t *testing.T) {
		resp := mustStatus(t, c, "GET", "/api/search?tag=ALPHA&tag=b&tag=", "", http.StatusOK)
		defer resp.Body.Close()
		got := decodeJSON[struct{ Tasks []TaskView }](t, resp.Body).Tasks
		if len(got) != 1 || got[0].Title != "both" || strings.Join(got[0].Tags, ",") != "alpha,b" {
			t.Fatalf("search a+b: %+v", got)
		}
		resp2 := mustStatus(t, c, "GET", "/api/search?tag=Alpha", "", http.StatusOK)
		defer resp2.Body.Close()
		if n := len(decodeJSON[struct{ Tasks []TaskView }](t, resp2.Body).Tasks); n != 3 {
			t.Fatalf("search a: %d tasks, want 3", n)
		}
	})

	t.Run("GET /api/tags counts and teamId", func(t *testing.T) {
		resp := mustStatus(t, c, "GET", "/api/tags", "", http.StatusOK)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if want := `{"tags":[{"tag":"alpha","count":3},{"tag":"b","count":1}]}`; strings.TrimSpace(string(b)) != want {
			t.Fatalf("tags = %s, want %s", b, want)
		}
		resp2 := mustStatus(t, c, "GET", "/api/tags?teamId="+team.ID, "", http.StatusOK)
		defer resp2.Body.Close()
		b, _ = io.ReadAll(resp2.Body)
		if want := `{"tags":[{"tag":"alpha","count":1},{"tag":"b","count":0}]}`; strings.TrimSpace(string(b)) != want {
			t.Fatalf("team tags = %s, want %s", b, want)
		}
		resp3 := mustStatus(t, c, "GET", "/api/tags?teamId=nope", "", http.StatusOK)
		defer resp3.Body.Close()
		b, _ = io.ReadAll(resp3.Body)
		if want := `{"tags":[{"tag":"alpha","count":0},{"tag":"b","count":0}]}`; strings.TrimSpace(string(b)) != want {
			t.Fatalf("unknown team tags = %s, want %s", b, want)
		}
		mustStatus(t, c, "GET", "/api/tags?status=closed", "", http.StatusOK).Body.Close()
		mustStatus(t, c, "GET", "/api/tags?status=bogus", "", http.StatusBadRequest).Body.Close()
	})

	t.Run("invalid tag is 400 on every list endpoint", func(t *testing.T) {
		for _, p := range []string{
			"/api/views/day", "/api/views/week", "/api/views/month", "/api/views/attention",
			"/api/search", "/api/backlog",
			"/api/teams/" + team.ID + "/board", "/api/teams/" + team.ID + "/history",
		} {
			resp := mustStatus(t, c, "GET", p+"?tag=a%20b", "", http.StatusBadRequest)
			e := decodeJSON[map[string]string](t, resp.Body)
			resp.Body.Close()
			if e["error"] != `invalid tag "a b"` {
				t.Fatalf("%s error = %q", p, e["error"])
			}
		}
	})

	t.Run("filter reaches day view and board", func(t *testing.T) {
		resp := mustStatus(t, c, "GET", "/api/views/day?tag=b", "", http.StatusOK)
		defer resp.Body.Close()
		day := decodeJSON[struct{ Tasks []TaskView }](t, resp.Body).Tasks
		if len(day) != 1 || day[0].Title != "both" {
			t.Fatalf("day ?tag=b: %+v", day)
		}
		resp2 := mustStatus(t, c, "GET", "/api/teams/"+team.ID+"/board?tag=b", "", http.StatusOK)
		defer resp2.Body.Close()
		if b := decodeJSON[Board](t, resp2.Body); len(b.Unassigned) != 0 {
			t.Fatalf("board ?tag=b: %+v", b.Unassigned)
		}
	})
}
