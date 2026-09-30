package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"taskman/internal/model"
)

func TestAuthenticate_Matrix(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t1 := mtTeam(t, s, "t1")
	ok := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "ADMIN", t1.ID)
	mtExec(t, s, `UPDATE members SET must_change_password = TRUE WHERE id = ?`, ok.ID)
	dis := mtLoginMember(t, s, "Dis", "dis@x.com", "password1", "USER")
	mtExec(t, s, `UPDATE members SET disabled = TRUE WHERE id = ?`, dis.ID)
	mtMember(t, s, "Plain", "plain@x.com") // assignable-only: never authenticates

	cases := []struct {
		name, email, pw string
		status          int
		msg             string
	}{
		{"wrong password", "ann@x.com", "nope", 401, "invalid email or password"},
		{"no such email", "ghost@x.com", "password1", 401, "invalid email or password"},
		{"empty password", "ann@x.com", "", 401, "invalid email or password"},
		{"blank email", "   ", "password1", 401, "invalid email or password"},
		{"login not enabled", "plain@x.com", "password1", 401, "invalid email or password"},
		{"byte-exact email", "ANN@x.com", "password1", 401, "invalid email or password"},
		{"disabled wrong pw", "dis@x.com", "nope", 401, "invalid email or password"},
		{"disabled", "dis@x.com", "password1", 403, "account disabled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Authenticate(ctx, c.email, c.pw)
			mtWantAPIErr(t, err, c.status, c.msg)
		})
	}

	u, err := s.Authenticate(ctx, "  ann@x.com ", "password1")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != ok.ID || !u.MustChangePassword || u.SystemRole != "ADMIN" || !reflect.DeepEqual(u.TeamIDs, []string{t1.ID}) {
		t.Fatalf("ctx user = %+v", u)
	}
	var m model.Member
	s.db.Where("id = ?", ok.ID).Take(&m)
	if m.LastLoginAt == nil || time.Since(*m.LastLoginAt) > time.Minute {
		t.Fatalf("lastLoginAt not stamped: %v", m.LastLoginAt)
	}
}

func TestAuthenticate_SweepsExpiredSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER")
	b := mtLoginMember(t, s, "Bob", "bob@x.com", "password1", "USER")
	live, _, _ := s.CreateSession(ctx, b.ID)
	for i := 0; i < 3; i++ {
		mtExec(t, s, `INSERT INTO sessions (id, token, user_id, expires_at) VALUES (?, ?, ?, ?)`,
			NewID(), NewID(), b.ID, time.Now().Add(-time.Hour))
	}
	// A failed login does not sweep.
	s.Authenticate(ctx, "ann@x.com", "wrong")
	if n := mtCount(t, s, `SELECT count(*) FROM sessions`); n != 4 {
		t.Fatalf("sessions after failed login = %d, want 4", n)
	}
	if _, err := s.Authenticate(ctx, "ann@x.com", "password1"); err != nil {
		t.Fatal(err)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE expires_at <= now()`); n != 0 {
		t.Fatalf("expired sessions left = %d", n)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE token = ?`, live); n != 1 {
		t.Fatal("live session swept")
	}
	_ = a
}

