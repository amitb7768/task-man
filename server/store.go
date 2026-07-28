package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Task statuses.
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

var validStatuses = map[string]bool{
	StatusTodo: true, StatusInProgress: true, StatusDone: true, StatusCancelled: true,
}
var validPriorities = map[string]bool{"": true, "low": true, "medium": true, "high": true}

// apiError carries an HTTP status alongside a message; handlers unwrap it to
// decide the response code, defaulting to 500 for anything else.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &apiError{400, fmt.Sprintf(format, args...)}
}
func notFoundErr(format string, args ...any) error {
	return &apiError{404, fmt.Sprintf(format, args...)}
}
func conflictErr(format string, args ...any) error {
	return &apiError{409, fmt.Sprintf(format, args...)}
}
func unauthorizedErr(format string, args ...any) error {
	return &apiError{401, fmt.Sprintf(format, args...)}
}
func forbiddenErr(format string, args ...any) error {
	return &apiError{403, fmt.Sprintf(format, args...)}
}
func unsupportedMediaErr(format string, args ...any) error {
	return &apiError{415, fmt.Sprintf(format, args...)}
}

// System-wide roles (docs/AUTH_FEATURES.md decision #2). Distinct from the
// pre-existing Member.Role job-title field — never conflate the two.
const (
	RoleAdmin = "ADMIN"
	RoleUser  = "USER"
)

// ---- data model ----

type Task struct {
	ID         bson.ObjectID  `bson:"_id" json:"id"`
	Title      string         `bson:"title" json:"title"`
	Notes      string         `bson:"notes,omitempty" json:"notes,omitempty"`
	Horizon    string         `bson:"horizon" json:"horizon"`
	Period     string         `bson:"period" json:"period"`
	DueDate    string         `bson:"dueDate,omitempty" json:"dueDate,omitempty"`
	Status     string         `bson:"status" json:"status"`
	Priority   string         `bson:"priority" json:"priority"`
	ParentID   *bson.ObjectID `bson:"parentId,omitempty" json:"parentId,omitempty"`
	TeamID     *bson.ObjectID `bson:"teamId,omitempty" json:"teamId,omitempty"`
	AssigneeID *bson.ObjectID `bson:"assigneeId,omitempty" json:"assigneeId,omitempty"`
	// WeekOf is system-managed and present iff TeamID is set: the ISO week
	// ("YYYY-Www") a team task last mattered — assigned on create, bumped
	// unconditionally on transition into a terminal status (done/cancelled),
	// and cleared on team->personal flip. Drives team-board visibility and
	// rollover/history (docs/DESIGN_V6_WEEK_ROLLOVER.md). Client-settable
	// only by ADMIN via PATCH (the rollover "move" primitive) — never by
	// USER.
	WeekOf string `bson:"weekOf,omitempty" json:"weekOf,omitempty"`
	// OwnerID is set (and system-managed) exactly when TeamID is nil: a
	// personal task's owner, the only session that may see it (see
	// canAccessTask). Never client-settable; derived from the session user
	// on create, and re-derived on patch if teamId's nil-ness changes.
	OwnerID     *bson.ObjectID `bson:"ownerId,omitempty" json:"ownerId,omitempty"`
	Recurrence  *Recurrence    `bson:"recurrence,omitempty" json:"recurrence,omitempty"`
	SeriesID    *bson.ObjectID `bson:"seriesId,omitempty" json:"seriesId,omitempty"`
	CreatedAt   time.Time      `bson:"createdAt" json:"createdAt"`
	UpdatedAt   time.Time      `bson:"updatedAt" json:"updatedAt"`
	CompletedAt *time.Time     `bson:"completedAt,omitempty" json:"completedAt,omitempty"`
}

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

type Team struct {
	ID        bson.ObjectID `bson:"_id" json:"id"`
	Name      string        `bson:"name" json:"name"`
	CreatedAt time.Time     `bson:"createdAt" json:"createdAt"`
}

type Member struct {
	ID        bson.ObjectID   `bson:"_id" json:"id"`
	Name      string          `bson:"name" json:"name"`
	Email     string          `bson:"email,omitempty" json:"email,omitempty"`
	Role      string          `bson:"role,omitempty" json:"role,omitempty"` // job title — distinct from SystemRole
	TeamIDs   []bson.ObjectID `bson:"teamIds,omitempty" json:"teamIds,omitempty"`
	CreatedAt time.Time       `bson:"createdAt" json:"createdAt"`

	// Credential fields (docs/AUTH_FEATURES.md decision #1): a member
	// without PasswordHash is assignable-only (today's pre-auth behavior);
	// "enable login" upgrades them. PasswordHash never round-trips through
	// JSON (json:"-") — it's only ever set via bcrypt hashing in auth.go.
	PasswordHash       string     `bson:"passwordHash,omitempty" json:"-"`
	SystemRole         string     `bson:"systemRole,omitempty" json:"systemRole,omitempty"`
	MustChangePassword bool       `bson:"mustChangePassword,omitempty" json:"mustChangePassword,omitempty"`
	Disabled           bool       `bson:"disabled,omitempty" json:"disabled,omitempty"`
	LastLoginAt        *time.Time `bson:"lastLoginAt,omitempty" json:"lastLoginAt,omitempty"`
}

type BoardMemberTasks struct {
	Member Member     `json:"member"`
	Tasks  []TaskView `json:"tasks"`
}

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

// ---- store ----

type Store struct {
	client   *mongo.Client
	db       *mongo.Database
	tasks    *mongo.Collection
	teams    *mongo.Collection
	members  *mongo.Collection
	sessions *mongo.Collection
}

func NewStore(ctx context.Context, uri, dbName string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}
	db := client.Database(dbName)
	return &Store{
		client:   client,
		db:       db,
		tasks:    db.Collection("tasks"),
		teams:    db.Collection("teams"),
		members:  db.Collection("members"),
		sessions: db.Collection("sessions"),
	}, nil
}

func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

func (s *Store) EnsureIndexes(ctx context.Context) error {
	_, err := s.tasks.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "horizon", Value: 1}, {Key: "period", Value: 1}}},
		{Keys: bson.D{{Key: "dueDate", Value: 1}}},
		{Keys: bson.D{{Key: "parentId", Value: 1}}},
		{Keys: bson.D{{Key: "teamId", Value: 1}, {Key: "assigneeId", Value: 1}}},
		{Keys: bson.D{{Key: "teamId", Value: 1}, {Key: "weekOf", Value: 1}, {Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "ownerId", Value: 1}}},
		{
			Keys: bson.D{{Key: "seriesId", Value: 1}, {Key: "period", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetPartialFilterExpression(bson.M{"seriesId": bson.M{"$exists": true}}),
		},
		{Keys: bson.D{{Key: "title", Value: "text"}, {Key: "notes", Value: "text"}}},
	})
	if err != nil {
		return fmt.Errorf("task indexes: %w", err)
	}
	_, err = s.teams.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "name", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("team indexes: %w", err)
	}
	_, err = s.members.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "email", Value: 1}},
		Options: options.Index().
			SetUnique(true).
			SetPartialFilterExpression(bson.M{"passwordHash": bson.M{"$exists": true}}),
	})
	if err != nil {
		return fmt.Errorf("member indexes: %w", err)
	}
	_, err = s.sessions.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "token", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	})
	if err != nil {
		return fmt.Errorf("session indexes: %w", err)
	}
	return nil
}

