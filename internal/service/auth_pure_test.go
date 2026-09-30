package service

// Pure (no-DB) tests for the auth/scope helpers that moved from server/ to
// internal/repo in wave 1.1 — relocated verbatim from server/auth_pure_test.go
// (wave 1.2), ids as strings.

import (
	"strings"
	"testing"

	"taskman/internal/model"
)

func strPtrPure(s string) *string { return &s }

func TestGenerateTempPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := generateTempPassword()
		if err != nil {
			t.Fatalf("generateTempPassword: %v", err)
		}
		if len(pw) != 12 {
			t.Fatalf("length = %d, want 12 (pw=%q)", len(pw), pw)
		}
		for _, r := range pw {
			if !strings.ContainsRune(tempPasswordChars, r) {
				t.Fatalf("char %q not in allowed charset", r)
			}
		}
		seen[pw] = true
	}
	if len(seen) < 45 { // extremely unlikely to collide this much by chance
		t.Fatalf("generated passwords look non-random: only %d unique of 50", len(seen))
	}
}

func TestGenerateSessionToken(t *testing.T) {
	tok, err := generateSessionToken()
	if err != nil {
		t.Fatalf("generateSessionToken: %v", err)
	}
	if len(tok) != 64 { // 32 bytes hex-encoded
		t.Fatalf("length = %d, want 64", len(tok))
	}
	tok2, err := generateSessionToken()
	if err != nil {
		t.Fatalf("generateSessionToken: %v", err)
	}
	if tok == tok2 {
		t.Fatalf("two calls produced the same token")
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if hash == "" || hash == "correct horse battery staple" {
		t.Fatalf("hash looks unhashed: %q", hash)
	}
	if !checkPassword(hash, "correct horse battery staple") {
		t.Fatalf("checkPassword: correct password rejected")
	}
	if checkPassword(hash, "wrong password") {
		t.Fatalf("checkPassword: wrong password accepted")
	}
	if checkPassword("", "anything") {
		t.Fatalf("checkPassword: empty hash accepted a password")
	}
}

func TestCanAccessTask(t *testing.T) {
	admin := &model.CtxUser{ID: NewID(), SystemRole: model.RoleAdmin}
	user := &model.CtxUser{ID: NewID(), SystemRole: model.RoleUser}
	otherUser := &model.CtxUser{ID: NewID(), SystemRole: model.RoleUser}
	teamA := NewID()
	teamB := NewID()
	user.TeamIDs = []string{teamA}

	personalOwnedByUser := &model.Task{OwnerID: strPtrPure(user.ID)}
	personalOwnedByOther := &model.Task{OwnerID: strPtrPure(otherUser.ID)}
	teamATask := &model.Task{TeamID: strPtrPure(teamA)}
	teamBTask := &model.Task{TeamID: strPtrPure(teamB)}

	cases := []struct {
		name string
		u    *model.CtxUser
		t    *model.Task
		want bool
	}{
		{"admin cannot see another user's personal task", admin, personalOwnedByUser, false},
		{"admin can see any team task", admin, teamBTask, true},
		{"owner can see own personal task", user, personalOwnedByUser, true},
		{"user cannot see another's personal task", user, personalOwnedByOther, false},
		{"user can see own team's task", user, teamATask, true},
		{"user cannot see other team's task", user, teamBTask, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := canAccessTask(c.u, c.t); got != c.want {
				t.Errorf("canAccessTask() = %v, want %v", got, c.want)
			}
		})
	}
}
