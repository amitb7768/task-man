package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type api struct {
	store        *Store
	loginLimiter *loginLimiter
}

// routes wires every endpoint, wrapping each with requireAuth or
// requireAdmin per docs/AUTH_FEATURES.md's endpoint x role matrix.
// POST /api/auth/login is the only route left unwrapped (public); static
// asset serving (mux.Handle("/", ...)) is registered separately in main.go
// and is likewise never auth-wrapped.
func (a *api) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// ---- auth ----
	mux.HandleFunc("POST /api/auth/login", a.login) // public
	mux.HandleFunc("POST /api/auth/logout", requireAuth(a.logout))
	mux.HandleFunc("GET /api/auth/me", requireAuth(a.me))
	mux.HandleFunc("POST /api/auth/change-password", requireAuth(a.changePassword))

	// ---- tasks ----
	mux.HandleFunc("POST /api/tasks", requireAuth(a.createTask))
	mux.HandleFunc("GET /api/tasks/{id}", requireAuth(a.getTask))
	mux.HandleFunc("PATCH /api/tasks/{id}", requireAuth(a.patchTask))
	mux.HandleFunc("DELETE /api/tasks/{id}", requireAdmin(a.deleteTask))
	mux.HandleFunc("POST /api/tasks/reschedule", requireAuth(a.reschedule))
	mux.HandleFunc("POST /api/tasks/restore", requireAdmin(a.restoreTasks))

	mux.HandleFunc("GET /api/views/day", requireAuth(a.viewDay))
	mux.HandleFunc("GET /api/views/week", requireAuth(a.viewWeek))
	mux.HandleFunc("GET /api/views/month", requireAuth(a.viewMonth))
	mux.HandleFunc("GET /api/views/attention", requireAuth(a.viewAttention))

	mux.HandleFunc("GET /api/search", requireAuth(a.search))

	// ---- teams ----
	mux.HandleFunc("POST /api/teams", requireAdmin(a.createTeam))
	mux.HandleFunc("GET /api/teams", requireAuth(a.listTeams))
	mux.HandleFunc("PATCH /api/teams/{id}", requireAdmin(a.patchTeam))
	mux.HandleFunc("DELETE /api/teams/{id}", requireAdmin(a.deleteTeam))
	mux.HandleFunc("GET /api/teams/{id}/board", requireAuth(a.teamBoard))
	mux.HandleFunc("GET /api/teams/{id}/rollover", requireAdmin(a.teamRollover))
	mux.HandleFunc("GET /api/teams/{id}/history", requireAuth(a.teamHistory))

	// ---- members (ADMIN only, all of it) ----
	mux.HandleFunc("POST /api/members", requireAdmin(a.createMember))
	mux.HandleFunc("GET /api/members", requireAdmin(a.listMembers))
	mux.HandleFunc("PATCH /api/members/{id}", requireAdmin(a.patchMember))
	mux.HandleFunc("DELETE /api/members/{id}", requireAdmin(a.deleteMember))
	mux.HandleFunc("POST /api/members/{id}/enable-login", requireAdmin(a.enableLogin))
	mux.HandleFunc("POST /api/members/{id}/reset-password", requireAdmin(a.resetPassword))

	return mux
}

// ---- helpers ----

func pathID(r *http.Request) (bson.ObjectID, error) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		return bson.ObjectID{}, badRequest("invalid id")
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.status, map[string]string{"error": ae.msg})
		return
	}
	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

// orEmpty turns a nil slice into an empty (but non-null) one for JSON output.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ---- tasks ----

func (a *api) createTask(w http.ResponseWriter, r *http.Request) {
	var t Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	view, err := a.store.CreateTask(r.Context(), &t)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *api) getTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	detail, err := a.store.GetTaskDetail(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	detail.Children = orEmpty(detail.Children)
	writeJSON(w, http.StatusOK, detail)
}

func (a *api) patchTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, badRequest("invalid body"))
		return
	}
	view, err := a.store.PatchTask(r.Context(), id, raw)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *api) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	deleted, err := a.store.DeleteTaskCascade(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": orEmpty(deleted)})
}

func (a *api) restoreTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	n, err := a.store.RestoreTasks(r.Context(), body.Tasks)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"restored": n})
}

