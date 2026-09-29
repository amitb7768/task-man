package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"

	"taskman/internal/model"
	"taskman/internal/repo"
)

// Integration suite: a THROWAWAY Mongo (TASKMAN_TEST_MONGO_URI, e.g. a
// `docker run -d --rm -p 127.0.0.1:27117:27017 mongo:7`) plus the usual
// TASKMAN_TEST_PG_DSN with a scratch schema per test. Skips cleanly unless
// both are set and reachable. It REFUSES a Mongo URI on port 27017 — that
// is where the production taskman Mongo lives.

func testMongo(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("TASKMAN_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TASKMAN_TEST_MONGO_URI not set; skipping Mongo-backed migration test")
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse TASKMAN_TEST_MONGO_URI: %v", err)
	}
	if p := u.Port(); p == "" || p == "27017" {
		t.Fatalf("TASKMAN_TEST_MONGO_URI %q targets port 27017 (prod taskman Mongo) — use a throwaway container on another port", uri)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := connectMongo(ctx, uri)
	if err != nil {
		t.Skipf("test Mongo unreachable (%v); skipping", err)
	}
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })
	db := c.Database(fmt.Sprintf("migtest_%d_%d", time.Now().UnixNano(), atomic.AddInt64(&counter, 1)))
	t.Cleanup(func() { _ = db.Drop(context.Background()) })
	return db
}

var counter int64