// BackfillWeekOf sets weekOf on team tasks that predate the v6 week-rollover
// feature (docs/DESIGN_V6_WEEK_ROLLOVER.md): weekOf = isoWeek(completedAt ??
// createdAt). Only touches documents where teamId is set and weekOf is
// still missing, so it's idempotent — safe to call on every boot, alongside
// EnsureIndexes. Computed in Go, not via Mongo's $isoWeek aggregation
// operator: ISO week membership depends on the server's local timezone
// (docs/CLAUDE.md: "machine-local timezone everywhere"), which $isoWeek
// can't see. A plain loop is fine — this is a one-time catch-up over a
// small dataset, not a hot path.
func (s *Store) BackfillWeekOf(ctx context.Context) error {
	cur, err := s.tasks.Find(ctx, bson.M{
		"teamId": bson.M{"$exists": true},
		"weekOf": bson.M{"$exists": false},
	})
	if err != nil {
		return fmt.Errorf("backfill weekOf: find: %w", err)
	}
	var tasks []Task
	if err := cur.All(ctx, &tasks); err != nil {
		return fmt.Errorf("backfill weekOf: decode: %w", err)
	}
	for _, t := range tasks {
		basis := t.CreatedAt
		if t.CompletedAt != nil {
			basis = *t.CompletedAt
		}
		week := isoWeekString(basis)
		if _, err := s.tasks.UpdateOne(ctx, bson.M{"_id": t.ID}, bson.M{"$set": bson.M{"weekOf": week}}); err != nil {
			return fmt.Errorf("backfill weekOf: update %s: %w", t.ID.Hex(), err)
		}
	}
	return nil
}

// ---- scoping helpers ----

func containsID(ids []bson.ObjectID, id bson.ObjectID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// sameTeamID reports whether a and b denote the same team (both nil, or
// both non-nil with equal values) — used to detect a patch attempting to
// move a task across the team/personal boundary.
func sameTeamID(a, b *bson.ObjectID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// canAccessTask reports whether u may view or edit t, per
// docs/AUTH_FEATURES.md decisions #4 and #5: personal tasks (teamId nil)
// are visible only to their owner — including ADMIN; team tasks are visible
// to ADMIN unconditionally, and to USER only when they belong to the task's
// team.
func canAccessTask(u *ctxUser, t *Task) bool {
	if t.TeamID == nil {
		return t.OwnerID != nil && *t.OwnerID == u.ID
	}
	if u.SystemRole == RoleAdmin {
		return true
	}
	return containsID(u.TeamIDs, *t.TeamID)
}

// taskScopeFilter is the Mongo filter fragment for "tasks u may see" in
// list-type reads (attention/search/reschedule): own personal tasks, plus
// team tasks u may see (ADMIN: every team task; USER: own teams' tasks
// only). Other users' personal tasks are never included, even for ADMIN.
func taskScopeFilter(u *ctxUser) bson.M {
	personal := bson.M{"ownerId": u.ID}
	var teamCond bson.M
	if u.SystemRole == RoleAdmin {
		teamCond = bson.M{"teamId": bson.M{"$exists": true}}
	} else {
		teamCond = bson.M{"teamId": bson.M{"$in": u.TeamIDs}}
	}
	return bson.M{"$or": []bson.M{personal, teamCond}}
}

// ---- validation ----

// validateTaskFields validates a task's fields and cross-references
// (parent/team/assignee existence, horizon rules). selfID, when non-nil, is
// the task's own id (for patch revalidation), used to reject self-parenting.
func (s *Store) validateTaskFields(ctx context.Context, t *Task, selfID *bson.ObjectID) error {
	if strings.TrimSpace(t.Title) == "" {
		return badRequest("title is required")
	}
	if !validHorizon(t.Horizon) {
		return badRequest("invalid horizon %q", t.Horizon)
	}
	if err := validatePeriod(t.Horizon, t.Period); err != nil {
		return badRequest("%s", err.Error())
	}
	if t.DueDate != "" {
		if _, err := parseDate(t.DueDate); err != nil {
			return badRequest("invalid dueDate: %s", err.Error())
		}
	}
	if !validStatuses[t.Status] {
		return badRequest("invalid status %q", t.Status)
	}
	if !validPriorities[t.Priority] {
		return badRequest("invalid priority %q", t.Priority)
	}

	// Backlog invariants (docs/DESIGN_V7_BACKLOG.md): a backlog task is
	// always personal, undated, and non-recurring — final-state checks, so
	// they cover both create and patch.
	if t.Horizon == HorizonBacklog {
		if t.TeamID != nil {
			return badRequest("backlog tasks are personal")
		}
		if t.DueDate != "" {
			return badRequest("backlog tasks cannot have a due date")
		}
		if t.Recurrence != nil {
			return badRequest("backlog tasks cannot recur")
		}
	}

	if t.ParentID != nil {
		if selfID != nil && *t.ParentID == *selfID {
			return badRequest("task cannot be its own parent")
		}
		var parent Task
		err := s.tasks.FindOne(ctx, bson.M{"_id": *t.ParentID}).Decode(&parent)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return badRequest("parent task not found")
		} else if err != nil {
			return err
		}
		// Personal-task privacy (docs/AUTH_FEATURES.md decision #4) also
		// governs attachment: a caller may only parent a task under one they
		// could access directly — otherwise a USER could inject a child into
		// someone else's private subtree (or a foreign team's task) purely
		// via parentId, bypassing canAccessTask entirely. Checked on every
		// create/patch that carries a parentId, same as the rest of this
		// block's cross-reference validation.
		if u := userFromContext(ctx); u != nil && !canAccessTask(u, &parent) {
			return forbiddenErr("not allowed to attach to that parent")
		}
		parentRank, _ := horizonRank(parent.Horizon)
		childRank, _ := horizonRank(t.Horizon)
		if childRank > parentRank {
			return badRequest("child horizon %q cannot be coarser than parent horizon %q", t.Horizon, parent.Horizon)
		}

		if selfID != nil {
			// Walk up the new parent's ancestor chain; if selfID appears,
			// re-parenting would create a cycle. A visited guard protects
			// against pre-existing bad data forming an unrelated loop.
			visited := map[bson.ObjectID]bool{}
			cur := parent
			for {
				if cur.ID == *selfID {
					return badRequest("parentId would create a cycle")
				}
				if visited[cur.ID] {
					break
				}
				visited[cur.ID] = true
				if cur.ParentID == nil {
					break
				}
				var next Task
				err := s.tasks.FindOne(ctx, bson.M{"_id": *cur.ParentID}).Decode(&next)
				if errors.Is(err, mongo.ErrNoDocuments) {
					break
				} else if err != nil {
					return err
				}
				cur = next
			}
		}
	}

	if t.TeamID != nil {
		cnt, err := s.teams.CountDocuments(ctx, bson.M{"_id": *t.TeamID})
		if err != nil {
			return err
		}
		if cnt == 0 {
			return badRequest("team not found")
		}
	}

	if t.AssigneeID != nil {
		if t.TeamID == nil {
			return badRequest("assigneeId requires teamId")
		}
		var member Member
		err := s.members.FindOne(ctx, bson.M{"_id": *t.AssigneeID}).Decode(&member)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return badRequest("assignee not found")
		} else if err != nil {
			return err
		}
		belongs := false
		for _, tid := range member.TeamIDs {
			if tid == *t.TeamID {
				belongs = true
				break
			}
		}
		if !belongs {
			return badRequest("assignee does not belong to team")
		}
	}

	if t.Recurrence != nil {
		if err := validateRecurrence(t.Recurrence); err != nil {
			return badRequest("%s", err.Error())
		}
		h, _ := recurrenceHorizon(t.Recurrence.Freq)
		if t.Horizon != h {
			return badRequest("recurrence freq %q requires horizon %q", t.Recurrence.Freq, h)
		}
	}
	return nil
}

