package repo

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"taskman/internal/model"
)

// testDSN returns TASKMAN_TEST_PG_DSN, skipping the test when it's unset —
// this whole file only runs against a real, disposable Postgres (see
// docs/DESIGN_PG_FSM_MIGRATION.md wave 1.0/1.2: a unique scratch schema per
// test/run, dropped on cleanup, never the app's own schema).
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TASKMAN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TASKMAN_TEST_PG_DSN not set; skipping Postgres-backed test")
	}
	return dsn
}

// scopedDSN returns dsn with its search_path query parameter set to schema,
// so every statement over the resulting connection — including
// golang-migrate's own schema_migrations bookkeeping table — resolves
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

// withScratchSchema creates a uniquely-named schema on dsn's database, hands
// its name to fn, and drops it (CASCADE) on cleanup regardless of outcome.
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

// wantTables is every table the migrations create (0001_init + 0003_tag_catalog).
var wantTables = []string{"teams", "members", "member_teams", "tasks", "task_activity", "sessions", "tags"}

func assertTablesExist(t *testing.T, dsn, schema string) {
	t.Helper()
	db, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Fatalf("assertTablesExist: open: %v", err)
	}
	defer db.Close()

	for _, tbl := range wantTables {
		var exists bool
		const q = `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`
		if err := db.QueryRow(q, schema, tbl).Scan(&exists); err != nil {
			t.Fatalf("assertTablesExist: query %s.%s: %v", schema, tbl, err)
		}
		if !exists {
			t.Errorf("table %s.%s does not exist after Migrate", schema, tbl)
		}
	}
}

// TestMigrateTwoScratchSchemas proves Migrate is schema-scoped: two
// independently-migrated scratch schemas on the same database, neither
// colliding with the other's tables or golang-migrate version row.
func TestMigrateTwoScratchSchemas(t *testing.T) {
	dsn := testDSN(t)

	schemaA := withScratchSchema(t, dsn, "a")
	schemaB := withScratchSchema(t, dsn, "b")

	if err := Migrate(scopedDSN(t, dsn, schemaA)); err != nil {
		t.Fatalf("Migrate(schema=%s): %v", schemaA, err)
	}
	if err := Migrate(scopedDSN(t, dsn, schemaB)); err != nil {
		t.Fatalf("Migrate(schema=%s): %v", schemaB, err)
	}

	assertTablesExist(t, dsn, schemaA)
	assertTablesExist(t, dsn, schemaB)

	// Re-running Migrate against an already-migrated schema must be a
	// no-op (ErrNoChange), not an error.
	if err := Migrate(scopedDSN(t, dsn, schemaA)); err != nil {
		t.Errorf("Migrate(schema=%s) second run: %v", schemaA, err)
	}
}