// scratchPG returns a DSN scoped to a fresh scratch schema (dropped on
// cleanup), plus a gorm handle on it.
func scratchPG(t *testing.T) (string, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("TASKMAN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TASKMAN_TEST_PG_DSN not set; skipping Postgres-backed test")
	}
	admin, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Skipf("test Postgres unreachable (%v); skipping", err)
	}
	schema := fmt.Sprintf("taskman_test_migmongo_%d_%d", time.Now().UnixNano(), atomic.AddInt64(&counter, 1))
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE`, schema)); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	scoped := u.String()
	db, err := repo.Open(scoped)
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})
	return scoped, db
}

// fixture ids
type fx struct {
	T1, T2                     bson.ObjectID
	M1, M2, M3, MDupEmail      bson.ObjectID
	MissingMember              bson.ObjectID // referenced, never inserted
	G, C, P                    bson.ObjectID // grandchild -> child -> parent; _id order G < C < P
	Series                     bson.ObjectID
	FatalAssignee, FatalStatus bson.ObjectID
	Acts                       []bson.ObjectID // P's activity, array order
}

func ms(y int, mo time.Month, d, h, mi, s, milli int) time.Time {
	return time.Date(y, mo, d, h, mi, s, milli*int(time.Millisecond), time.UTC)
}

func seed(t *testing.T, db *mongo.Database) fx {
	t.Helper()
	ctx := context.Background()
	var f fx
	f.T1, f.T2 = bson.NewObjectID(), bson.NewObjectID()
	f.M1, f.M2, f.M3, f.MDupEmail, f.MissingMember = bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID()
	// minted in this order so _id sort puts the grandchild first: the tool
	// reads in _id order and must still insert parents first.
	f.G, f.C, f.P = bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID()
	f.Series = bson.NewObjectID()
	f.FatalAssignee, f.FatalStatus = bson.NewObjectID(), bson.NewObjectID()
	// activity ids minted in REVERSE of array order so an ORDER BY id would
	// visibly break the order assertion.
	f.Acts = make([]bson.ObjectID, 4)
	for i := 3; i >= 0; i-- {
		f.Acts[i] = bson.NewObjectID()
	}
	created := ms(2026, 9, 1, 8, 0, 0, 123)

	ins := func(coll string, docs ...any) {
		if _, err := db.Collection(coll).InsertMany(ctx, docs); err != nil {
			t.Fatalf("seed %s: %v", coll, err)
		}
	}
	ins("teams",
		bson.D{{Key: "_id", Value: f.T1}, {Key: "name", Value: "Alpha"}, {Key: "createdAt", Value: created}},
		bson.D{{Key: "_id", Value: f.T2}, {Key: "name", Value: "Beta"}, {Key: "createdAt", Value: created}},
	)
	ins("members",
		// login-enabled admin, teamIds out of team order with a duplicate
		bson.D{{Key: "_id", Value: f.M1}, {Key: "name", Value: "Admin"}, {Key: "email", Value: "Admin@x.io"}, {Key: "role", Value: "lead"},
			{Key: "teamIds", Value: bson.A{f.T2, f.T1, f.T2}}, {Key: "createdAt", Value: created},
			{Key: "passwordHash", Value: "$2a$hash1"}, {Key: "systemRole", Value: "ADMIN"}, {Key: "mustChangePassword", Value: true},
			{Key: "lastLoginAt", Value: ms(2026, 9, 20, 9, 0, 0, 7)}},
		// everything optional ABSENT
		bson.D{{Key: "_id", Value: f.M2}, {Key: "name", Value: "Plain"}, {Key: "createdAt", Value: created}},
		// optional strings explicitly EMPTY
		bson.D{{Key: "_id", Value: f.M3}, {Key: "name", Value: "Empty"}, {Key: "email", Value: ""}, {Key: "passwordHash", Value: ""},
			{Key: "role", Value: ""}, {Key: "teamIds", Value: bson.A{}}, {Key: "createdAt", Value: created}},
		// FATAL: login email colliding case-insensitively with M1
		bson.D{{Key: "_id", Value: f.MDupEmail}, {Key: "name", Value: "Dup"}, {Key: "email", Value: "admin@X.IO"},
			{Key: "passwordHash", Value: "$2a$hash2"}, {Key: "systemRole", Value: "USER"}, {Key: "createdAt", Value: created}},
	)
	sameAt := ms(2026, 9, 10, 12, 0, 0, 500)
	ins("tasks",
		// grandchild (inserted first): personal, done
		bson.D{{Key: "_id", Value: f.G}, {Key: "title", Value: "grandchild"}, {Key: "horizon", Value: "daily"}, {Key: "period", Value: "2026-09-10"},
			{Key: "status", Value: "done"}, {Key: "priority", Value: "low"}, {Key: "parentId", Value: f.C}, {Key: "ownerId", Value: f.M2},
			{Key: "createdAt", Value: created}, {Key: "updatedAt", Value: created}, {Key: "completedAt", Value: ms(2026, 9, 10, 18, 0, 0, 1)}},
		// child: personal backlog, dueDate/notes explicitly EMPTY, weekOf absent
		bson.D{{Key: "_id", Value: f.C}, {Key: "title", Value: "child"}, {Key: "notes", Value: ""}, {Key: "horizon", Value: "backlog"}, {Key: "period", Value: ""},
			{Key: "dueDate", Value: ""}, {Key: "status", Value: "todo"}, {Key: "priority", Value: ""}, {Key: "parentId", Value: f.P},
			{Key: "ownerId", Value: f.M1}, {Key: "createdAt", Value: created}, {Key: "updatedAt", Value: created}},
		// parent: team task, everything set, recurrence, 4 activity entries (two share `at`, one by a missing member)
		bson.D{{Key: "_id", Value: f.P}, {Key: "title", Value: "parent"}, {Key: "notes", Value: "some notes"}, {Key: "horizon", Value: "weekly"},
			{Key: "period", Value: "2026-W39"}, {Key: "dueDate", Value: "2026-09-30"}, {Key: "status", Value: "in_progress"}, {Key: "priority", Value: "high"},
			{Key: "teamId", Value: f.T1}, {Key: "assigneeId", Value: f.M1}, {Key: "weekOf", Value: "2026-W39"},
			{Key: "recurrence", Value: bson.D{{Key: "freq", Value: "weekly"}, {Key: "interval", Value: 2}, {Key: "anchor", Value: "2026-W39"}}},
			{Key: "seriesId", Value: f.Series}, {Key: "createdAt", Value: created}, {Key: "updatedAt", Value: ms(2026, 9, 21, 7, 30, 0, 999)},
			{Key: "activity", Value: bson.A{
				bson.D{{Key: "_id", Value: f.Acts[0]}, {Key: "kind", Value: "note"}, {Key: "date", Value: "2026-09-12"}, {Key: "at", Value: ms(2026, 9, 12, 9, 0, 0, 0)},
					{Key: "by", Value: f.M1}, {Key: "byName", Value: "Admin"}, {Key: "text", Value: "first"}, {Key: "editedAt", Value: ms(2026, 9, 12, 10, 0, 0, 42)}},
				bson.D{{Key: "_id", Value: f.Acts[1]}, {Key: "kind", Value: "status"}, {Key: "date", Value: "2026-09-10"}, {Key: "at", Value: sameAt},
					{Key: "by", Value: f.M1}, {Key: "byName", Value: "Admin"}, {Key: "from", Value: "todo"}, {Key: "to", Value: "in_progress"}},
				bson.D{{Key: "_id", Value: f.Acts[2]}, {Key: "kind", Value: "note"}, {Key: "date", Value: "2026-09-10"}, {Key: "at", Value: sameAt},
					{Key: "by", Value: f.MissingMember}, {Key: "byName", Value: "Ghost"}, {Key: "text", Value: "same instant"}},
				bson.D{{Key: "_id", Value: f.Acts[3]}, {Key: "kind", Value: "note"}, {Key: "date", Value: "2026-09-01"}, {Key: "at", Value: ms(2026, 9, 5, 0, 0, 0, 0)},
					{Key: "text", Value: "backdated, no author"}},
			}}},
		// FATAL: dangling assignee
		bson.D{{Key: "_id", Value: f.FatalAssignee}, {Key: "title", Value: "bad assignee"}, {Key: "horizon", Value: "daily"}, {Key: "period", Value: "2026-09-10"},
			{Key: "status", Value: "todo"}, {Key: "priority", Value: ""}, {Key: "teamId", Value: f.T2}, {Key: "assigneeId", Value: f.MissingMember},
			{Key: "weekOf", Value: "2026-W37"}, {Key: "createdAt", Value: created}, {Key: "updatedAt", Value: created}},
		// FATAL: status outside the CHECK set
		bson.D{{Key: "_id", Value: f.FatalStatus}, {Key: "title", Value: "bad status"}, {Key: "horizon", Value: "daily"}, {Key: "period", Value: "2026-09-10"},
			{Key: "status", Value: "blocked"}, {Key: "priority", Value: ""}, {Key: "ownerId", Value: f.M1}, {Key: "createdAt", Value: created}, {Key: "updatedAt", Value: created}},
	)
	ins("sessions", bson.D{{Key: "_id", Value: bson.NewObjectID()}, {Key: "token", Value: "tok"}, {Key: "userId", Value: f.M1}, {Key: "expiresAt", Value: ms(2026, 12, 1, 0, 0, 0, 0)}})
	return f
}

func removeFatals(t *testing.T, db *mongo.Database, f fx) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Collection("tasks").DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{f.FatalAssignee, f.FatalStatus}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("members").DeleteOne(ctx, bson.D{{Key: "_id", Value: f.MDupEmail}}); err != nil {
		t.Fatal(err)
	}
}

func runTool(t *testing.T, mdb *mongo.Database, dsn string, apply bool) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	code := run(ctx, runOptions{MongoURI: os.Getenv("TASKMAN_TEST_MONGO_URI"), MongoDB: mdb.Name(), PGDSN: dsn, Apply: apply}, &buf)
	t.Logf("exit=%d\n%s", code, buf.String())
	return code, buf.String()
}

func rowCounts(t *testing.T, db *gorm.DB) map[string]int64 {
	t.Helper()
	c, err := countTables(db, append(append([]string{}, dataTables...), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustContain(t *testing.T, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q", s)
		}
	}
}

// (1) Dry run: every finding reported, nonzero exit, zero PG writes.
func TestDryRunReportsFindingsAndWritesNothing(t *testing.T) {
	mdb := testMongo(t)
	f := seed(t, mdb)

	// (a) unmigrated schema: the dry run must not even create tables.
	dsn, db := scratchPG(t)
	code, out := runTool(t, mdb, dsn, false)
	if code != 1 {
		t.Fatalf("dry run exit = %d, want 1", code)
	}
	mustContain(t, out,
		"schema: not migrated",
		"assigneeId "+f.MissingMember.Hex()+" → no such member",
		`status "blocked" not in`,
		"collides case-insensitively with member "+f.M1.Hex(),
		"activity[2].by "+f.MissingMember.Hex()+" → no such member; by_id written as NULL",
		"sessions       1  (NOT migrated by design",
		"FATAL (3)",
		"WARN (1)",
		"DRY RUN — nothing written. 3 FATAL finding(s)",
	)
	var n int
	if err := db.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema()`).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("dry run created %d table(s) in the scratch schema", n)
	}

	// (b) migrated, empty schema: still zero rows afterwards.
	dsn2, db2 := scratchPG(t)
	if err := repo.Migrate(dsn2); err != nil {
		t.Fatal(err)
	}
	code, out = runTool(t, mdb, dsn2, false)
	if code != 1 {
		t.Fatalf("dry run exit = %d, want 1", code)
	}
	mustContain(t, out, "schema: migrated (version 1, dirty=false)", "DRY RUN — nothing written")
	for tbl, c := range rowCounts(t, db2) {
		if c != 0 {
			t.Errorf("dry run wrote %d row(s) to %s", c, tbl)
		}
	}

	// --apply with fatals refuses the same way.
	code, out = runTool(t, mdb, dsn2, true)
	if code != 1 || !strings.Contains(out, "REFUSED — 3 FATAL") {
		t.Fatalf("apply with fatals: exit=%d", code)
	}
	for tbl, c := range rowCounts(t, db2) {
		if c != 0 {
			t.Errorf("refused apply wrote %d row(s) to %s", c, tbl)
		}
	}
}