// ---- task CRUD ----

func (s *Store) CreateTask(ctx context.Context, t *Task) (*TaskView, error) {
	t.ID = bson.NewObjectID()
	t.SeriesID = nil // system-managed; ignore any client-supplied value
	if t.Status == "" {
		t.Status = StatusTodo
	}
	now := time.Now()
	t.CreatedAt, t.UpdatedAt = now, now
	if t.Status == StatusDone {
		t.CompletedAt = &now
	} else {
		t.CompletedAt = nil
	}

	// ownerId is system-managed, never client-settable (docs/AUTH_FEATURES.md
	// decision #3 + #5): personal tasks (teamId nil) are owned by the
	// creating session; team tasks never carry an owner. A USER may only
	// create a team task in a team they belong to (ADMIN: any team).
	if u := userFromContext(ctx); u != nil {
		if t.TeamID == nil {
			oid := u.ID
			t.OwnerID = &oid
		} else {
			t.OwnerID = nil
			if u.SystemRole != RoleAdmin && !containsID(u.TeamIDs, *t.TeamID) {
				return nil, forbiddenErr("not a member of that team")
			}
		}
	}

	// weekOf is system-managed like ownerId: present iff TeamID is set,
	// always the current ISO week on create (docs/DESIGN_V6_WEEK_ROLLOVER.md)
	// — including a task created already-terminal, which is just the create
	// rule applying, no separate transition logic needed. Never
	// client-settable on create; overwrite whatever was sent, if anything.
	if t.TeamID != nil {
		t.WeekOf = isoWeekString(now)
	} else {
		t.WeekOf = ""
	}

	if err := s.validateTaskFields(ctx, t, nil); err != nil {
		return nil, err
	}

	if t.Recurrence != nil {
		t.SeriesID = &t.ID // first instance's _id
		// anchor is server-managed: always the creating task's own period
		// (never client-settable — overwrite whatever was sent, if anything).
		anchor, err := computeAnchor(t.Recurrence.Freq, t.Period)
		if err != nil {
			return nil, err
		}
		t.Recurrence.Anchor = anchor
	}

	if _, err := s.tasks.InsertOne(ctx, t); err != nil {
		return nil, err
	}
	return &TaskView{Task: *t, Progress: Progress{}}, nil
}

func (s *Store) getTaskRaw(ctx context.Context, id bson.ObjectID) (*Task, error) {
	var t Task
	err := s.tasks.FindOne(ctx, bson.M{"_id": id}).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, notFoundErr("task not found")
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) progress(ctx context.Context, parentID bson.ObjectID) (Progress, error) {
	cur, err := s.tasks.Find(ctx, bson.M{"parentId": parentID}, options.Find().SetProjection(bson.M{"status": 1}))
	if err != nil {
		return Progress{}, err
	}
	defer cur.Close(ctx)
	var p Progress
	for cur.Next(ctx) {
		var doc struct {
			Status string `bson:"status"`
		}
		if err := cur.Decode(&doc); err != nil {
			return Progress{}, err
		}
		p.Total++
		if doc.Status == StatusDone || doc.Status == StatusCancelled {
			p.Done++
		}
	}
	return p, cur.Err()
}

func (s *Store) toView(ctx context.Context, t Task) (TaskView, error) {
	p, err := s.progress(ctx, t.ID)
	if err != nil {
		return TaskView{}, err
	}
	return TaskView{Task: t, Progress: p}, nil
}

