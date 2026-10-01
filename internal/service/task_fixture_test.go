package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"taskman/internal/model"
)

// tkEnv is the task-domain fixture: one team with two USERs, a second team
// neither belongs to, and an ADMIN in no team.
type tkEnv struct {
	s                  *Service
	admin, alice, bob  *model.CtxUser
	team, otherTeam    string
	asAdmin, asAlice   context.Context
	asBob, noUser      context.Context
	aliceName, bobName string
}

func tkSetup(t *testing.T) *tkEnv {
	t.Helper()
	s := newTestStore(t)
	e := &tkEnv{s: s, noUser: context.Background()}
	e.team = tkTeam(t, s, "Alpha")
	e.otherTeam = tkTeam(t, s, "Beta")
	e.admin = tkMember(t, s, "Admin", model.RoleAdmin)
	e.alice = tkMember(t, s, "Alice", model.RoleUser, e.team)
	e.bob = tkMember(t, s, "Bob", model.RoleUser, e.team)
	e.asAdmin, e.asAlice, e.asBob = asUser(e.admin), asUser(e.alice), asUser(e.bob)
	return e
}

func tkTeam(t *testing.T, s *Service, name string) string {
	t.Helper()
	tm := model.Team{ID: NewID(), Name: name, CreatedAt: time.Now()}
	if err := s.db.Create(&tm).Error; err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return tm.ID
}

func tkMember(t *testing.T, s *Service, name, role string, teams ...string) *model.CtxUser {
	t.Helper()
	m := model.Member{ID: NewID(), Name: name, SystemRole: role, CreatedAt: time.Now(),
		Email: model.NullStr(name + "@x.test"), PasswordHash: "h"}
	if err := s.db.Create(&m).Error; err != nil {
		t.Fatalf("seed member: %v", err)
	}
	for _, tid := range teams {
		if err := s.db.Exec("INSERT INTO member_teams (member_id, team_id) VALUES (?, ?)", m.ID, tid).Error; err != nil {
			t.Fatalf("seed member_teams: %v", err)
		}
	}
	return &model.CtxUser{ID: m.ID, Name: name, SystemRole: role, TeamIDs: teams}
}

func tkStr(s string) *string { return &s }

func tkToday() string { return model.CurrentPeriod(model.HorizonDaily) }

func tkThisWeek() string { return model.CurrentPeriod(model.HorizonWeekly) }

func tkDaysAgo(n int) string {
	return model.CurrentPeriodAt(model.HorizonDaily, time.Now().AddDate(0, 0, -n))
}

// tkCreate creates a daily task for today with the given mutations applied.
func tkCreate(t *testing.T, ctx context.Context, s *Service, title string, mut func(*model.Task)) *model.TaskView {
	t.Helper()
	tk := &model.Task{Title: title, Horizon: model.HorizonDaily, Period: tkToday()}
	if mut != nil {
		mut(tk)
	}
	v, err := s.CreateTask(ctx, tk)
	if err != nil {
		t.Fatalf("CreateTask %q: %v", title, err)
	}
	return v
}

func tkPatch(t *testing.T, ctx context.Context, s *Service, id, raw string) *model.TaskView {
	t.Helper()
	v, err := s.PatchTask(ctx, id, []byte(raw))
	if err != nil {
		t.Fatalf("PatchTask %s: %v", raw, err)
	}
	return v
}

// wantStatus asserts err is an *APIError with the given HTTP status (and,
// when msg != "", that exact message).
func tkWantErr(t *testing.T, err error, status int, msg string) {
	t.Helper()
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want APIError %d %q, got %v", status, msg, err)
	}
	if ae.Status != status || (msg != "" && ae.Msg != msg) {
		t.Fatalf("want %d %q, got %d %q", status, msg, ae.Status, ae.Msg)
	}
}

// tkTags seeds the tag catalog (docs/DESIGN_V11_TAG_CATALOG.md) with names
// (already normalised); a task may only carry catalog tags. Idempotent.
func tkTags(t *testing.T, s *Service, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := s.db.Exec("INSERT INTO tags (name, created_at) VALUES (?, now()) ON CONFLICT DO NOTHING", n).Error; err != nil {
			t.Fatalf("seed tag %q: %v", n, err)
		}
	}
}

func tkExec(t *testing.T, s *Service, sql string, args ...any) {
	t.Helper()
	if err := s.db.Exec(sql, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func tkIDs(vs []model.TaskView) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.ID
	}
	return out
}

func tkHas(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
