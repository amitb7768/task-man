package repo

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx/v5" database/sql driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending up-migrations in ./migrations against dsn (a
// standard "postgres://..." DSN — pgx's stdlib driver, never lib/pq; see
// docs/DESIGN_PG_FSM_MIGRATION.md).
//
// It respects a schema-scoped DSN: a `search_path=<schema>[,...]` query
// parameter (a normal libpq/pgx connection parameter) scopes not just the
// migrated tables but golang-migrate's own version-tracking table too —
// pgxmigrate.WithInstance defaults its Config.SchemaName from
// `SELECT CURRENT_SCHEMA()`, which reflects whatever schema search_path
// resolved to for this connection. This is what lets a test harness migrate
// an isolated scratch schema per test/run without colliding with another
// run's schema_migrations row, all against the same database.
func Migrate(dsn string) error {
	db, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		return fmt.Errorf("repo: open db: %w", err)
	}
	defer db.Close()

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("repo: init migrate driver: %w", err)
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("repo: init migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx/v5", driver)
	if err != nil {
		return fmt.Errorf("repo: init migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("repo: migrate up: %w", err)
	}
	return nil
}