func (s *Store) toViews(ctx context.Context, tasks []Task) ([]TaskView, error) {
	views := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		v, err := s.toView(ctx, t)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

func (s *Store) findViews(ctx context.Context, filter bson.M) ([]TaskView, error) {
	cur, err := s.tasks.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var tasks []Task
	if err := cur.All(ctx, &tasks); err != nil {
		return nil, err
	}
	return s.toViews(ctx, tasks)
}

// findPersonal is findViews restricted to tasks with no teamId (personal
// planning views only show untargeted tasks), further scoped to the session
// user's own tasks — Day/Week/Month planning views are personal to the
// signed-in caller for EVERYONE, admin included (docs/AUTH_FEATURES.md
// matrix: "Views day/week/month | own personal | own personal").
func (s *Store) findPersonal(ctx context.Context, filter bson.M) ([]TaskView, error) {
	f := bson.M{"teamId": bson.M{"$exists": false}}
	if u := userFromContext(ctx); u != nil {
		f["ownerId"] = u.ID
	}
	for k, v := range filter {
		f[k] = v
	}
	return s.findViews(ctx, f)
}

// Backlog returns the caller's own backlog tasks (findPersonal scopes to
// ownerId, same as the personal planning views), newest-first by createdAt.
// findViews only supports an ascending createdAt sort, so reverse the
// already-fetched slice rather than adding a sort parameter.
func (s *Store) Backlog(ctx context.Context) ([]TaskView, error) {
	tasks, err := s.findPersonal(ctx, bson.M{"horizon": HorizonBacklog})
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(tasks)-1; i < j; i, j = i+1, j-1 {
		tasks[i], tasks[j] = tasks[j], tasks[i]
	}
	return tasks, nil
}

func (s *Store) GetTaskDetail(ctx context.Context, id bson.ObjectID) (*TaskDetail, error) {
	t, err := s.getTaskRaw(ctx, id)
	if err != nil {
		return nil, err
	}
	u := userFromContext(ctx)
	if u != nil && !canAccessTask(u, t) {
		return nil, forbiddenErr("not allowed")
	}
	view, err := s.toView(ctx, *t)
	if err != nil {
		return nil, err
	}
	cur, err := s.tasks.Find(ctx, bson.M{"parentId": id}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var children []Task
	if err := cur.All(ctx, &children); err != nil {
		return nil, err
	}
	// Personal-task privacy (docs/AUTH_FEATURES.md decision #4) applies to
	// children too: a caller must not see a child they couldn't access
	// directly — e.g. a foreign USER's personal task parented under a team
	// task the caller can otherwise see.
	if u != nil {
		visible := make([]Task, 0, len(children))
		for _, c := range children {
			if canAccessTask(u, &c) {
				visible = append(visible, c)
			}
		}
		children = visible
	}
	childViews, err := s.toViews(ctx, children)
	if err != nil {
		return nil, err
	}
	return &TaskDetail{TaskView: view, Children: childViews}, nil
}

// PatchTask applies a partial JSON update onto the existing task (fields
// absent from raw are left unchanged, since json.Unmarshal only overwrites
// keys present in the input) and revalidates the merged result.
func (s *Store) PatchTask(ctx context.Context, id bson.ObjectID, raw []byte) (*TaskView, error) {
	t, err := s.getTaskRaw(ctx, id)
	if err != nil {
		return nil, err
	}
	orig := *t
	u := userFromContext(ctx)
	if u != nil && !canAccessTask(u, &orig) {
		return nil, forbiddenErr("not allowed")
	}
	now := time.Now()

	// weekOf is a plain string field: json.Unmarshal only overwrites keys
	// present in the input, but comparing the merged value against orig
	// can't distinguish "the client explicitly resent the current value"
	// from "the client omitted it" — so detect an attempted patch via raw
	// key presence instead (docs/DESIGN_V6_WEEK_ROLLOVER.md's ADMIN-only
	// weekOf patch, checked below once teamId/status merging settles).
	var patchFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patchFields); err != nil {
		return nil, badRequest("invalid JSON: %s", err.Error())
	}
	// json.Unmarshal onto the Task struct matches field names case-
	// insensitively, so a differently-cased key (e.g. "weekof") would still
	// land in t.WeekOf; detect presence the same way here or the check below
	// can be bypassed by a non-canonical key.
	weekOfPatched := false
	for k := range patchFields {
		if strings.EqualFold(k, "weekOf") {
			weekOfPatched = true
			break
		}
	}

	// Recurrence is a pointer field: json.Unmarshal decodes onto the struct
	// it already points to (merging present keys in place) rather than
	// replacing the pointer, so capture an independent value copy now —
	// before unmarshal mutates it — to detect freq/interval changes below.
	// cloneRecurrence also deep-copies the Interval sub-field (itself a
	// pointer), otherwise an interval-only change would never be detected.
	origRecurrence := cloneRecurrence(t.Recurrence)
	if err := json.Unmarshal(raw, t); err != nil {
		return nil, badRequest("invalid JSON: %s", err.Error())
	}
	// Immutable/system-managed fields: never client-settable via patch.
	t.ID = orig.ID
	t.SeriesID = orig.SeriesID
	t.CreatedAt = orig.CreatedAt

	// A USER may not move a task across the team/personal boundary at all —
	// only ADMIN can (docs/AUTH_FEATURES.md matrix: task DELETE/team-
	// management-adjacent powers are ADMIN-only; otherwise a USER could
	// launder a team task into a personal one to escape admin oversight, or
	// hop it into a team they don't belong to). Covers team->personal,
	// personal->team, and team->other-team; ADMIN's ownerId auto-derive
	// below is unaffected.
	if u != nil && u.SystemRole != RoleAdmin && !sameTeamID(orig.TeamID, t.TeamID) {
		return nil, forbiddenErr("only an admin can change a task's team")
	}

	// ownerId mirrors the personal/team invariant (docs/AUTH_FEATURES.md
	// decision #3: "Personal task = ownerId set + teamId null") and is
	// never client-settable. Re-derive it only when the patch flips
	// teamId's nil-ness; otherwise it's immutable. weekOf mirrors the same
	// flip (docs/DESIGN_V6_WEEK_ROLLOVER.md): personal->team assigns the
	// current week (like a fresh team-task create); team->personal unsets
	// it, since weekOf has no meaning off the team board.
	switch {
	case orig.TeamID == nil && t.TeamID != nil: // personal -> team
		t.OwnerID = nil
		t.WeekOf = isoWeekString(now)
	case orig.TeamID != nil && t.TeamID == nil: // team -> personal
		if u != nil {
			oid := u.ID
			t.OwnerID = &oid
		} else {
			t.OwnerID = orig.OwnerID
		}
		t.WeekOf = ""
	default:
		t.OwnerID = orig.OwnerID
	}
	if u != nil && u.SystemRole != RoleAdmin && t.TeamID != nil && !containsID(u.TeamIDs, *t.TeamID) {
		return nil, forbiddenErr("not a member of that team")
	}

	// weekOf: system-managed except for the ADMIN-only direct-patch
	// primitive rollover uses as its "move to this week" / undo action
	// (docs/DESIGN_V6_WEEK_ROLLOVER.md). An explicit patch attempt requires
	// ADMIN, a team task, and a well-formed ISO week key; it overrides
	// whatever the team/personal-flip switch above just set. USER may never
	// touch weekOf, even on their own team's tasks.
	if weekOfPatched {
		if u != nil && u.SystemRole != RoleAdmin {
			return nil, forbiddenErr("only an admin can change weekOf")
		}
		if t.TeamID == nil {
			return nil, badRequest("weekOf is not valid on a personal task")
		}
		if _, _, err := parseISOWeek(t.WeekOf); err != nil {
			return nil, badRequest("invalid weekOf: %s", err.Error())
		}
	}

	switch {
	case orig.Status != StatusDone && t.Status == StatusDone:
		t.CompletedAt = &now
	case orig.Status == StatusDone && t.Status != StatusDone:
		t.CompletedAt = nil
	default:
		t.CompletedAt = orig.CompletedAt
	}
	t.UpdatedAt = now

	// Transition into terminal (done/cancelled) bumps a team task's weekOf
	// to the current week, unconditionally — "the week a team task last
	// mattered" (docs/DESIGN_V6_WEEK_ROLLOVER.md) — overriding whatever the
	// explicit-patch check above just validated. Transitions back out of
	// terminal leave weekOf as-is (no action needed).
	wasTerminal := orig.Status == StatusDone || orig.Status == StatusCancelled
	isTerminal := t.Status == StatusDone || t.Status == StatusCancelled
	if !wasTerminal && isTerminal && t.TeamID != nil {
		t.WeekOf = isoWeekString(now)
	}

	if err := s.validateTaskFields(ctx, t, &id); err != nil {
		return nil, err
	}

	if t.Horizon != orig.Horizon {
		if err := s.validateChildrenHorizon(ctx, id, t.Horizon); err != nil {
			return nil, err
		}
	}

	// Recurrence anchor: server-managed, never client-settable. Re-anchor
	// to the patched instance's (possibly also-patched) period when freq or
	// interval changed; otherwise keep the original anchor, even if the
	// patch touched other recurrence fields (weekdays/dayOfMonth) or tried
	// to set anchor directly.
	if t.Recurrence != nil {
		changed := origRecurrence == nil ||
			origRecurrence.Freq != t.Recurrence.Freq ||
			effectiveInterval(origRecurrence) != effectiveInterval(t.Recurrence)
		if changed {
			anchor, err := computeAnchor(t.Recurrence.Freq, t.Period)
			if err != nil {
				return nil, err
			}
			t.Recurrence.Anchor = anchor
		} else {
			t.Recurrence.Anchor = origRecurrence.Anchor
		}
	}

	if _, err := s.tasks.ReplaceOne(ctx, bson.M{"_id": id}, t); err != nil {
		return nil, err
	}
	view, err := s.toView(ctx, *t)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// validateChildrenHorizon returns a 400 error if any direct child of
// parentID has a horizon finer-grained than newHorizon — enforced when a
// patch changes the parent's horizon, since child.rank must stay <=
// parent.rank.
func (s *Store) validateChildrenHorizon(ctx context.Context, parentID bson.ObjectID, newHorizon string) error {
	newRank, _ := horizonRank(newHorizon)
	cur, err := s.tasks.Find(ctx, bson.M{"parentId": parentID}, options.Find().SetProjection(bson.M{"horizon": 1}))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var doc struct {
			Horizon string `bson:"horizon"`
		}
		if err := cur.Decode(&doc); err != nil {
			return err
		}
		childRank, _ := horizonRank(doc.Horizon)
		if childRank > newRank {
			return badRequest("horizon %q is coarser than an existing child's horizon %q", newHorizon, doc.Horizon)
		}
	}
	return cur.Err()
}

// DeleteTaskCascade deletes id and its full descendant subtree, returning
// the deleted docs (root first, then descendants breadth-first) as
// TaskViews — the same shape task JSON has everywhere else — so the caller
// can power an undo (POST /api/tasks/restore). Progress is computed before
// the delete, while descendants still exist to be counted.
func (s *Store) DeleteTaskCascade(ctx context.Context, id bson.ObjectID) ([]TaskView, error) {
	// DELETE is route-gated to ADMIN with no further scoping in the matrix
	// ("Task DELETE + restore: ADMIN only", unqualified — unlike PATCH's
	// "any" cell, which decision #4's privacy rule narrows). Deliberately
	// NOT running canAccessTask here: personal-task privacy governs
	// visibility/listing, not admin moderation capability — an admin must
	// be able to delete any task, including another user's personal task,
	// or those tasks would become permanently undeletable (USER has no
	// delete power at all, even over their own tasks, per the matrix).
	root, err := s.getTaskRaw(ctx, id)
	if err != nil {
		return nil, err
	}
	subtree := []Task{*root}
	visited := map[bson.ObjectID]bool{id: true}
	queue := []bson.ObjectID{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		c, err := s.tasks.Find(ctx, bson.M{"parentId": cur})
		if err != nil {
			return nil, err
		}
		var kids []Task
		if err := c.All(ctx, &kids); err != nil {
			return nil, err
		}
		for _, k := range kids {
			if visited[k.ID] {
				continue
			}
			visited[k.ID] = true
			subtree = append(subtree, k)
			queue = append(queue, k.ID)
		}
	}

	views, err := s.toViews(ctx, subtree)
	if err != nil {
		return nil, err
	}

	ids := make([]bson.ObjectID, len(subtree))
	for i, t := range subtree {
		ids[i] = t.ID
	}
	if _, err := s.tasks.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}}); err != nil {
		return nil, err
	}
	return views, nil
}

