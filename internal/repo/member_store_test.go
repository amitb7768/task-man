package repo

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"taskman/internal/model"
)

func TestMembers_TeamIDsJoinRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t1 := mtTeam(t, s, "t1")
	t2 := mtTeam(t, s, "t2")
	t3 := mtTeam(t, s, "t3")

	_, err := s.CreateMember(ctx, &model.Member{Name: " "})
	mtWantAPIErr(t, err, 400, "name is required")
	_, err = s.CreateMember(ctx, &model.Member{Name: "X", TeamIDs: []string{t1.ID, "bogus"}})
	mtWantAPIErr(t, err, 400, "team not found: bogus")
	if n := mtCount(t, s, `SELECT count(*) FROM members`); n != 0 {
		t.Fatal("failed create left a row")
	}

	// Written order is the read order (Mongo array order).
	zed := mtMember(t, s, "zed", "", t3.ID, t1.ID, t2.ID)
	amy := mtMember(t, s, "Amy", "", t2.ID)
	bob := mtMember(t, s, "bob", "")

	all, err := s.ListMembers(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := []string{all[0].Name, all[1].Name, all[2].Name}; !reflect.DeepEqual(names, []string{"Amy", "bob", "zed"}) {
		t.Fatalf("order = %v", names)
	}
	if !reflect.DeepEqual(all[2].TeamIDs, []string{t3.ID, t1.ID, t2.ID}) || all[1].TeamIDs != nil {
		t.Fatalf("teamIds = %v / %v", all[2].TeamIDs, all[1].TeamIDs)
	}

	inT2, _ := s.ListMembers(ctx, &t2.ID)
	if len(inT2) != 2 || inT2[0].ID != amy.ID || inT2[1].ID != zed.ID {
		t.Fatalf("team filter = %+v", inT2)
	}

	// Patch without teamIds keeps them; patch with reorders/replaces them.
	p, err := s.PatchMember(ctx, zed.ID, []byte(`{"role":"eng"}`))
	if err != nil || p.Role != "eng" || !reflect.DeepEqual(p.TeamIDs, []string{t3.ID, t1.ID, t2.ID}) {
		t.Fatalf("patch role = %+v, %v", p, err)
	}
	p, err = s.PatchMember(ctx, zed.ID, []byte(`{"teamIds":["`+t2.ID+`","`+t3.ID+`"]}`))
	if err != nil || !reflect.DeepEqual(p.TeamIDs, []string{t2.ID, t3.ID}) {
		t.Fatalf("patch teams = %+v, %v", p, err)
	}
	all, _ = s.ListMembers(ctx, nil)
	if !reflect.DeepEqual(all[2].TeamIDs, []string{t2.ID, t3.ID}) {
		t.Fatalf("reloaded teamIds = %v", all[2].TeamIDs)
	}
	_, err = s.PatchMember(ctx, zed.ID, []byte(`{"teamIds":["nope"]}`))
	mtWantAPIErr(t, err, 400, "team not found: nope")
	p, _ = s.PatchMember(ctx, zed.ID, []byte(`{"teamIds":[]}`))
	if len(p.TeamIDs) != 0 || mtCount(t, s, `SELECT count(*) FROM member_teams WHERE member_id = ?`, zed.ID) != 0 {
		t.Fatal("empty teamIds did not clear join rows")
	}
	_ = bob
}

