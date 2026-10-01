package model

import "time"

// View/result/param types the task domain returns. Each mirrors the
// server/store.go type of the same name with ids as opaque strings; JSON tags
// are byte-identical to it.

// Progress is a task's direct-child completion count (done + cancelled count
// as done).
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// TaskView is every task field plus computed direct-child progress.
type TaskView struct {
	Task
	Progress Progress `json:"progress"`
}

// TaskDetail is a TaskView plus its direct children, each a TaskView.
type TaskDetail struct {
	TaskView
	Children []TaskView `json:"children"`
}

// BoardMemberTasks is one member's column on a team board.
type BoardMemberTasks struct {
	Member Member     `json:"member"`
	Tasks  []TaskView `json:"tasks"`
}

// Board is GET /api/teams/{id}/board's response.
type Board struct {
	Team       Team               `json:"team"`
	Members    []BoardMemberTasks `json:"members"`
	Unassigned []TaskView         `json:"unassigned"`
	// Week is the current ISO week ("YYYY-Www") the board's visibility
	// filter is scoped to (docs/DESIGN_V6_WEEK_ROLLOVER.md).
	Week string `json:"week"`
	// StaleOpen counts open (todo/in_progress), undated tasks whose weekOf
	// is before Week — the rollover banner's trigger, ADMIN-only in the UI.
	StaleOpen int `json:"staleOpen"`
}

// SearchParams is GET /api/tasks/search's parsed query string.
type SearchParams struct {
	Q, Status, Priority, Horizon string
	TeamID, AssigneeID           *string
	Overdue                      bool
	// Tags: AND filter — a task matches when it carries every tag
	// (normalised by the handler). Empty = no tag filter.
	Tags []string
}

// NoteInput is the client-settable half of a note entry
// (docs/DESIGN_V9_NOTES_SUMMARY.md). Every other ActivityEntry field is
// server-derived.
type NoteInput struct {
	Text string `json:"text"`
	Date string `json:"date"`
}

// SummaryParams is GET /api/summary's parsed query string.
type SummaryParams struct {
	From, To   string
	TeamID     *string
	AssigneeID *string
}

// SummaryTask is one row of a summary section: the reporting-relevant task
// fields plus the entries from its timeline that fall inside the range.
type SummaryTask struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Status       string          `json:"status"`
	Priority     string          `json:"priority"`
	Horizon      string          `json:"horizon"`
	DueDate      string          `json:"dueDate,omitempty"`
	TeamID       *string         `json:"teamId,omitempty"`
	TeamName     string          `json:"teamName,omitempty"`
	AssigneeID   *string         `json:"assigneeId,omitempty"`
	AssigneeName string          `json:"assigneeName,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	ClosedDate   string          `json:"closedDate,omitempty"`
	Overdue      bool            `json:"overdue"`
	Notes        []ActivityEntry `json:"notes"`
}

// SummaryResult is GET /api/summary's response. teamId/teamName appear only
// in team scope, assigneeId/assigneeName only when filtered; the three
// section slices are always non-nil.
type SummaryResult struct {
	From         string        `json:"from"`
	To           string        `json:"to"`
	TeamID       *string       `json:"teamId,omitempty"`
	TeamName     string        `json:"teamName,omitempty"`
	AssigneeID   *string       `json:"assigneeId,omitempty"`
	AssigneeName string        `json:"assigneeName,omitempty"`
	Completed    []SummaryTask `json:"completed"`
	Updated      []SummaryTask `json:"updated"`
	Added        []SummaryTask `json:"added"`
}