// RestoreTasks reinserts previously-deleted task documents with their
// original ids (as produced by DeleteTaskCascade / the DELETE response).
// Insertion is unordered so one bad or already-present doc doesn't block
// the rest; duplicate-key errors are ignored (silently skipped — e.g. a
// restore replayed twice, or a doc that was never actually deleted).  Any
// other write error is surfaced. Returns the count of documents actually
// inserted.
func (s *Store) RestoreTasks(ctx context.Context, tasks []Task) (int, error) {
	if len(tasks) == 0 {
		return 0, nil
	}
	docs := make([]any, len(tasks))
	for i, t := range tasks {
		docs[i] = t
	}
	res, err := s.tasks.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	inserted := 0
	if res != nil {
		inserted = len(res.InsertedIDs)
	}
	if err == nil {
		return inserted, nil
	}
	var bwe mongo.BulkWriteException
	if !errors.As(err, &bwe) {
		return inserted, err
	}
	for _, we := range bwe.WriteErrors {
		if !mongo.IsDuplicateKeyError(we) {
			return inserted, err
		}
	}
	return inserted, nil // every failure was a duplicate key: ignore them all
}

func (s *Store) Reschedule(ctx context.Context, ids []bson.ObjectID) (int, error) {
	u := userFromContext(ctx)
	updated := 0
	for _, id := range ids {
		t, err := s.getTaskRaw(ctx, id)
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) && ae.status == 404 {
				continue
			}
			return updated, err
		}
		if u != nil && !canAccessTask(u, t) {
			continue // out of the caller's visible scope: skip silently, like a missing id
		}
		if t.Horizon == HorizonBacklog {
			continue // no period to reschedule to (docs/DESIGN_V7_BACKLOG.md): a no-op, not a real update
		}
		set := bson.M{"period": currentPeriod(t.Horizon), "updatedAt": time.Now()}
		if t.DueDate != "" {
			set["dueDate"] = currentPeriod(HorizonDaily)
		}
		res, err := s.tasks.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set})
		if err != nil {
			return updated, err
		}
		if res.MatchedCount > 0 {
			updated++
		}
	}
	return updated, nil
}

// ---- recurrence materialization ----

// Materialize lazily/idempotently spawns missing instances for every
// recurring series, up to "now"'s period. Called before every read that
// surfaces tasks (views, attention, search, team board).
func (s *Store) Materialize(ctx context.Context) error {
	cur, err := s.tasks.Find(ctx, bson.M{"seriesId": bson.M{"$exists": true}})
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	latest := map[bson.ObjectID]Task{}
	for cur.Next(ctx) {
		var t Task
		if err := cur.Decode(&t); err != nil {
			return err
		}
		if t.SeriesID == nil {
			continue
		}
		if ex, ok := latest[*t.SeriesID]; !ok || t.Period > ex.Period {
			latest[*t.SeriesID] = t
		}
	}
	if err := cur.Err(); err != nil {
		return err
	}

	now := time.Now()
	for _, t := range latest {
		if t.Recurrence == nil {
			continue // recurrence removed from the live instance: series ended
		}
		horizon, ok := recurrenceHorizon(t.Recurrence.Freq)
		if !ok {
			continue
		}
		cp := currentPeriodAt(horizon, now)
		periods, err := missingPeriods(t.Recurrence, horizon, t.Period, cp)
		if err != nil {
			return err
		}
		for _, p := range periods {
			spawn := &Task{
				ID:         bson.NewObjectID(),
				Title:      t.Title,
				Notes:      t.Notes,
				Horizon:    horizon,
				Period:     p,
				Status:     StatusTodo,
				Priority:   t.Priority,
				OwnerID:    t.OwnerID, // docs/AUTH_FEATURES.md: spawned instances copy ownerId from the series task
				TeamID:     t.TeamID,
				AssigneeID: t.AssigneeID,
				Recurrence: t.Recurrence,
				SeriesID:   t.SeriesID,
				CreatedAt:  now,
				UpdatedAt:  now,
				// dueDate NOT copied; parentId NOT copied (per DESIGN.md).
			}
			if d := dueDateForSpawn(t.Recurrence, horizon, p); d != "" {
				spawn.DueDate = d
			}
			if spawn.TeamID != nil {
				// docs/DESIGN_V6_WEEK_ROLLOVER.md: spawned team-task
				// instances start life in the current week, same as a fresh
				// team-task create.
				spawn.WeekOf = isoWeekString(now)
			}
			if _, err := s.tasks.InsertOne(ctx, spawn); err != nil && !mongo.IsDuplicateKeyError(err) {
				return err
			}
		}
	}
	return nil
}

// ---- views ----

func (s *Store) ViewDay(ctx context.Context, date string) (tasks, weekContext []TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, err
	}
	if err = validatePeriod(HorizonDaily, date); err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	tasks, err = s.findPersonal(ctx, bson.M{"horizon": HorizonDaily, "period": date})
	if err != nil {
		return nil, nil, err
	}
	week, err := weekOfDate(date)
	if err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	weekContext, err = s.findPersonal(ctx, bson.M{"horizon": HorizonWeekly, "period": week})
	return tasks, weekContext, err
}