func TestPatchMember_Validation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER")
	tok, _, _ := s.CreateSession(ctx, m.ID)

	_, err := s.PatchMember(ctx, "ghost", []byte(`{}`))
	mtWantAPIErr(t, err, 404, "member not found")
	_, err = s.PatchMember(ctx, m.ID, []byte(`{`))
	if ae, ok := err.(*APIError); !ok || ae.Status != 400 || !strings.HasPrefix(ae.Msg, "invalid JSON: ") {
		t.Fatalf("bad JSON err = %v", err)
	}
	_, err = s.PatchMember(ctx, m.ID, []byte(`{"name":""}`))
	mtWantAPIErr(t, err, 400, "name is required")
	_, err = s.PatchMember(ctx, m.ID, []byte(`{"systemRole":"ROOT"}`))
	mtWantAPIErr(t, err, 400, `invalid systemRole "ROOT"`)

	// passwordHash is json:"-": a patch can't touch it; disabling kills sessions.
	p, err := s.PatchMember(ctx, m.ID, []byte(`{"disabled":true,"passwordHash":"x"}`))
	if err != nil || !p.Disabled {
		t.Fatalf("disable = %+v, %v", p, err)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE token = ?`, tok); n != 0 {
		t.Fatal("disable did not kill sessions")
	}
	if n := mtCount(t, s, `SELECT count(*) FROM members WHERE id = ? AND password_hash LIKE '$2%'`, m.ID); n != 1 {
		t.Fatal("password hash lost or overwritten by patch")
	}
	// Clearing email writes NULL, never ''.
	if _, err := s.PatchMember(ctx, m.ID, []byte(`{"email":""}`)); err != nil {
		t.Fatal(err)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM members WHERE id = ? AND email IS NULL`, m.ID); n != 1 {
		t.Fatal("empty email not stored as NULL")
	}

	// CreateMember with an out-of-CHECK systemRole: 400, not a raw 23514.
	_, err = s.CreateMember(ctx, &model.Member{Name: "X", SystemRole: "ROOT"})
	mtWantAPIErr(t, err, 400, `invalid systemRole "ROOT"`)
}

func TestMembers_EmailConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Assignable-only members may share an email (partial index).
	mtMember(t, s, "a1", "same@x.com")
	mtMember(t, s, "a2", "same@x.com")

	hash, _ := hashPassword("password1")
	_, err := s.CreateMember(ctx, &model.Member{Name: "L1", Email: "Dup@x.com", PasswordHash: model.NullStr(hash)})
	if err != nil {
		t.Fatal(err)
	}
	// Case variant collides now (lower(email) index) — accepted micro-delta.
	_, err = s.CreateMember(ctx, &model.Member{Name: "L2", Email: "dup@x.com", PasswordHash: model.NullStr(hash)})
	mtWantAPIErr(t, err, 409, "email already in use")

	l3 := mtLoginMember(t, s, "L3", "l3@x.com", "password1", "USER")
	_, err = s.PatchMember(ctx, l3.ID, []byte(`{"email":"DUP@x.com"}`))
	mtWantAPIErr(t, err, 409, "email already in use")
}

func TestDeleteMember(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	team := mtTeam(t, s, "t")

	login := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER")
	mtWantAPIErr(t, s.DeleteMember(ctx, login.ID), 409, "member has login enabled; disable instead of deleting")
	mtWantAPIErr(t, s.DeleteMember(ctx, "ghost"), 404, "member not found")

	plain := mtMember(t, s, "Bob", "", team.ID)
	task := mtTask(t, s, &team.ID, &plain.ID, model.StatusTodo, "")
	mtExec(t, s, `INSERT INTO sessions (id, token, user_id, expires_at) VALUES (?, ?, ?, ?)`,
		NewID(), NewID(), plain.ID, time.Now().Add(time.Hour))
	mtExec(t, s, `INSERT INTO task_activity (id, task_id, kind, date, at, by_id, by_name, text)
		VALUES (?, ?, 'note', '2026-01-01', now(), ?, 'Bob', 'hi')`, NewID(), task, plain.ID)

	if err := s.DeleteMember(ctx, plain.ID); err != nil {
		t.Fatal(err)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM tasks WHERE id = ? AND assignee_id IS NULL`, task); n != 1 {
		t.Fatal("assignee_id not SET NULL")
	}
	if n := mtCount(t, s, `SELECT count(*) FROM member_teams WHERE member_id = ?`, plain.ID); n != 0 {
		t.Fatal("member_teams not cascaded")
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE user_id = ?`, plain.ID); n != 0 {
		t.Fatal("sessions not cascaded")
	}
	if n := mtCount(t, s, `SELECT count(*) FROM task_activity WHERE task_id = ? AND by_id IS NULL AND by_name = 'Bob'`, task); n != 1 {
		t.Fatal("activity by_id not SET NULL (or byName lost)")
	}
}
