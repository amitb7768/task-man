package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"taskman/internal/model"
)

// dataTables are the tables the migration writes, in insert order.
var dataTables = []string{"teams", "members", "member_teams", "tasks", "task_activity"}

// targetState is a read-only look at the Postgres side.
type targetState struct {
	Migrated bool
	Version  int64
	Dirty    bool
	Counts   map[string]int64 // dataTables + sessions; nil when not migrated
}

// inspectTarget reads the target inside a READ ONLY transaction, so the dry
// run provably writes nothing to Postgres.
func inspectTarget(ctx context.Context, db *gorm.DB) (*targetState, error) {
	st := &targetState{}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SET TRANSACTION READ ONLY`).Error; err != nil {
			return err
		}
		var present bool
		if err := tx.Raw(`SELECT to_regclass('tasks') IS NOT NULL`).Scan(&present).Error; err != nil {
			return err
		}
		if !present {
			return nil
		}
		st.Migrated = true
		var hasVer bool
		if err := tx.Raw(`SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&hasVer).Error; err != nil {
			return err
		}
		if hasVer {
			var row struct {
				Version int64
				Dirty   bool
			}
			if err := tx.Raw(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&row).Error; err != nil {
				return err
			}
			st.Version, st.Dirty = row.Version, row.Dirty
		}
		c, err := countTables(tx, append(append([]string{}, dataTables...), "sessions"))
		st.Counts = c
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("inspect target: %w", err)
	}
	return st, nil
}

func countTables(db *gorm.DB, tables []string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range tables {
		var n int64
		// table names come from the fixed list above, never from input
		if err := db.Raw(`SELECT count(*) FROM ` + t).Scan(&n).Error; err != nil {
			return nil, fmt.Errorf("count %s: %w", t, err)
		}
		out[t] = n
	}
	return out, nil
}

// targetFindings turns the target state into findings: the migration only
// ever writes into an EMPTY schema (the server boot seeds an ADMIN member
// into an empty DB — if it was started on PG before the migration ran, that
// row must go first).
func targetFindings(st *targetState) []finding {
	var fs []finding
	if !st.Migrated {
		return nil
	}
	if st.Dirty {
		fs = append(fs, finding{Sev: sevFatal, Coll: "target", Msg: fmt.Sprintf("schema_migrations is dirty at version %d — fix the schema first", st.Version)})
	}
	for _, t := range append(append([]string{}, dataTables...), "sessions") {
		if n := st.Counts[t]; n > 0 {
			fs = append(fs, finding{Sev: sevFatal, Coll: "target", Msg: fmt.Sprintf("table %s already has %d row(s) — migrate into an EMPTY schema only (was the server started on PG and seeded an ADMIN?)", t, n)})
		}
	}
	return fs
}

// applyPlan writes the plan in ONE transaction. Any error rolls everything
// back. checkEmpty re-verifies emptiness under a table lock inside the tx
// (tests turn it off to prove the duplicate-key path also rolls back).
func applyPlan(ctx context.Context, db *gorm.DB, p *plan, checkEmpty bool) error {
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Block concurrent writers (the server must be stopped anyway).
		if err := tx.Exec(`LOCK TABLE teams, members, member_teams, tasks, task_activity IN SHARE ROW EXCLUSIVE MODE`).Error; err != nil {
			return fmt.Errorf("lock tables: %w", err)
		}
		if checkEmpty {
			c, err := countTables(tx, dataTables)
			if err != nil {
				return err
			}
			for _, t := range dataTables {
				if c[t] > 0 {
					return fmt.Errorf("target table %s already has %d row(s); refusing to write into a non-empty schema", t, c[t])
				}
			}
		}
		ins := tx.Session(&gorm.Session{CreateBatchSize: 500})
		if len(p.Teams) > 0 {
			if err := ins.Create(&p.Teams).Error; err != nil {
				return fmt.Errorf("insert teams: %w", err)
			}
		}
		if len(p.Members) > 0 {
			if err := ins.Create(&p.Members).Error; err != nil {
				return fmt.Errorf("insert members: %w", err)
			}
		}
		if err := insertMemberTeams(tx, p.MemberTeams); err != nil {
			return fmt.Errorf("insert member_teams: %w", err)
		}
		if len(p.Tasks) > 0 {
			// Activity is gorm:"-" on model.Task; rows go in below.
			if err := ins.Create(&p.Tasks).Error; err != nil {
				return fmt.Errorf("insert tasks: %w", err)
			}
		}
		for i := range p.Tasks {
			acts := p.Tasks[i].Activity
			if len(acts) == 0 {
				continue
			}
			// ONE multi-VALUES INSERT per task, in array order: the identity
			// seq is drawn in VALUES order, so seq order == Mongo array order.
			if err := tx.Create(&acts).Error; err != nil {
				return fmt.Errorf("insert task_activity for task %s: %w", p.Tasks[i].ID, err)
			}
		}
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("duplicate key (SQLSTATE 23505, constraint %s: %s) — the target already holds some of these rows (re-run of the cutover?). Transaction ROLLED BACK, nothing written: %w", pgErr.ConstraintName, pgErr.Detail, err)
		}
		return fmt.Errorf("transaction ROLLED BACK, nothing written: %w", err)
	}
	return nil
}

// insertMemberTeams mirrors internal/service writeMemberTeams' statement
// shape (plain multi-VALUES; the plan already deduped keep-first).
func insertMemberTeams(tx *gorm.DB, rows []memberTeamRow) error {
	const chunk = 1000
	for start := 0; start < len(rows); start += chunk {
		end := min(start+chunk, len(rows))
		var sb strings.Builder
		sb.WriteString(`INSERT INTO member_teams (member_id, team_id, pos) VALUES `)
		args := make([]any, 0, 3*(end-start))
		for i, r := range rows[start:end] {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(?, ?, ?)")
			args = append(args, r.MemberID, r.TeamID, r.Pos)
		}
		if err := tx.Exec(sb.String(), args...).Error; err != nil {
			return err
		}
	}
	return nil
}

// loadTask reads one task back the way the app does (model.Task + its
// activity ordered by seq).
func loadTask(db *gorm.DB, id string) (*model.Task, error) {
	var t model.Task
	if err := db.Where("id = ?", id).First(&t).Error; err != nil {
		return nil, err
	}
	if err := db.Where("task_id = ?", id).Order("seq").Find(&t.Activity).Error; err != nil {
		return nil, err
	}
	if len(t.Activity) == 0 {
		t.Activity = nil
	}
	// pgx hands timestamptz back in time.Local; the app's read path
	// normalizes to UTC (internal/service/tasks.go), so do the same here.
	t.CreatedAt, t.UpdatedAt, t.CompletedAt = t.CreatedAt.UTC(), t.UpdatedAt.UTC(), utcPtr(t.CompletedAt)
	for i := range t.Activity {
		e := &t.Activity[i]
		e.At, e.EditedAt = e.At.UTC(), utcPtr(e.EditedAt)
	}
	return &t, nil
}