func (s *Store) ViewWeek(ctx context.Context, week string) (tasks []TaskView, days map[string][]TaskView, monthContext []TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, nil, err
	}
	if err = validatePeriod(HorizonWeekly, week); err != nil {
		return nil, nil, nil, badRequest("%s", err.Error())
	}
	tasks, err = s.findPersonal(ctx, bson.M{"horizon": HorizonWeekly, "period": week})
	if err != nil {
		return nil, nil, nil, err
	}
	dates, err := datesInWeek(week)
	if err != nil {
		return nil, nil, nil, badRequest("%s", err.Error())
	}
	days = map[string][]TaskView{}
	for _, d := range dates {
		dv, err := s.findPersonal(ctx, bson.M{"horizon": HorizonDaily, "period": d})
		if err != nil {
			return nil, nil, nil, err
		}
		days[d] = dv
	}
	month, err := monthOfWeek(week)
	if err != nil {
		return nil, nil, nil, badRequest("%s", err.Error())
	}
	monthContext, err = s.findPersonal(ctx, bson.M{"horizon": HorizonMonthly, "period": month})
	return tasks, days, monthContext, err
}

func (s *Store) ViewMonth(ctx context.Context, month string) (tasks []TaskView, weeks map[string][]TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, err
	}
	if err = validatePeriod(HorizonMonthly, month); err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	tasks, err = s.findPersonal(ctx, bson.M{"horizon": HorizonMonthly, "period": month})
	if err != nil {
		return nil, nil, err
	}
	ws, err := weeksInMonth(month)
	if err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	weeks = map[string][]TaskView{}
	for _, w := range ws {
		dates, err := datesInWeek(w)
		if err != nil {
			return nil, nil, badRequest("%s", err.Error())
		}
		// Month-view daily rollup (docs/DESIGN_V7_BACKLOG.md): widen the
		// per-week filter to also pick up daily tasks whose date falls inside
		// this month — a month-spanning week's daily tasks land in only the
		// month their date belongs to, even though the week bucket itself
		// (weeksInMonth) appears in both months.
		var monthDates []string
		for _, d := range dates {
			if strings.HasPrefix(d, month) {
				monthDates = append(monthDates, d)
			}
		}
		wv, err := s.findPersonal(ctx, bson.M{"$or": []bson.M{
			{"horizon": HorizonWeekly, "period": w},
			{"horizon": HorizonDaily, "period": bson.M{"$in": monthDates}},
		}})
		if err != nil {
			return nil, nil, err
		}
		weeks[w] = wv
	}
	return tasks, weeks, nil
}

// ViewAttention returns overdue + slipped tasks, scoped per
// docs/AUTH_FEATURES.md matrix ("Attention | own personal + ALL team tasks
// | own personal + OWN teams' tasks"): own personal tasks plus visible team
// tasks (ADMIN: every team; USER: own teams only). Another user's personal
// tasks never appear, even for ADMIN.
func (s *Store) ViewAttention(ctx context.Context) (overdue, slipped []TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, err
	}
	today := currentPeriod(HorizonDaily)
	overdueFilter := bson.M{
		"dueDate": bson.M{"$exists": true, "$lt": today},
		"status":  bson.M{"$in": []string{StatusTodo, StatusInProgress}},
	}
	slippedFilter := bson.M{
		"dueDate": bson.M{"$exists": false},
		"status":  bson.M{"$in": []string{StatusTodo, StatusInProgress}},
		"$or": []bson.M{
			{"horizon": HorizonDaily, "period": bson.M{"$lt": today}},
			{"horizon": HorizonWeekly, "period": bson.M{"$lt": currentPeriod(HorizonWeekly)}},
			{"horizon": HorizonMonthly, "period": bson.M{"$lt": currentPeriod(HorizonMonthly)}},
		},
	}
	if u := userFromContext(ctx); u != nil {
		scope := taskScopeFilter(u)
		overdueFilter = bson.M{"$and": []bson.M{overdueFilter, scope}}
		slippedFilter = bson.M{"$and": []bson.M{slippedFilter, scope}}
	}
	overdue, err = s.findViews(ctx, overdueFilter)
	if err != nil {
		return nil, nil, err
	}
	slipped, err = s.findViews(ctx, slippedFilter)
	return overdue, slipped, err
}

// ---- search ----

type SearchParams struct {
	Q, Status, Priority, Horizon string
	TeamID, AssigneeID           *bson.ObjectID
	Overdue                      bool
}

func (s *Store) Search(ctx context.Context, p SearchParams) ([]TaskView, error) {
	if err := s.Materialize(ctx); err != nil {
		return nil, err
	}
	var conds []bson.M
	if p.Q != "" {
		conds = append(conds, bson.M{"$text": bson.M{"$search": p.Q}})
	}
	if p.Status == "open" {
		conds = append(conds, bson.M{"status": bson.M{"$in": []string{StatusTodo, StatusInProgress}}})
	} else if p.Status != "" {
		conds = append(conds, bson.M{"status": p.Status})
	}
	if p.Priority != "" {
		conds = append(conds, bson.M{"priority": p.Priority})
	}
	if p.Horizon != "" {
		conds = append(conds, bson.M{"horizon": p.Horizon})
	} else {
		// Backlog tasks are hidden from Search/All-Tasks by default
		// (docs/DESIGN_V7_BACKLOG.md) — only an explicit horizon=backlog opts
		// in, which the branch above already handles.
		conds = append(conds, bson.M{"horizon": bson.M{"$ne": HorizonBacklog}})
	}
	if p.TeamID != nil {
		conds = append(conds, bson.M{"teamId": *p.TeamID})
	}
	if p.AssigneeID != nil {
		conds = append(conds, bson.M{"assigneeId": *p.AssigneeID})
	}
	if p.Overdue {
		conds = append(conds, bson.M{
			"dueDate": bson.M{"$exists": true, "$lt": currentPeriod(HorizonDaily)},
			"status":  bson.M{"$in": []string{StatusTodo, StatusInProgress}},
		})
	}
	// Scoped per docs/AUTH_FEATURES.md matrix, same rule as Attention: own
	// personal + visible team tasks (ADMIN: all teams; USER: own teams).
	if u := userFromContext(ctx); u != nil {
		conds = append(conds, taskScopeFilter(u))
	}
	var filter bson.M
	switch len(conds) {
	case 0:
		filter = bson.M{}
	case 1:
		filter = conds[0]
	default:
		filter = bson.M{"$and": conds}
	}
	return s.findViews(ctx, filter)
}

// ---- teams ----