func (a *api) reschedule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	ids := make([]bson.ObjectID, 0, len(body.IDs))
	for _, s := range body.IDs {
		oid, err := bson.ObjectIDFromHex(s)
		if err != nil {
			writeErr(w, badRequest("invalid id %q", s))
			return
		}
		ids = append(ids, oid)
	}
	n, err := a.store.Reschedule(r.Context(), ids)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

// ---- views ----

func (a *api) viewDay(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = currentPeriod(HorizonDaily)
	}
	tasks, weekCtx, err := a.store.ViewDay(r.Context(), date)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":       orEmpty(tasks),
		"weekContext": orEmpty(weekCtx),
	})
}

func (a *api) viewWeek(w http.ResponseWriter, r *http.Request) {
	week := r.URL.Query().Get("week")
	if week == "" {
		week = currentPeriod(HorizonWeekly)
	}
	tasks, days, monthCtx, err := a.store.ViewWeek(r.Context(), week)
	if err != nil {
		writeErr(w, err)
		return
	}
	daysOut := map[string]any{}
	for k, v := range days {
		daysOut[k] = orEmpty(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":        orEmpty(tasks),
		"days":         daysOut,
		"monthContext": orEmpty(monthCtx),
	})
}

func (a *api) viewMonth(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = currentPeriod(HorizonMonthly)
	}
	tasks, weeks, err := a.store.ViewMonth(r.Context(), month)
	if err != nil {
		writeErr(w, err)
		return
	}
	weeksOut := map[string]any{}
	for k, v := range weeks {
		weeksOut[k] = orEmpty(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": orEmpty(tasks),
		"weeks": weeksOut,
	})
}

func (a *api) viewAttention(w http.ResponseWriter, r *http.Request) {
	overdue, slipped, err := a.store.ViewAttention(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"overdue": orEmpty(overdue),
		"slipped": orEmpty(slipped),
	})
}

// ---- search ----

func (a *api) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := SearchParams{
		Q:        q.Get("q"),
		Status:   q.Get("status"),
		Priority: q.Get("priority"),
		Horizon:  q.Get("horizon"),
		Overdue:  q.Get("overdue") == "true",
	}
	if v := q.Get("teamId"); v != "" {
		oid, err := bson.ObjectIDFromHex(v)
		if err != nil {
			writeErr(w, badRequest("invalid teamId"))
			return
		}
		p.TeamID = &oid
	}
	if v := q.Get("assigneeId"); v != "" {
		oid, err := bson.ObjectIDFromHex(v)
		if err != nil {
			writeErr(w, badRequest("invalid assigneeId"))
			return
		}
		p.AssigneeID = &oid
	}
	tasks, err := a.store.Search(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": orEmpty(tasks)})
}

// ---- teams ----

func (a *api) createTeam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	team, err := a.store.CreateTeam(r.Context(), body.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (a *api) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := a.store.ListTeamsWithCounts(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(teams))
}

func (a *api) patchTeam(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	team, err := a.store.PatchTeam(r.Context(), id, body.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (a *api) deleteTeam(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.store.DeleteTeam(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) teamBoard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	board, err := a.store.TeamBoard(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, board)
}

func (a *api) teamRollover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	week, tasks, err := a.store.TeamRollover(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"week":  week,
		"tasks": orEmpty(tasks),
	})
}

func (a *api) teamHistory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	offset, limit := 0, defaultHistoryLimit
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, badRequest("invalid offset"))
			return
		}
		offset = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, badRequest("invalid limit"))
			return
		}
		limit = n
	}
	tasks, hasMore, err := a.store.TeamHistory(r.Context(), id, offset, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":   orEmpty(tasks),
		"hasMore": hasMore,
	})
}

// ---- members ----

func (a *api) createMember(w http.ResponseWriter, r *http.Request) {
	var m Member
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	created, err := a.store.CreateMember(r.Context(), &m)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *api) listMembers(w http.ResponseWriter, r *http.Request) {
	var teamID *bson.ObjectID
	if v := r.URL.Query().Get("teamId"); v != "" {
		oid, err := bson.ObjectIDFromHex(v)
		if err != nil {
			writeErr(w, badRequest("invalid teamId"))
			return
		}
		teamID = &oid
	}
	members, err := a.store.ListMembers(r.Context(), teamID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(members))
}

func (a *api) patchMember(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, badRequest("invalid body"))
		return
	}
	m, err := a.store.PatchMember(r.Context(), id, raw)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (a *api) deleteMember(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.store.DeleteMember(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
