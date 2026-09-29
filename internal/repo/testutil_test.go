package repo

import (
	"context"
	"testing"

	"taskman/internal/model"
)

// newTestStore migrates a unique scratch schema on TASKMAN_TEST_PG_DSN and
// returns a Store bound to it. The schema is dropped (CASCADE) on cleanup.
// Skips when the DSN env is unset. This is the shared harness for every
// repo test — same isolation philosophy as the Mongo suite's scratch DB per
// test.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	base := testDSN(t)
	schema := withScratchSchema(t, base, "repo")
	dsn := scopedDSN(t, base, schema)
	if err := Migrate(dsn); err != nil {
		t.Fatalf("newTestStore: Migrate: %v", err)
	}
	db, err := Open(dsn)
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