// TestRecurrenceNullRoundTrip is the wave-1.0 hard requirement: a nil
// *model.Recurrence MUST persist as SQL NULL on tasks.recurrence (never the
// jsonb literal 'null'), and a non-nil one must round-trip exactly.
func TestRecurrenceNullRoundTrip(t *testing.T) {
	dsn := testDSN(t)
	schema := withScratchSchema(t, dsn, "recur")
	dsn = scopedDSN(t, dsn, schema)

	if err := Migrate(dsn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("gdb.DB(): %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("nil recurrence persists as SQL NULL", func(t *testing.T) {
		task := &model.Task{
			ID:        "task-nil-recurrence",
			Title:     "no recurrence",
			Horizon:   model.HorizonDaily,
			Status:    model.StatusTodo,
			CreatedAt: now,
			UpdatedAt: now,
			// Notes/DueDate/WeekOf left "" — exercises NullStr's Value() at
			// the same time (each must persist as SQL NULL too).
		}
		if err := gdb.Create(task).Error; err != nil {
			t.Fatalf("Create: %v", err)
		}

		// Assert the raw column is SQL NULL, not the jsonb literal 'null'
		// (jsonb 'null' would render as the 4-byte text "null", not a NULL
		// column — sql.NullString.Valid would still be true in that case).
		var raw sql.NullString
		if err := gdb.Raw(`SELECT recurrence::text FROM tasks WHERE id = ?`, task.ID).Scan(&raw).Error; err != nil {
			t.Fatalf("select raw recurrence: %v", err)
		}
		if raw.Valid {
			t.Errorf("tasks.recurrence = %q, want SQL NULL", raw.String)
		}
		var rawNotes sql.NullString
		if err := gdb.Raw(`SELECT notes FROM tasks WHERE id = ?`, task.ID).Scan(&rawNotes).Error; err != nil {
			t.Fatalf("select raw notes: %v", err)
		}
		if rawNotes.Valid {
			t.Errorf("tasks.notes = %q, want SQL NULL (NullStr(\"\") did not persist as NULL)", rawNotes.String)
		}

		var got model.Task
		if err := gdb.First(&got, "id = ?", task.ID).Error; err != nil {
			t.Fatalf("First: %v", err)
		}
		if got.Recurrence != nil {
			t.Errorf("got.Recurrence = %+v, want nil", got.Recurrence)
		}
		if got.Notes != "" {
			t.Errorf("got.Notes = %q, want \"\" (NULL scanned back to non-empty)", got.Notes)
		}
	})

	t.Run("non-nil recurrence round-trips exactly", func(t *testing.T) {
		interval := 2
		want := &model.Recurrence{
			Freq:     model.FreqDaily,
			Interval: &interval,
			Anchor:   "2026-01-01",
		}
		task := &model.Task{
			ID:         "task-with-recurrence",
			Title:      "has recurrence",
			Horizon:    model.HorizonDaily,
			Status:     model.StatusTodo,
			CreatedAt:  now,
			UpdatedAt:  now,
			Recurrence: want,
		}
		if err := gdb.Create(task).Error; err != nil {
			t.Fatalf("Create: %v", err)
		}

		var raw sql.NullString
		if err := gdb.Raw(`SELECT recurrence::text FROM tasks WHERE id = ?`, task.ID).Scan(&raw).Error; err != nil {
			t.Fatalf("select raw recurrence: %v", err)
		}
		if !raw.Valid {
			t.Fatalf("tasks.recurrence = SQL NULL, want a jsonb value")
		}

		var got model.Task
		if err := gdb.First(&got, "id = ?", task.ID).Error; err != nil {
			t.Fatalf("First: %v", err)
		}
		if got.Recurrence == nil {
			t.Fatalf("got.Recurrence = nil, want %+v", want)
		}
		if got.Recurrence.Freq != want.Freq ||
			got.Recurrence.Anchor != want.Anchor ||
			got.Recurrence.Interval == nil || *got.Recurrence.Interval != *want.Interval {
			t.Errorf("got.Recurrence = %+v, want %+v", got.Recurrence, want)
		}
	})

	t.Run("nil tags persist as jsonb [] (0002_tags)", func(t *testing.T) {
		task := &model.Task{
			ID:        "task-nil-tags",
			Title:     "no tags",
			Horizon:   model.HorizonDaily,
			Status:    model.StatusTodo,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := gdb.Create(task).Error; err != nil {
			t.Fatalf("Create: %v", err)
		}
		var raw string
		if err := gdb.Raw(`SELECT tags::text FROM tasks WHERE id = ?`, task.ID).Scan(&raw).Error; err != nil {
			t.Fatalf("select raw tags: %v", err)
		}
		if raw != "[]" {
			t.Fatalf("tasks.tags = %q, want []", raw)
		}
		task.ID, task.Tags = "task-tags", model.Tags{"a", "b"}
		if err := gdb.Create(task).Error; err != nil {
			t.Fatalf("Create tagged: %v", err)
		}
		var got model.Task
		if err := gdb.First(&got, "id = ?", task.ID).Error; err != nil {
			t.Fatalf("First: %v", err)
		}
		if len(got.Tags) != 2 || got.Tags[0] != "a" || got.Tags[1] != "b" {
			t.Fatalf("tags round-trip = %#v", got.Tags)
		}
	})

	t.Run("task_activity.seq is never sent on insert", func(t *testing.T) {
		task := &model.Task{
			ID:        "task-for-activity",
			Title:     "activity host",
			Horizon:   model.HorizonDaily,
			Status:    model.StatusTodo,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := gdb.Create(task).Error; err != nil {
			t.Fatalf("Create task: %v", err)
		}

		entries := []*model.ActivityEntry{
			{ID: "act-1", TaskID: task.ID, Kind: model.ActivityNote, Date: "2026-09-29", At: now, Text: "first"},
			{ID: "act-2", TaskID: task.ID, Kind: model.ActivityNote, Date: "2026-09-29", At: now, Text: "second"},
		}
		for _, e := range entries {
			if err := gdb.Create(e).Error; err != nil {
				t.Fatalf("Create activity %s: %v", e.ID, err)
			}
			if e.Seq == 0 {
				t.Errorf("activity %s: Seq not populated after Create (GENERATED ALWAYS AS IDENTITY column wasn't read back)", e.ID)
			}
		}

		var got []model.ActivityEntry
		if err := gdb.Where("task_id = ?", task.ID).Order("seq").Find(&got).Error; err != nil {
			t.Fatalf("Find activity: %v", err)
		}
		if len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
			t.Errorf("activity rows out of order or missing: %+v", got)
		}
		if got[0].Seq >= got[1].Seq {
			t.Errorf("Seq not increasing: %d >= %d", got[0].Seq, got[1].Seq)
		}
	})
}

// migrateTo applies the embedded migrations up to (and including) version.
func migrateTo(t *testing.T, dsn string, version uint) {
	t.Helper()
	db, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Fatalf("migrateTo: open: %v", err)
	}
	defer db.Close()
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		t.Fatalf("migrateTo: driver: %v", err)
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("migrateTo: source: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx/v5", driver)
	if err != nil {
		t.Fatalf("migrateTo: init: %v", err)
	}
	defer m.Close()
	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrateTo(%d): %v", version, err)
	}
}

