package model

import "time"

// Team mirrors server/store.go's Team struct; JSON tags byte-identical.
type Team struct {
	ID        string    `gorm:"column:id;primaryKey" json:"id"`
	Name      string    `gorm:"column:name" json:"name"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime:false" json:"createdAt"` // business-managed, like Task's
}

// TableName implements gorm's Tabler interface.
func (Team) TableName() string { return "teams" }

// Member mirrors server/store.go's Member struct; JSON tags byte-identical.
//
// TeamIDs replaces Mongo's embedded array via the member_teams join table
// (0001_init.up.sql): it is not a column (`gorm:"-"`) — the repo layer
// populates it from a join query, the same way Task.Activity is populated
// from task_activity.
type Member struct {
	ID        string    `gorm:"column:id;primaryKey" json:"id"`
	Name      string    `gorm:"column:name" json:"name"`
	Email     NullStr   `gorm:"column:email" json:"email,omitempty"`
	Role      string    `gorm:"column:role" json:"role,omitempty"` // job title — distinct from SystemRole
	TeamIDs   []string  `gorm:"-" json:"teamIds,omitempty"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime:false" json:"createdAt"` // business-managed, like Task's

	// Credential fields (docs/AUTH_FEATURES.md decision #1): a member
	// without PasswordHash is assignable-only (today's pre-auth behavior);
	// "enable login" upgrades them. PasswordHash never round-trips through
	// JSON (json:"-") — it's only ever set via bcrypt hashing in auth.go.
	PasswordHash       NullStr    `gorm:"column:password_hash" json:"-"`
	SystemRole         string     `gorm:"column:system_role" json:"systemRole,omitempty"`
	MustChangePassword bool       `gorm:"column:must_change_password" json:"mustChangePassword,omitempty"`
	Disabled           bool       `gorm:"column:disabled" json:"disabled,omitempty"`
	LastLoginAt        *time.Time `gorm:"column:last_login_at" json:"lastLoginAt,omitempty"`
}

// TableName implements gorm's Tabler interface.
func (Member) TableName() string { return "members" }

// Session is a Postgres-backed opaque-token session (docs/AUTH_FEATURES.md
// decision #9), mirroring server/auth.go's Session struct. No JSON tags —
// like the Mongo original, it never round-trips through the API; only the
// opaque Token travels, as the session cookie value.
type Session struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Token     string    `gorm:"column:token"`
	UserID    string    `gorm:"column:user_id"`
	ExpiresAt time.Time `gorm:"column:expires_at"`
}

// TableName implements gorm's Tabler interface.
func (Session) TableName() string { return "sessions" }
