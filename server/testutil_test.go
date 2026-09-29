package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"taskman/internal/model"
	"taskman/internal/repo"
)

// The server suite runs against a real, disposable Postgres
// (TASKMAN_TEST_PG_DSN — e.g. docker-compose's postgres service at
// postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable).
// Every test gets its own freshly-migrated scratch schema, dropped (CASCADE)
// on cleanup — the same isolation the Mongo suite had with a scratch DB per
// test. The scratch-schema helpers below replicate internal/repo's
// testutil_test.go/migrate_test.go (package-private test helpers can't be
// imported across packages).

// testDSN returns TASKMAN_TEST_PG_DSN, skipping the test when it's unset.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TASKMAN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TASKMAN_TEST_PG_DSN not set; skipping Postgres-backed test")
	}
	return dsn
}

// scopedDSN returns dsn with its search_path set to schema, so every
// statement over the resulting connection (migrations included) resolves
// against that schema alone.
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

var testSchemaCounter int64

// withScratchSchema creates a uniquely-named schema on dsn's database and
// drops it (CASCADE) on cleanup regardless of outcome.
func withScratchSchema(t *testing.T, dsn, tag string) string {
	t.Helper()
	admin, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Fatalf("withScratchSchema: open admin conn: %v", err)
	}
	t.Cleanup(func() { admin.Close() })

	n := atomic.AddInt64(&testSchemaCounter, 1)
	schema := fmt.Sprintf("taskman_test_%s_%d_%d", tag, time.Now().UnixNano(), n)
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

// testDBs maps each test store to its gorm handle, so the direct* fixture
// helpers (and the few tests that poke rows directly) can reach the scratch
// schema without the repo exporting its db field.
var testDBs sync.Map // *Store -> *gorm.DB

func testDB(t *testing.T, store *Store) *gorm.DB {
	t.Helper()
	v, ok := testDBs.Load(store)
	if !ok {
		t.Fatalf("testDB: store was not created by newTestStore")
	}
	return v.(*gorm.DB)
}

// newTestStore migrates a fresh scratch schema and returns a Store bound
// to it; the pool is closed and the schema dropped on cleanup.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	base := testDSN(t)
	schema := withScratchSchema(t, base, "server")
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
	store := repo.New(db)
	testDBs.Store(store, db)
	t.Cleanup(func() {
		testDBs.Delete(store)
		sqlDB.Close()
	})
	return store
}

// newTestAPI wires a *api (with its own loginLimiter) around a fresh test
// store and returns it plus the store, for tests that want to seed data
// directly.
func newTestAPI(t *testing.T) (*api, *Store) {
	t.Helper()
	store := newTestStore(t)
	return &api{store: store, loginLimiter: newLoginLimiter()}, store
}

// testServer wraps a's routes with the same global middleware chain main.go
// uses, so tests exercise the real request pipeline (jsonGuard, sessionLoad,
// mustChangePasswordGate) end to end.
func testServer(a *api) *httptest.Server {
	mux := a.routes()
	handler := chain(mux, requestLog, jsonGuard, a.sessionLoad, mustChangePasswordGate)
	return httptest.NewServer(handler)
}

// jsonClient is an http.Client with a cookiejar (so it carries the session
// cookie automatically like a browser) and helpers that always set
// Content-Type: application/json, since jsonGuard requires it.
type jsonClient struct {
	c   *http.Client
	srv *httptest.Server
}

func newJSONClient(srv *httptest.Server) *jsonClient {
	jar, _ := cookiejar.New(nil)
	return &jsonClient{c: &http.Client{Jar: jar}, srv: srv}
}

func (j *jsonClient) do(method, path, body string) (*http.Response, error) {
	req, err := http.NewRequest(method, j.srv.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return j.c.Do(req)
}

// newID mints a fresh opaque id for a directly-seeded fixture row.
func newID() string { return repo.NewID() }

// directMember inserts a member row (plus its member_teams rows, in
// TeamIDs order) straight into the scratch schema, bypassing HTTP/admin
// gating, so tests can seed fixtures without depending on the endpoints
// under test.
func directMember(t *testing.T, store *Store, m Member) Member {
	t.Helper()
	if m.ID == "" {
		m.ID = newID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	db := testDB(t, store)
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed member: %v", err)
	}
	for i, tid := range m.TeamIDs {
		if err := db.Exec(`INSERT INTO member_teams (member_id, team_id, pos) VALUES (?, ?, ?)`, m.ID, tid, i).Error; err != nil {
			t.Fatalf("seed member_teams: %v", err)
		}
	}
	return m
}

func directTeam(t *testing.T, store *Store, name string) Team {
	t.Helper()
	team := Team{ID: newID(), Name: name, CreatedAt: time.Now()}
	if err := testDB(t, store).Create(&team).Error; err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return team
}

// directTask inserts a task row straight into the scratch schema, bypassing
// CreateTask's system-managed-field derivation (ownerId/weekOf/timestamps),
// so tests can seed fixtures with exactly the field values they need — e.g.
// weekof_test.go's board/rollover/history tests need specific weekOf/
// createdAt/status combinations CreateTask would never produce directly.
// Task.Activity is not a column; any entries are written to task_activity
// in slice order.
func directTask(t *testing.T, store *Store, task Task) Task {
	t.Helper()
	if task.ID == "" {
		task.ID = newID()
	}
	if task.Status == "" {
		task.Status = StatusTodo
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now()
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	db := testDB(t, store)
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	for i := range task.Activity {
		e := task.Activity[i]
		if e.ID == "" {
			e.ID = newID()
		}
		e.TaskID = task.ID
		if err := db.Create(&e).Error; err != nil {
			t.Fatalf("seed task activity: %v", err)
		}
	}
	return task
}

// Test-only stand-ins for names the server no longer defines (the
// period.go shim is gone; the note cap is unexported in internal/repo).
const dateLayout = model.DateLayout

// maxNoteLen mirrors internal/repo's (unexported) note-text rune cap.
const maxNoteLen = 4000