// TestTagCatalogSeed proves 0003_tag_catalog seeds the catalog from the tags
// tasks already carry (docs/DESIGN_V11_TAG_CATALOG.md decision #2), so no
// existing task becomes invalid; and that the seed statement is idempotent.
func TestTagCatalogSeed(t *testing.T) {
	base := testDSN(t)
	schema := withScratchSchema(t, base, "tagseed")
	dsn := scopedDSN(t, base, schema)

	migrateTo(t, dsn, 2)
	db, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, tags := range []string{`["backend","ui"]`, `["ui","ops"]`, `[]`} {
		if _, err := db.Exec(`INSERT INTO tasks (id, title, horizon, period, status, tags, created_at, updated_at)
			VALUES ($1, 't', 'daily', '2026-10-01', 'todo', $2::jsonb, now(), now())`, fmt.Sprintf("seed-%d", i), tags); err != nil {
			t.Fatalf("insert pre-0003 task: %v", err)
		}
	}

	if err := Migrate(dsn); err != nil {
		t.Fatalf("Migrate to head: %v", err)
	}
	names := func() []string {
		t.Helper()
		rows, err := db.Query(`SELECT name FROM tags ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		return out
	}
	want := []string{"backend", "ops", "ui"}
	if got := names(); !slices.Equal(got, want) {
		t.Fatalf("seeded catalog = %v, want %v", got, want)
	}
	var nullBy int
	if err := db.QueryRow(`SELECT count(*) FROM tags WHERE created_by IS NULL`).Scan(&nullBy); err != nil || nullBy != 3 {
		t.Fatalf("seeded rows created_by NULL: n=%d err=%v", nullBy, err)
	}

	// Re-running Migrate is a no-op; re-running the seed statement itself
	// adds nothing and does not error.
	if err := Migrate(dsn); err != nil {
		t.Fatalf("Migrate second run: %v", err)
	}
	raw, err := migrationsFS.ReadFile("migrations/0003_tag_catalog.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(raw), "INSERT INTO tags")
	if i < 0 {
		t.Fatal("seed statement not found in 0003_tag_catalog.up.sql")
	}
	if _, err := db.Exec(string(raw[i:])); err != nil {
		t.Fatalf("seed re-run: %v", err)
	}
	if got := names(); !slices.Equal(got, want) {
		t.Fatalf("catalog after seed re-run = %v, want %v", got, want)
	}
}