func TestChangePassword(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER")
	mtExec(t, s, `UPDATE members SET must_change_password = TRUE WHERE id = ?`, a.ID)
	keep, _, _ := s.CreateSession(ctx, a.ID)
	other, _, _ := s.CreateSession(ctx, a.ID)

	mtWantAPIErr(t, s.ChangePassword(ctx, "ghost", "x", "newpassword", keep), 404, "member not found")
	mtWantAPIErr(t, s.ChangePassword(ctx, a.ID, "wrong", "newpassword", keep), 400, "current password is incorrect")
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE user_id = ?`, a.ID); n != 2 {
		t.Fatal("rejected change killed sessions")
	}

	if err := s.ChangePassword(ctx, a.ID, "password1", "newpassword", keep); err != nil {
		t.Fatal(err)
	}
	u, err := s.Authenticate(ctx, "ann@x.com", "newpassword")
	if err != nil {
		t.Fatal(err)
	}
	if u.MustChangePassword {
		t.Fatal("mustChangePassword not cleared")
	}
	mtWantAPIErr(t, func() error { _, err := s.Authenticate(ctx, "ann@x.com", "password1"); return err }(), 401, "invalid email or password")
	if _, _, ok := s.LoadSessionUser(ctx, keep); !ok {
		t.Fatal("kept session killed")
	}
	if _, _, ok := s.LoadSessionUser(ctx, other); ok {
		t.Fatal("other session survived")
	}
}

func TestEnableLogin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m := mtMember(t, s, "Bob", "bob@x.com")
	noEmail := mtMember(t, s, "NoMail", "")

	_, err := s.EnableLogin(ctx, m.ID, "ROOT")
	mtWantAPIErr(t, err, 400, `systemRole must be "ADMIN" or "USER"`)
	_, err = s.EnableLogin(ctx, "ghost", "USER")
	mtWantAPIErr(t, err, 404, "member not found")
	_, err = s.EnableLogin(ctx, noEmail.ID, "USER")
	mtWantAPIErr(t, err, 400, "member has no email; set one before enabling login")

	mtExec(t, s, `UPDATE members SET disabled = TRUE WHERE id = ?`, m.ID)
	temp, err := s.EnableLogin(ctx, m.ID, "USER")
	if err != nil {
		t.Fatal(err)
	}
	if len(temp) != 12 {
		t.Fatalf("temp password %q", temp)
	}
	u, err := s.Authenticate(ctx, "bob@x.com", temp)
	if err != nil {
		t.Fatalf("temp password does not authenticate (disabled not cleared?): %v", err)
	}
	if !u.MustChangePassword || u.SystemRole != "USER" {
		t.Fatalf("ctx user = %+v", u)
	}
	_, err = s.EnableLogin(ctx, m.ID, "USER")
	mtWantAPIErr(t, err, 409, "login already enabled for this member")

	// lower(email) uniqueness among login-enabled members: a case variant
	// now 409s (Mongo's byte-exact index allowed it — accepted micro-delta).
	dup := mtMember(t, s, "Bob2", "BOB@x.com")
	_, err = s.EnableLogin(ctx, dup.ID, "USER")
	mtWantAPIErr(t, err, 409, "email already in use by another login-enabled member")
}

func TestResetPassword(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	plain := mtMember(t, s, "Plain", "p@x.com")
	_, err := s.ResetPassword(ctx, plain.ID)
	mtWantAPIErr(t, err, 409, "login not enabled for this member")
	_, err = s.ResetPassword(ctx, "ghost")
	mtWantAPIErr(t, err, 404, "member not found")

	a := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER")
	tok, _, _ := s.CreateSession(ctx, a.ID)
	temp, err := s.ResetPassword(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE user_id = ?`, a.ID); n != 0 {
		t.Fatal("sessions survived reset")
	}
	_ = tok
	u, err := s.Authenticate(ctx, "ann@x.com", temp)
	if err != nil {
		t.Fatal(err)
	}
	if !u.MustChangePassword {
		t.Fatal("mustChangePassword not set by reset")
	}
}

func TestTeamsForUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t1 := mtTeam(t, s, "zeta")
	mtTeam(t, s, "other")
	t3 := mtTeam(t, s, "alpha")
	teams, err := s.TeamsForUser(ctx, &model.CtxUser{TeamIDs: []string{t3.ID, t1.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 2 || teams[0].ID != t1.ID || teams[1].ID != t3.ID {
		t.Fatalf("teams = %+v (want creation order zeta, alpha)", teams)
	}
	if teams, _ := s.TeamsForUser(ctx, &model.CtxUser{}); teams != nil {
		t.Fatal("no teams should be nil")
	}
}
