package service

import (
	"context"
	"encoding/hex"
	"reflect"
	"testing"
	"time"
)

func TestSession_Lifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t1 := mtTeam(t, s, "t1")
	m := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER", t1.ID)

	token, exp, err := s.CreateSession(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := hex.DecodeString(token); err != nil || len(b) != 32 {
		t.Fatalf("token %q is not 256-bit hex", token)
	}
	if d := time.Until(exp); d < SessionTTL-time.Minute || d > SessionTTL {
		t.Fatalf("expiry %v not ~now+TTL", exp)
	}

	// Slide: pull expiry close, load, expect it pushed back to now+TTL.
	mtExec(t, s, `UPDATE sessions SET expires_at = ? WHERE token = ?`, time.Now().Add(time.Minute), token)
	u, newExp, ok := s.LoadSessionUser(ctx, token)
	if !ok {
		t.Fatal("LoadSessionUser: not ok")
	}
	if u.ID != m.ID || u.Email != "ann@x.com" || u.SystemRole != "USER" || !reflect.DeepEqual(u.TeamIDs, []string{t1.ID}) {
		t.Fatalf("ctx user = %+v", u)
	}
	if d := time.Until(newExp); d < SessionTTL-time.Minute {
		t.Fatalf("returned expiry did not slide: %v", newExp)
	}
	if stored := mtSessionExpiry(t, s, token); stored.Sub(newExp).Abs() > time.Millisecond {
		t.Fatalf("stored expiry %v != returned %v", stored, newExp)
	}

	// Expired (code-side check; no TTL index anymore).
	mtExec(t, s, `UPDATE sessions SET expires_at = ? WHERE token = ?`, time.Now().Add(-time.Second), token)
	if _, _, ok := s.LoadSessionUser(ctx, token); ok {
		t.Fatal("expired session loaded")
	}

	token2, _, _ := s.CreateSession(ctx, m.ID)
	if err := s.DeleteSession(ctx, token2); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.LoadSessionUser(ctx, token2); ok {
		t.Fatal("deleted session loaded")
	}
	if _, _, ok := s.LoadSessionUser(ctx, "nope"); ok {
		t.Fatal("unknown token loaded")
	}
}

func TestSession_LoadRejectsDisabledAndLoginless(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER")
	token, _, _ := s.CreateSession(ctx, m.ID)
	mtExec(t, s, `UPDATE members SET disabled = TRUE WHERE id = ?`, m.ID)
	if _, _, ok := s.LoadSessionUser(ctx, token); ok {
		t.Fatal("disabled member's session loaded")
	}
	plain := mtMember(t, s, "Bob", "")
	token2, _, _ := s.CreateSession(ctx, plain.ID)
	if _, _, ok := s.LoadSessionUser(ctx, token2); ok {
		t.Fatal("login-less member's session loaded")
	}
}

func TestSession_DeleteForUserExcept(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mtLoginMember(t, s, "Ann", "ann@x.com", "password1", "USER")
	b := mtLoginMember(t, s, "Bob", "bob@x.com", "password1", "USER")
	keep, _, _ := s.CreateSession(ctx, a.ID)
	s.CreateSession(ctx, a.ID)
	s.CreateSession(ctx, a.ID)
	s.CreateSession(ctx, b.ID)
	if err := s.DeleteSessionsForUserExcept(ctx, a.ID, keep); err != nil {
		t.Fatal(err)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE user_id = ?`, a.ID); n != 1 {
		t.Fatalf("a sessions = %d, want 1", n)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions WHERE token = ?`, keep); n != 1 {
		t.Fatal("kept session gone")
	}
	if err := s.DeleteSessionsForUser(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if n := mtCount(t, s, `SELECT count(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions left = %d, want only b's", n)
	}
}
