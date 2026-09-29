package repo

import (
	"context"
	"testing"
)

func TestSeedAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("no ADMIN_EMAIL is a no-op", func(t *testing.T) {
		s := newTestStore(t)
		t.Setenv("ADMIN_EMAIL", "")
		if err := SeedAdmin(ctx, s); err != nil {
			t.Fatal(err)
		}
		if n := mtCount(t, s, `SELECT count(*) FROM members`); n != 0 {
			t.Fatal("seeded without ADMIN_EMAIL")
		}
	})

	t.Run("creates admin, idempotent", func(t *testing.T) {
		s := newTestStore(t)
		t.Setenv("ADMIN_EMAIL", " boss@x.com ")
		t.Setenv("ADMIN_PASSWORD", "password1")
		if err := SeedAdmin(ctx, s); err != nil {
			t.Fatal(err)
		}
		if err := SeedAdmin(ctx, s); err != nil {
			t.Fatal(err)
		}
		if n := mtCount(t, s, `SELECT count(*) FROM members`); n != 1 {
			t.Fatalf("members = %d, want 1", n)
		}
		u, err := s.Authenticate(ctx, "boss@x.com", "password1")
		if err != nil {
			t.Fatal(err)
		}
		if u.SystemRole != "ADMIN" || !u.MustChangePassword || u.Name != "Admin" {
			t.Fatalf("seeded user = %+v", u)
		}
	})

	t.Run("upgrades existing assignable-only member", func(t *testing.T) {
		s := newTestStore(t)
		m := mtMember(t, s, "Boss", "boss@x.com")
		mtExec(t, s, `UPDATE members SET disabled = TRUE WHERE id = ?`, m.ID)
		t.Setenv("ADMIN_EMAIL", "boss@x.com")
		t.Setenv("ADMIN_PASSWORD", "")
		if err := SeedAdmin(ctx, s); err != nil {
			t.Fatal(err)
		}
		if n := mtCount(t, s, `SELECT count(*) FROM members`); n != 1 {
			t.Fatal("duplicate person created")
		}
		if n := mtCount(t, s, `SELECT count(*) FROM members WHERE id = ? AND password_hash IS NOT NULL
			AND system_role = 'ADMIN' AND must_change_password AND NOT disabled`, m.ID); n != 1 {
			t.Fatal("existing member not upgraded")
		}
	})
}
