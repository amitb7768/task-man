package repo

import "github.com/google/uuid"

// NewID mints a new opaque primary-key id for a Postgres-native row. Ids are
// opaque strings end to end (docs/DESIGN_PG_FSM_MIGRATION.md: "existing
// ObjectID hex migrates as-is; new ids = UUID — the API treats ids as
// opaque"), so callers in the wave-1.1 repo layer use this instead of
// reaching for uuid.New() directly.
func NewID() string {
	return uuid.NewString()
}
