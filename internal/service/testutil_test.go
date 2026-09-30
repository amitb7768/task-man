package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"taskman/internal/model"
	"taskman/internal/repo"
)

// The scratch-schema helpers below (testDSN, scopedDSN, withScratchSchema)
// replicate internal/repo's migrate_test.go — package-private test helpers
// can't be imported across packages.

// testDSN returns TASKMAN_TEST_PG_DSN, skipping the test when it's unset —
// every service test runs against a real, disposable Postgres (a unique
// scratch schema per test, dropped on cleanup, never the app's own schema).
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TASKMAN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TASKMAN_TEST_PG_DSN not set; skipping Postgres-backed test")
	}
	return dsn
}

// scopedDSN returns dsn with its search_path query parameter set to schema,
// so every statement over the resulting connection (migrations included)
// resolves against that schema alone.
func scopedDSN(t *testing.T, dsn, schema string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("scopedDSN: parse %q: %v", dsn, err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// withScratchSchema creates a uniquely-named schema on dsn's database and
// drops it (CASCADE) on cleanup regardless of outcome.
func withScratchSchema(t *testing.T, dsn, tag string) string {
	t.Helper()
	admin, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Fatalf("withScratchSchema: open admin conn: %v", err)
	}
	t.Cleanup(func() { admin.Close() })

	schema := fmt.Sprintf("taskman_test_%s_%d", tag, time.Now().UnixNano())
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("withScratchSchema: create schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema)); err != nil {
			t.Errorf("withScratchSchema: drop schema %s: %v", schema, err)
		}
	})
	return schema
}

// newTestStore migrates a unique scratch schema on TASKMAN_TEST_PG_DSN and
// returns a Service bound to it. The schema is dropped (CASCADE) on cleanup.
// Skips when the DSN env is unset. This is the shared harness for every
// service test — same isolation philosophy as the Mongo suite's scratch DB
// per test.
func newTestStore(t *testing.T) *Service {
	t.Helper()
	base := testDSN(t)
	schema := withScratchSchema(t, base, "service")
	dsn := scopedDSN(t, base, schema)
	if err := repo.Migrate(dsn); err != nil {
		t.Fatalf("newTestStore: Migrate: %v", err)
	}
	db, err := repo.Open(dsn)
	if err != nil {
		t.Fatalf("newTestStore: Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("newTestStore: db.DB(): %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return New(db)
}

// asUser returns a context carrying u, mirroring what the server's session
// middleware attaches per request.
func asUser(u *model.CtxUser) context.Context {
	return model.WithUser(context.Background(), u)
}