func (s *Store) CreateTeam(ctx context.Context, name string) (*Team, error) {
	if strings.TrimSpace(name) == "" {
		return nil, badRequest("name is required")
	}
	t := &Team{ID: bson.NewObjectID(), Name: name, CreatedAt: time.Now()}
	_, err := s.teams.InsertOne(ctx, t)
	if mongo.IsDuplicateKeyError(err) {
		return nil, conflictErr("team name already exists")
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ListTeams returns every team for ADMIN, or only the caller's own teams
// for USER (docs/AUTH_FEATURES.md matrix: "GET /api/teams | all teams |
// own teams only").
func (s *Store) ListTeams(ctx context.Context) ([]Team, error) {
	filter := bson.M{}
	if u := userFromContext(ctx); u != nil && u.SystemRole != RoleAdmin {
		filter["_id"] = bson.M{"$in": u.TeamIDs}
	}
	cur, err := s.teams.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var teams []Team
	if err := cur.All(ctx, &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

// TeamView is a Team plus derived member/task counts for the teams landing
// page.
type TeamView struct {
	Team
	MemberCount  int `json:"memberCount"`
	OpenCount    int `json:"openCount"`
	OverdueCount int `json:"overdueCount"`
}

// ListTeamsWithCounts returns every team plus memberCount (members whose
// teamIds include the team), openCount (open tasks with that teamId), and
// overdueCount (of those, past-due). Materializes recurring instances first
// so counts see spawned tasks, same as every other read path.
func (s *Store) ListTeamsWithCounts(ctx context.Context) ([]TeamView, error) {
	if err := s.Materialize(ctx); err != nil {
		return nil, err
	}
	teams, err := s.ListTeams(ctx)
	if err != nil {
		return nil, err
	}
	today := currentPeriod(HorizonDaily)
	views := make([]TeamView, 0, len(teams))
	for _, t := range teams {
		memberCount, err := s.members.CountDocuments(ctx, bson.M{"teamIds": t.ID})
		if err != nil {
			return nil, err
		}
		openFilter := bson.M{
			"teamId": t.ID,
			"status": bson.M{"$in": []string{StatusTodo, StatusInProgress}},
		}
		openCount, err := s.tasks.CountDocuments(ctx, openFilter)
		if err != nil {
			return nil, err
		}
		overdueFilter := bson.M{
			"teamId":  t.ID,
			"status":  bson.M{"$in": []string{StatusTodo, StatusInProgress}},
			"dueDate": bson.M{"$exists": true, "$lt": today},
		}
		overdueCount, err := s.tasks.CountDocuments(ctx, overdueFilter)
		if err != nil {
			return nil, err
		}
		views = append(views, TeamView{
			Team:         t,
			MemberCount:  int(memberCount),
			OpenCount:    int(openCount),
			OverdueCount: int(overdueCount),
		})
	}
	return views, nil
}

func (s *Store) PatchTeam(ctx context.Context, id bson.ObjectID, name string) (*Team, error) {
	if strings.TrimSpace(name) == "" {
		return nil, badRequest("name is required")
	}
	res, err := s.teams.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"name": name}})
	if mongo.IsDuplicateKeyError(err) {
		return nil, conflictErr("team name already exists")
	}
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, notFoundErr("team not found")
	}
	var team Team
	if err := s.teams.FindOne(ctx, bson.M{"_id": id}).Decode(&team); err != nil {
		return nil, err
	}
	return &team, nil
}

func (s *Store) DeleteTeam(ctx context.Context, id bson.ObjectID) error {
	count, err := s.tasks.CountDocuments(ctx, bson.M{"teamId": id})
	if err != nil {
		return err
	}
	if count > 0 {
		return conflictErr("team has tasks")
	}
	res, err := s.teams.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return notFoundErr("team not found")
	}
	return nil
}

// ---- members ----

func (s *Store) validateTeamIDs(ctx context.Context, teamIDs []bson.ObjectID) error {
	for _, tid := range teamIDs {
		cnt, err := s.teams.CountDocuments(ctx, bson.M{"_id": tid})
		if err != nil {
			return err
		}
		if cnt == 0 {
			return badRequest("team not found: %s", tid.Hex())
		}
	}
	return nil
}

func (s *Store) CreateMember(ctx context.Context, m *Member) (*Member, error) {
	if strings.TrimSpace(m.Name) == "" {
		return nil, badRequest("name is required")
	}
	if err := s.validateTeamIDs(ctx, m.TeamIDs); err != nil {
		return nil, err
	}
	m.ID = bson.NewObjectID()
	m.CreatedAt = time.Now()
	if _, err := s.members.InsertOne(ctx, m); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, conflictErr("email already in use")
		}
		return nil, err
	}
	return m, nil
}

func (s *Store) ListMembers(ctx context.Context, teamID *bson.ObjectID) ([]Member, error) {
	filter := bson.M{}
	if teamID != nil {
		filter["teamIds"] = *teamID
	}
	cur, err := s.members.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var members []Member
	if err := cur.All(ctx, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// PatchMember applies a partial JSON update (existing member fields, plus
// docs/AUTH_FEATURES.md-added systemRole/disabled — this whole route is
// ADMIN-only, gated at registration in routes()). Disabling a previously
// enabled member kills all of their sessions immediately (decision #8/#9).
func (s *Store) PatchMember(ctx context.Context, id bson.ObjectID, raw []byte) (*Member, error) {
	var m Member
	err := s.members.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, notFoundErr("member not found")
	}
	if err != nil {
		return nil, err
	}
	wasDisabled := m.Disabled
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, badRequest("invalid JSON: %s", err.Error())
	}
	m.ID = id
	if strings.TrimSpace(m.Name) == "" {
		return nil, badRequest("name is required")
	}
	if m.SystemRole != "" && !validSystemRole(m.SystemRole) {
		return nil, badRequest("invalid systemRole %q", m.SystemRole)
	}
	if err := s.validateTeamIDs(ctx, m.TeamIDs); err != nil {
		return nil, err
	}
	if _, err := s.members.ReplaceOne(ctx, bson.M{"_id": id}, &m); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, conflictErr("email already in use")
		}
		return nil, err
	}
	if m.Disabled && !wasDisabled {
		if err := s.DeleteSessionsForUser(ctx, id); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

// DeleteMember hard-deletes a member record. Login-enabled members (those
// with a passwordHash) cannot be hard-deleted — docs/AUTH_FEATURES.md
// decision #8: "No hard delete of login-enabled users in v4"; disable
// instead, which blocks login, kills sessions, and keeps task history. This
// mirrors DeleteTeam's "team has tasks" 409 pattern. Assignable-only
// (never-enabled-login) records keep today's hard-delete behavior. Any
// successful deletion also cascades to that member's sessions (tidy-up;
// login-less members realistically have none, but harmless either way).
func (s *Store) DeleteMember(ctx context.Context, id bson.ObjectID) error {
	var m Member
	err := s.members.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return notFoundErr("member not found")
	}
	if err != nil {
		return err
	}
	if m.PasswordHash != "" {
		return conflictErr("member has login enabled; disable instead of deleting")
	}
	if _, err := s.tasks.UpdateMany(ctx, bson.M{"assigneeId": id}, bson.M{"$unset": bson.M{"assigneeId": ""}}); err != nil {
		return err
	}
	if _, err := s.members.DeleteOne(ctx, bson.M{"_id": id}); err != nil {
		return err
	}
	return s.DeleteSessionsForUser(ctx, id)
}

// ---- team board ----

// boardVisibilityFilter is the Mongo filter fragment for "task state visible
// on the team board this week" (docs/DESIGN_V6_WEEK_ROLLOVER.md, W = current
// ISO week): open tasks (todo/in_progress) with a dueDate are always
// visible; open tasks without one only if weekOf == W; terminal
// (done/cancelled) tasks only if weekOf == W (the completed fold).
func boardVisibilityFilter(W string) bson.M {
	return bson.M{
		"$or": []bson.M{
			{
				"status": bson.M{"$in": []string{StatusTodo, StatusInProgress}},
				"$or": []bson.M{
					{"dueDate": bson.M{"$exists": true}},
					{"weekOf": W},
				},
			},
			{
				"status": bson.M{"$in": []string{StatusDone, StatusCancelled}},
				"weekOf": W,
			},
		},
	}
}

