package model

import "time"

// Task statuses.
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// System-wide roles (docs/AUTH_FEATURES.md decision #2). Distinct from the
// pre-existing Member.Role job-title field — never conflate the two.
const (
	RoleAdmin = "ADMIN"
	RoleUser  = "USER"
)

// Activity entry kinds.
const (
	ActivityNote   = "note"
	ActivityStatus = "status"
)

// Task mirrors server/store.go's Task struct: the JSON tags below are
// byte-identical to it (this API's JSON must not change by one byte) — only
// the Go types differ, mapping bson.ObjectID -> string and Mongo
// field-absence -> either NullStr (for optional plain-string columns) or a
// nil pointer (for FK/embedded-doc columns), per
// internal/repo/migrations/0001_init.up.sql.
type Task struct {
	ID       string  `gorm:"column:id;primaryKey" json:"id"`
	Title    string  `gorm:"column:title" json:"title"`
	Notes    NullStr `gorm:"column:notes" json:"notes,omitempty"`
	Horizon  string  `gorm:"column:horizon" json:"horizon"`
	Period   string  `gorm:"column:period" json:"period"`
	DueDate  NullStr `gorm:"column:due_date" json:"dueDate,omitempty"`
	Status   string  `gorm:"column:status" json:"status"`
	Priority string  `gorm:"column:priority" json:"priority"`

	ParentID   *string `gorm:"column:parent_id" json:"parentId,omitempty"`
	TeamID     *string `gorm:"column:team_id" json:"teamId,omitempty"`
	AssigneeID *string `gorm:"column:assignee_id" json:"assigneeId,omitempty"`

	// WeekOf is system-managed and present iff TeamID is set: the ISO week
	// ("YYYY-Www") a team task last mattered — assigned on create, bumped
	// unconditionally on transition into a terminal status (done/cancelled),
	// and cleared on team->personal flip. Drives team-board visibility and
	// rollover/history (docs/DESIGN_V6_WEEK_ROLLOVER.md). Client-settable
	// only by ADMIN via PATCH (the rollover "move" primitive) — never by
	// USER.
	WeekOf NullStr `gorm:"column:week_of" json:"weekOf,omitempty"`

	// OwnerID is set (and system-managed) exactly when TeamID is nil: a
	// personal task's owner, the only session that may see it (see
	// canAccessTask). Never client-settable; derived from the session user
	// on create, and re-derived on patch if teamId's nil-ness changes.
	OwnerID *string `gorm:"column:owner_id" json:"ownerId,omitempty"`

	// Recurrence is stored as jsonb; a nil pointer MUST persist as SQL NULL,
	// never jsonb 'null' (see Recurrence.Value/Scan in recur.go).
	Recurrence *Recurrence `gorm:"column:recurrence;type:jsonb" json:"recurrence,omitempty"`
	SeriesID   *string     `gorm:"column:series_id" json:"seriesId,omitempty"`

	// autoCreateTime/autoUpdateTime are OFF: timestamps are business-managed
	// (createdAt set once in CreateTask and never patchable; updatedAt bumped
	// exactly where store.go bumps it). gorm's name-based auto-touch would
	// silently rewrite updatedAt on every Save — breaking restore replay,
	// which must preserve the original timestamps verbatim.
	CreatedAt   time.Time  `gorm:"column:created_at;autoCreateTime:false" json:"createdAt"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;autoUpdateTime:false" json:"updatedAt"`
	CompletedAt *time.Time `gorm:"column:completed_at" json:"completedAt,omitempty"`

	// Activity is the task's append-only daily-notes/status timeline
	// (docs/DESIGN_V9_NOTES_SUMMARY.md). It lives in the task_activity child
	// table (see ActivityEntry), never as a column on tasks — `gorm:"-"`
	// keeps GORM from touching it on Task CRUD; the repo layer loads/writes
	// it explicitly, ordered by ActivityEntry.Seq. System-managed: never
	// settable through POST/PATCH /api/tasks — only the /notes endpoints and
	// PatchTask's status auto-log write it. List reads project it away (see
	// findViews), so never write back a Task that came from a projected
	// read.
	Activity []ActivityEntry `gorm:"-" json:"activity,omitempty"`
}

// TableName implements gorm's Tabler interface.
func (Task) TableName() string { return "tasks" }

// ActivityEntry is one timeline entry on a Task: either a user-written note
// (kind "note", carrying Text) or an auto-logged status transition (kind
// "status", carrying From/To). Date is the machine-local "YYYY-MM-DD" day
// bucket the entry belongs to — backdating is allowed and intended, so it is
// deliberately independent of At (the server instant of the write). ByName is
// denormalized at write time so reads never join against members.
//
// JSON tags are byte-identical to server/store.go's ActivityEntry (id, kind,
// date, at, by, byName, text, from, to, editedAt, with that struct's exact
// omitempty set). TaskID and Seq are DB-only (json:"-"): task_activity.seq is
// `GENERATED ALWAYS AS IDENTITY`, so it must never be sent on INSERT —
// `<-:false` makes the field read-only from GORM's perspective (populated on
// SELECT, ignored on Create) — and ordering reads use Seq, never At (`at`
// values can tie; see 0001_init.up.sql).
type ActivityEntry struct {
	ID     string `gorm:"column:id;primaryKey" json:"id"`
	TaskID string `gorm:"column:task_id" json:"-"`
	// <-:false keeps Seq out of every INSERT (task_activity.seq is
	// GENERATED ALWAYS AS IDENTITY — Postgres rejects an explicit value,
	// even a zero, without OVERRIDING SYSTEM VALUE). The empty `default:`
	// marks it HasDefaultValue-with-no-client-computable-value, which is
	// what makes GORM add it to the INSERT's RETURNING clause so Seq comes
	// back populated on Create without a second round-trip.
	Seq int64 `gorm:"column:seq;<-:false;default:" json:"-"`

	Kind string    `gorm:"column:kind" json:"kind"`
	Date string    `gorm:"column:date" json:"date"`
	At   time.Time `gorm:"column:at" json:"at"`

	By       *string    `gorm:"column:by_id" json:"by,omitempty"`
	ByName   string     `gorm:"column:by_name" json:"byName,omitempty"`
	Text     string     `gorm:"column:text" json:"text,omitempty"`
	From     string     `gorm:"column:from_status" json:"from,omitempty"`
	To       string     `gorm:"column:to_status" json:"to,omitempty"`
	EditedAt *time.Time `gorm:"column:edited_at" json:"editedAt,omitempty"`
}

// TableName implements gorm's Tabler interface.
func (ActivityEntry) TableName() string { return "task_activity" }