// (2) + (3) Apply after the fatal fixtures are gone; then re-apply guards.
func TestApplyMigratesFaithfullyAndRefusesRerun(t *testing.T) {
	mdb := testMongo(t)
	f := seed(t, mdb)
	removeFatals(t, mdb, f)
	dsn, db := scratchPG(t)

	code, out := runTool(t, mdb, dsn, true)
	if code != 0 {
		t.Fatalf("apply exit = %d, want 0", code)
	}
	mustContain(t, out, "schema: not migrated", "COMMITTED — teams=2 members=3 member_teams=2 tasks=3 task_activity=4", "ALL CHECKS PASSED")
	if strings.Contains(out, "FAIL") {
		t.Errorf("verification printed a FAIL line")
	}

	want := map[string]int64{"teams": 2, "members": 3, "member_teams": 2, "tasks": 3, "task_activity": 4, "sessions": 0}
	if got := rowCounts(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("row counts = %v, want %v", got, want)
	}

	// ids preserved as hex
	var ids []string
	db.Raw(`SELECT id FROM tasks ORDER BY id`).Scan(&ids)
	if !reflect.DeepEqual(ids, []string{f.G.Hex(), f.C.Hex(), f.P.Hex()}) {
		t.Errorf("task ids = %v", ids)
	}

	// NULL where absent OR empty; '' kept for period/priority
	type nullRow struct {
		Notes, DueDate, WeekOf, Recurrence, TeamID, SeriesID *string
		Period, Priority                                     string
	}
	var c nullRow
	db.Raw(`SELECT notes, due_date, week_of, recurrence::text AS recurrence, team_id, series_id, period, priority FROM tasks WHERE id = ?`, f.C.Hex()).Scan(&c)
	if c.Notes != nil || c.DueDate != nil || c.WeekOf != nil || c.Recurrence != nil || c.TeamID != nil || c.SeriesID != nil {
		t.Errorf("child optional columns not all NULL: %+v", c)
	}
	if c.Period != "" || c.Priority != "" {
		t.Errorf("child period/priority = %q/%q, want ''", c.Period, c.Priority)
	}
	var recurNull bool
	db.Raw(`SELECT recurrence IS NULL FROM tasks WHERE id = ?`, f.C.Hex()).Scan(&recurNull)
	if !recurNull {
		t.Errorf("absent recurrence must be SQL NULL (not jsonb 'null')")
	}
	type memRow struct {
		Email, PasswordHash *string
		LastLoginAt         *time.Time
		Role, SystemRole    string
	}
	for _, id := range []bson.ObjectID{f.M2, f.M3} {
		var m memRow
		db.Raw(`SELECT email, password_hash, last_login_at, role, system_role FROM members WHERE id = ?`, id.Hex()).Scan(&m)
		if m.Email != nil || m.PasswordHash != nil || m.LastLoginAt != nil || m.Role != "" || m.SystemRole != "" {
			t.Errorf("member %s: %+v, want NULL email/hash/lastLogin", id.Hex(), m)
		}
	}
	var m1 model.Member
	if err := db.Where("id = ?", f.M1.Hex()).First(&m1).Error; err != nil {
		t.Fatal(err)
	}
	if m1.Email != "Admin@x.io" || m1.PasswordHash != "$2a$hash1" || m1.SystemRole != "ADMIN" || !m1.MustChangePassword || m1.LastLoginAt == nil ||
		!m1.LastLoginAt.Equal(ms(2026, 9, 20, 9, 0, 0, 7)) || m1.Role != "lead" {
		t.Errorf("M1 = %+v", m1)
	}

	// member_teams: array order via pos, duplicate keeps first position
	tids, err := repo.LoadMemberTeamIDs(db, []string{f.M1.Hex(), f.M3.Hex()})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tids[f.M1.Hex()], []string{f.T2.Hex(), f.T1.Hex()}) || tids[f.M3.Hex()] != nil {
		t.Errorf("member teams = %v", tids)
	}
	var pos []int
	db.Raw(`SELECT pos FROM member_teams WHERE member_id = ? ORDER BY pos`, f.M1.Hex()).Scan(&pos)
	if !reflect.DeepEqual(pos, []int{0, 1}) {
		t.Errorf("pos = %v", pos)
	}

	// parent: app-shaped read (model.Task), recurrence round-trip, weekOf, activity order
	pt, err := loadTask(db, f.P.Hex())
	if err != nil {
		t.Fatal(err)
	}
	two := 2
	if !reflect.DeepEqual(pt.Recurrence, &model.Recurrence{Freq: "weekly", Interval: &two, Anchor: "2026-W39"}) {
		t.Errorf("recurrence = %+v", pt.Recurrence)
	}
	var recurJSON string
	db.Raw(`SELECT recurrence::text FROM tasks WHERE id = ?`, f.P.Hex()).Scan(&recurJSON)
	if recurJSON != `{"freq": "weekly", "anchor": "2026-W39", "interval": 2}` {
		t.Errorf("recurrence jsonb = %s", recurJSON)
	}
	if pt.WeekOf != "2026-W39" || pt.DueDate != "2026-09-30" || pt.Notes != "some notes" || *pt.TeamID != f.T1.Hex() ||
		*pt.AssigneeID != f.M1.Hex() || *pt.SeriesID != f.Series.Hex() || pt.OwnerID != nil || pt.Priority != "high" ||
		!pt.UpdatedAt.Equal(ms(2026, 9, 21, 7, 30, 0, 999)) {
		t.Errorf("parent = %+v", pt)
	}
	if len(pt.Activity) != 4 {
		t.Fatalf("parent activity len = %d", len(pt.Activity))
	}
	for i, e := range pt.Activity { // loadTask orders by seq
		if e.ID != f.Acts[i].Hex() {
			t.Errorf("activity[%d] (by seq) = %s, want %s — seq order must equal Mongo array order", i, e.ID, f.Acts[i].Hex())
		}
	}
	if a := pt.Activity[2]; a.By != nil || a.ByName != "Ghost" || a.Text != "same instant" {
		t.Errorf("dangling-by entry = %+v, want by NULL, byName kept", a)
	}
	if a := pt.Activity[0]; a.By == nil || *a.By != f.M1.Hex() || a.EditedAt == nil || !a.EditedAt.Equal(ms(2026, 9, 12, 10, 0, 0, 42)) {
		t.Errorf("activity[0] = %+v", a)
	}
	if a := pt.Activity[1]; a.From != "todo" || a.To != "in_progress" || a.Kind != "status" {
		t.Errorf("activity[1] = %+v", a)
	}
	var nullStrs int
	db.Raw(`SELECT count(*) FROM task_activity WHERE by_id = '' OR text IS NULL`).Scan(&nullStrs)
	if nullStrs != 0 {
		t.Errorf("unexpected '' by_id / NULL text rows: %d", nullStrs)
	}

	// grandchild chain intact
	var parentOfG, parentOfC string
	db.Raw(`SELECT parent_id FROM tasks WHERE id = ?`, f.G.Hex()).Scan(&parentOfG)
	db.Raw(`SELECT parent_id FROM tasks WHERE id = ?`, f.C.Hex()).Scan(&parentOfC)
	if parentOfG != f.C.Hex() || parentOfC != f.P.Hex() {
		t.Errorf("parent chain G→%s C→%s", parentOfG, parentOfC)
	}

	// (3a) re-running the cutover: refused by the empty-target pre-check.
	code, out = runTool(t, mdb, dsn, true)
	if code != 1 {
		t.Fatalf("second apply exit = %d, want 1", code)
	}
	mustContain(t, out, "table teams already has 2 row(s)", "REFUSED")
	if got := rowCounts(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("row counts after refused re-run = %v, want %v", got, want)
	}

	// (3b) with the pre-check bypassed, the PK makes it fail with 23505 and
	// the whole transaction rolls back.
	p := buildPlan(mustRead(t, mdb))
	err = applyPlan(context.Background(), db, p, false)
	if err == nil || !strings.Contains(err.Error(), "SQLSTATE 23505") || !strings.Contains(err.Error(), "ROLLED BACK") {
		t.Fatalf("duplicate apply err = %v, want a 23505 rolled-back error", err)
	}
	t.Logf("duplicate apply error: %v", err)
	if got := rowCounts(t, db); !reflect.DeepEqual(got, want) {
		t.Fatalf("row counts after failed duplicate apply = %v, want %v", got, want)
	}
}

func mustRead(t *testing.T, mdb *mongo.Database) *source {
	t.Helper()
	src, err := readSource(context.Background(), mdb)
	if err != nil {
		t.Fatal(err)
	}
	return src
}
