package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"taskman/internal/model"
)

// Test helpers for the member/team/session/auth suites. Prefixed mt* to
// stay clear of the task-domain suite's helpers in the same package.

func mtExec(t *testing.T, s *Service, sql string, args ...any) {
	t.Helper()
	if err := s.db.Exec(sql, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func mtCount(t *testing.T, s *Service, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.db.Raw(sql, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func mtWantAPIErr(t *testing.T, err error, status int, msg string) {
	t.Helper()
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want APIError %d %q, got %v", status, msg, err)
	}
	if ae.Status != status || ae.Msg != msg {
		t.Fatalf("want APIError %d %q, got %d %q", status, msg, ae.Status, ae.Msg)
	}
}

func mtTeam(t *testing.T, s *Service, name string) *model.Team {
	t.Helper()
	tm, err := s.CreateTeam(context.Background(), name)
	if err != nil {
		t.Fatalf("CreateTeam %q: %v", name, err)
	}
	return tm
}

func mtMember(t *testing.T, s *Service, name, email string, teamIDs ...string) *model.Member {
	t.Helper()
	m, err := s.CreateMember(context.Background(), &model.Member{Name: name, Email: model.NullStr(email), TeamIDs: teamIDs})
	if err != nil {
		t.Fatalf("CreateMember %q: %v", name, err)
	}
	return m
}

// mtLoginMember creates a login-enabled member with a known password.
func mtLoginMember(t *testing.T, s *Service, name, email, pw, role string, teamIDs ...string) *model.Member {
	t.Helper()
	m := mtMember(t, s, name, email, teamIDs...)
	hash, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	mtExec(t, s, `UPDATE members SET password_hash = ?, system_role = ? WHERE id = ?`, hash, role, m.ID)
	m.PasswordHash = model.NullStr(hash)
	m.SystemRole = role
	return m
}

// mtTask inserts a minimal task row (the task repo is another wave's).
func mtTask(t *testing.T, s *Service, teamID, assigneeID *string, status, dueDate string) string {
	t.Helper()
	id := NewID()
	var due any
	if dueDate != "" {
		due = dueDate
	}
	now := time.Now()
	mtExec(t, s, `INSERT INTO tasks (id, title, horizon, status, team_id, assignee_id, due_date, created_at, updated_at)
		VALUES (?, 'x', 'daily', ?, ?, ?, ?, ?, ?)`, id, status, teamID, assigneeID, due, now, now)
	return id
}

func mtSessionExpiry(t *testing.T, s *Service, token string) time.Time {
	t.Helper()
	var sess model.Session
	if err := s.db.Where("token = ?", token).Take(&sess).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	return sess.ExpiresAt
}
