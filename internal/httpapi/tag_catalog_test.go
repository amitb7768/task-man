package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// v11 tag catalog routes (docs/DESIGN_V11_TAG_CATALOG.md).
func TestTagCatalogHTTP(t *testing.T) {
	a, store := newTestAPI(t)
	srv := testServer(a)
	defer srv.Close()

	team := directTeam(t, store, "Alpha")
	admin := directMember(t, store, Member{
		Name: "Ada Admin", Email: "ada@example.com",
		PasswordHash: mustHash(t, "correcthorse1"),
		SystemRole:   RoleAdmin, TeamIDs: []string{team.ID},
	})
	directMember(t, store, Member{
		Name: "Uma User", Email: "uma@example.com",
		PasswordHash: mustHash(t, "correcthorse2"),
		SystemRole:   RoleUser, TeamIDs: []string{team.ID},
	})

	errMsg := func(t *testing.T, resp *http.Response) string {
		t.Helper()
		defer resp.Body.Close()
		return decodeJSON[map[string]string](t, resp.Body)["error"]
	}

	t.Run("unauthenticated is 401", func(t *testing.T) {
		anon := newJSONClient(srv)
		mustStatus(t, anon, "GET", "/api/tags/catalog", "", http.StatusUnauthorized).Body.Close()
		mustStatus(t, anon, "POST", "/api/tags/catalog", `{"name":"x"}`, http.StatusUnauthorized).Body.Close()
		mustStatus(t, anon, "DELETE", "/api/tags/catalog/x", "", http.StatusUnauthorized).Body.Close()
	})

	ac := newJSONClient(srv)
	loginAs(t, ac, "ada@example.com", "correcthorse1")
	uc := newJSONClient(srv)
	loginAs(t, uc, "uma@example.com", "correcthorse2")

	t.Run("empty catalog is [] not null", func(t *testing.T) {
		resp := mustStatus(t, uc, "GET", "/api/tags/catalog", "", http.StatusOK)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if strings.TrimSpace(string(b)) != `{"tags":[]}` {
			t.Fatalf("catalog = %s", b)
		}
	})

	t.Run("USER cannot create or delete", func(t *testing.T) {
		if m := errMsg(t, mustStatus(t, uc, "POST", "/api/tags/catalog", `{"name":"x"}`, http.StatusForbidden)); m != "admin only" {
			t.Fatalf("POST as USER error = %q", m)
		}
		if m := errMsg(t, mustStatus(t, uc, "DELETE", "/api/tags/catalog/x", "", http.StatusForbidden)); m != "admin only" {
			t.Fatalf("DELETE as USER error = %q", m)
		}
	})

	t.Run("ADMIN create: 201 shape, normalised, 409, 400", func(t *testing.T) {
		resp := mustStatus(t, ac, "POST", "/api/tags/catalog", `{"name":"  Ops "}`, http.StatusCreated)
		defer resp.Body.Close()
		got := decodeJSON[map[string]json.RawMessage](t, resp.Body)
		if string(got["name"]) != `"ops"` || string(got["createdBy"]) != `"`+admin.ID+`"` {
			t.Fatalf("created = %v", got)
		}
		var at time.Time
		if err := json.Unmarshal(got["createdAt"], &at); err != nil || at.IsZero() {
			t.Fatalf("createdAt = %s (%v)", got["createdAt"], err)
		}
		if m := errMsg(t, mustStatus(t, ac, "POST", "/api/tags/catalog", `{"name":"OPS"}`, http.StatusConflict)); m != `tag "ops" already exists` {
			t.Fatalf("dup error = %q", m)
		}
		if m := errMsg(t, mustStatus(t, ac, "POST", "/api/tags/catalog", `{"name":"a b"}`, http.StatusBadRequest)); m != `invalid tag "a b"` {
			t.Fatalf("invalid error = %q", m)
		}
		if m := errMsg(t, mustStatus(t, ac, "POST", "/api/tags/catalog", `{"name":""}`, http.StatusBadRequest)); m != "name is required" {
			t.Fatalf("empty error = %q", m)
		}
		mustStatus(t, ac, "POST", "/api/tags/catalog", `{`, http.StatusBadRequest).Body.Close()
		mustStatus(t, ac, "POST", "/api/tags/catalog", `{"name":"ci/cd"}`, http.StatusCreated).Body.Close()
		mustStatus(t, ac, "POST", "/api/tags/catalog", `{"name":"api"}`, http.StatusCreated).Body.Close()
	})

	t.Run("catalog list is name asc, visible to USER", func(t *testing.T) {
		resp := mustStatus(t, uc, "GET", "/api/tags/catalog", "", http.StatusOK)
		defer resp.Body.Close()
		got := decodeJSON[struct {
			Tags []struct {
				Name      string  `json:"name"`
				CreatedBy *string `json:"createdBy"`
			} `json:"tags"`
		}](t, resp.Body).Tags
		var names []string
		for _, g := range got {
			names = append(names, g.Name)
		}
		if strings.Join(names, ",") != "api,ci/cd,ops" {
			t.Fatalf("catalog names = %v", names)
		}
	})

	day := currentPeriod(HorizonDaily)
	t.Run("task writes accept only catalog tags", func(t *testing.T) {
		if m := errMsg(t, mustStatus(t, uc, "POST", "/api/tasks", `{"title":"x","horizon":"daily","period":"`+day+`","tags":["zzz"]}`, http.StatusBadRequest)); m != `unknown tag "zzz"` {
			t.Fatalf("create unknown error = %q", m)
		}
		resp := mustStatus(t, uc, "POST", "/api/tasks", `{"title":"x","horizon":"daily","period":"`+day+`","tags":["Ops"]}`, http.StatusCreated)
		id := decodeJSON[TaskView](t, resp.Body).ID
		resp.Body.Close()
		if m := errMsg(t, mustStatus(t, uc, "PATCH", "/api/tasks/"+id, `{"tags":["ops","nah"]}`, http.StatusBadRequest)); m != `unknown tag "nah"` {
			t.Fatalf("patch unknown error = %q", m)
		}
	})

	t.Run("GET /api/tags lists every catalog tag with count 0", func(t *testing.T) {
		resp := mustStatus(t, uc, "GET", "/api/tags", "", http.StatusOK)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		want := `{"tags":[{"tag":"api","count":0},{"tag":"ci/cd","count":0},{"tag":"ops","count":1}]}`
		if strings.TrimSpace(string(b)) != want {
			t.Fatalf("tags = %s, want %s", b, want)
		}
	})

	t.Run("ADMIN delete: 409 in use, 404 unknown, 204", func(t *testing.T) {
		if m := errMsg(t, mustStatus(t, ac, "DELETE", "/api/tags/catalog/ops", "", http.StatusConflict)); m != `tag "ops" is in use by 1 tasks` {
			t.Fatalf("in-use error = %q", m)
		}
		mustStatus(t, ac, "DELETE", "/api/tags/catalog/ghost", "", http.StatusNotFound).Body.Close()
		mustStatus(t, ac, "DELETE", "/api/tags/catalog/api", "", http.StatusNoContent).Body.Close()
		mustStatus(t, ac, "DELETE", "/api/tags/catalog/ci%2Fcd", "", http.StatusNoContent).Body.Close()
		mustStatus(t, ac, "DELETE", "/api/tags/catalog/api", "", http.StatusNotFound).Body.Close()
		resp := mustStatus(t, ac, "GET", "/api/tags/catalog", "", http.StatusOK)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), `"name":"ops"`) || strings.Contains(string(b), `"api"`) || strings.Contains(string(b), `ci/cd`) {
			t.Fatalf("catalog after delete = %s", b)
		}
	})
}
