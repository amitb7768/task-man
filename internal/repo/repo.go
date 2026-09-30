// Package repo is taskman's Postgres infrastructure: the embedded schema
// migrations and their boot-time runner (Migrate), the gorm pool (Open), id
// minting (NewID), and a few pure query helpers with no business rules.
// Business flows — and the transactions they need — live in
// internal/service (docs/DESIGN_PG_FSM_MIGRATION.md, P3).
package repo

import (
	"net/url"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open opens a gorm Postgres handle on dsn. SQL logging is off: the server
// has its own request log, and every repo error is returned to the caller.
//
// The session TimeZone is forced to UTC so every timestamptz reads back in
// UTC — the Mongo driver decoded UTC, so this keeps read-path JSON in its
// historical "…Z" form across ALL repos (create/patch responses still carry
// the in-memory local `now`, also Mongo parity). pgx passes unknown URL
// query params to the server as runtime session parameters.
func Open(dsn string) (*gorm.DB, error) {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		q := u.Query()
		if q.Get("TimeZone") == "" && q.Get("timezone") == "" {
			q.Set("TimeZone", "UTC")
			u.RawQuery = q.Encode()
			dsn = u.String()
		}
	}
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}