// TeamBoard returns a team's board. USER callers must belong to the team
// (docs/AUTH_FEATURES.md matrix: "GET /api/teams/{id}/board | any team |
// own teams only"); ADMIN may view any team's board. Task visibility is
// further scoped to the current week per boardVisibilityFilter
// (docs/DESIGN_V6_WEEK_ROLLOVER.md) — note this narrows what's shown, not
// what's counted: progress() (per-task subtask counts) stays unscoped by
// design, and hidden stale tasks remain reachable via Search/All-Tasks/
// Attention, whose scoping is untouched.
func (s *Store) TeamBoard(ctx context.Context, teamID bson.ObjectID) (*Board, error) {
	if err := s.Materialize(ctx); err != nil {
		return nil, err
	}
	if u := userFromContext(ctx); u != nil && u.SystemRole != RoleAdmin && !containsID(u.TeamIDs, teamID) {
		return nil, forbiddenErr("not a member of that team")
	}
	var team Team
	err := s.teams.FindOne(ctx, bson.M{"_id": teamID}).Decode(&team)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, notFoundErr("team not found")
	}
	if err != nil {
		return nil, err
	}

	members, err := s.ListMembers(ctx, &teamID)
	if err != nil {
		return nil, err
	}

	W := currentPeriod(HorizonWeekly)
	visibility := boardVisibilityFilter(W)

	board := &Board{Team: team, Members: []BoardMemberTasks{}, Week: W}
	for _, m := range members {
		filter := bson.M{"$and": []bson.M{{"teamId": teamID, "assigneeId": m.ID}, visibility}}
		tasks, err := s.findViews(ctx, filter)
		if err != nil {
			return nil, err
		}
		sortBoardTasks(tasks)
		board.Members = append(board.Members, BoardMemberTasks{Member: m, Tasks: tasks})
	}
	unassignedFilter := bson.M{"$and": []bson.M{
		{"teamId": teamID, "assigneeId": bson.M{"$exists": false}},
		visibility,
	}}
	unassigned, err := s.findViews(ctx, unassignedFilter)
	if err != nil {
		return nil, err
	}
	sortBoardTasks(unassigned)
	board.Unassigned = unassigned

	staleOpen, err := s.tasks.CountDocuments(ctx, bson.M{
		"teamId":  teamID,
		"status":  bson.M{"$in": []string{StatusTodo, StatusInProgress}},
		"dueDate": bson.M{"$exists": false},
		"weekOf":  bson.M{"$lt": W},
	})
	if err != nil {
		return nil, err
	}
	board.StaleOpen = int(staleOpen)

	return board, nil
}

// TeamRollover returns a team's rollover candidates (docs/DESIGN_V6_WEEK_ROLLOVER.md):
// open, undated tasks whose weekOf is before the current week, sorted
// weekOf asc then createdAt asc. Route-gated to requireAdmin with no
// further team-membership scoping — an ADMIN may roll over any team, same
// as TeamBoard's ADMIN carve-out.
func (s *Store) TeamRollover(ctx context.Context, teamID bson.ObjectID) (week string, tasks []TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return "", nil, err
	}
	var team Team
	err = s.teams.FindOne(ctx, bson.M{"_id": teamID}).Decode(&team)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil, notFoundErr("team not found")
	}
	if err != nil {
		return "", nil, err
	}

	W := currentPeriod(HorizonWeekly)
	filter := bson.M{
		"teamId":  teamID,
		"status":  bson.M{"$in": []string{StatusTodo, StatusInProgress}},
		"dueDate": bson.M{"$exists": false},
		"weekOf":  bson.M{"$lt": W},
	}
	cur, err := s.tasks.Find(ctx, filter, options.Find().SetSort(bson.D{
		{Key: "weekOf", Value: 1},
		{Key: "createdAt", Value: 1},
	}))
	if err != nil {
		return "", nil, err
	}
	var rawTasks []Task
	if err := cur.All(ctx, &rawTasks); err != nil {
		return "", nil, err
	}
	views, err := s.toViews(ctx, rawTasks)
	if err != nil {
		return "", nil, err
	}
	return W, views, nil
}

// defaultHistoryLimit/maxHistoryLimit bound TeamHistory's page size
// (docs/DESIGN_V6_WEEK_ROLLOVER.md: "limit default 50, max 200").
const (
	defaultHistoryLimit = 50
	maxHistoryLimit     = 200
)

// TeamHistory returns a team's completed-task history strictly before the
// current week (current-week completions live on the board's completed
// fold instead — docs/DESIGN_V6_WEEK_ROLLOVER.md), newest-first by
// createdAt, offset/limit paginated — the codebase's first pagination.
// Scoped like TeamBoard: ADMIN any team, USER own teams only. hasMore is
// computed by fetching one extra row past limit.
func (s *Store) TeamHistory(ctx context.Context, teamID bson.ObjectID, offset, limit int) (tasks []TaskView, hasMore bool, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, false, err
	}
	if u := userFromContext(ctx); u != nil && u.SystemRole != RoleAdmin && !containsID(u.TeamIDs, teamID) {
		return nil, false, forbiddenErr("not a member of that team")
	}
	var team Team
	err = s.teams.FindOne(ctx, bson.M{"_id": teamID}).Decode(&team)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, false, notFoundErr("team not found")
	}
	if err != nil {
		return nil, false, err
	}

	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	if limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}
	if offset < 0 {
		offset = 0
	}

	W := currentPeriod(HorizonWeekly)
	filter := bson.M{
		"teamId": teamID,
		"status": bson.M{"$in": []string{StatusDone, StatusCancelled}},
		"weekOf": bson.M{"$lt": W},
	}
	cur, err := s.tasks.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetSkip(int64(offset)).
		SetLimit(int64(limit+1)))
	if err != nil {
		return nil, false, err
	}
	var rawTasks []Task
	if err := cur.All(ctx, &rawTasks); err != nil {
		return nil, false, err
	}
	hasMore = len(rawTasks) > limit
	if hasMore {
		rawTasks = rawTasks[:limit]
	}
	views, err := s.toViews(ctx, rawTasks)
	if err != nil {
		return nil, false, err
	}
	return views, hasMore, nil
}

var priorityRank = map[string]int{"high": 3, "medium": 2, "low": 1, "": 0}

// sortBoardTasks sorts in place: open before closed, then priority desc,
// then dueDate ascending (tasks without a dueDate sort last).
func sortBoardTasks(tasks []TaskView) {
	closed := func(status string) bool { return status == StatusDone || status == StatusCancelled }
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if closed(a.Status) != closed(b.Status) {
			return !closed(a.Status)
		}
		if priorityRank[a.Priority] != priorityRank[b.Priority] {
			return priorityRank[a.Priority] > priorityRank[b.Priority]
		}
		ad, bd := a.DueDate, b.DueDate
		if ad == "" {
			ad = "9999-99-99"
		}
		if bd == "" {
			bd = "9999-99-99"
		}
		return ad < bd
	})
}
