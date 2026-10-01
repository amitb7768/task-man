package service

// Task domain, part 1: scoping helpers, validation, CRUD, PatchTask,
// cascade delete/restore, Reschedule. Ported method-for-method from
// server/store.go (the specification): same validation order, same error
// statuses and message strings, same JSON-visible results. Ids are opaque
// strings; an unknown id takes the same notFoundErr path Mongo's
// ErrNoDocuments did.
//
// BackfillWeekOf is deliberately NOT ported: it was a one-time catch-up for
// pre-v6 Mongo docs, and cmd/migrate-mongo carries weekOf over verbatim, so
// every Postgres row already has it.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"taskman/internal/model"
)

var validStatuses = map[string]bool{
	model.StatusTodo: true, model.StatusInProgress: true, model.StatusDone: true, model.StatusCancelled: true,
}
var validPriorities = map[string]bool{"": true, "low": true, "medium": true, "high": true}

var openStatuses = []string{model.StatusTodo, model.StatusInProgress}
var terminalStatuses = []string{model.StatusDone, model.StatusCancelled}

// ---- SQL condition builder ----

// taskCond accumulates AND-ed WHERE fragments. Every fragment is wrapped in
// parentheses so an OR inside one can never leak into its neighbours — the
// SQL analogue of Mongo's nested $and/$or documents.
type taskCond struct {
	parts []string
	args  []any
}

func (c *taskCond) and(sql string, args ...any) *taskCond {
	c.parts = append(c.parts, "("+sql+")")
	c.args = append(c.args, args...)
	return c
}

func (c *taskCond) andCond(o taskCond) *taskCond {
	if len(o.parts) == 0 {
		return c
	}
	return c.and(o.sql(), o.args...)
}

func (c taskCond) sql() string {
	if len(c.parts) == 0 {
		return "TRUE"
	}
	return strings.Join(c.parts, " AND ")
}

func (c taskCond) apply(db *gorm.DB) *gorm.DB { return db.Where(c.sql(), c.args...) }

// tagCond is the tag filter every list read ANDs in (docs/DESIGN_V10_TAGS.md
// decision #4): a task matches when it carries EVERY requested tag — jsonb
// containment, served by the tasks_tags GIN index. tags must already be
// normalised (the handler runs model.NormalizeTags). Empty = zero cond.
func tagCond(tags []string) taskCond {
	var c taskCond
	if len(tags) == 0 {
		return c
	}
	b, _ := json.Marshal(tags) // a []string cannot fail to marshal
	return *c.and("tags @> ?::jsonb", string(b))
}

// ---- scoping helpers ----

// sameTeamID reports whether a and b denote the same team (both nil, or
// both non-nil with equal values) — used to detect a patch attempting to
// move a task across the team/personal boundary.
func sameTeamID(a, b *string) bool {
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
func canAccessTask(u *model.CtxUser, t *model.Task) bool {
	if t.TeamID == nil {
		return t.OwnerID != nil && *t.OwnerID == u.ID
	}
	if u.SystemRole == model.RoleAdmin {
		return true
	}
	return u.InTeam(*t.TeamID)
}

// taskScope is the SQL fragment for "tasks u may see" in list-type reads
// (attention/search/summary): own personal tasks, plus team tasks u may see
// (ADMIN: every team task; USER: own teams' tasks only). Other users'
// personal tasks are never included, even for ADMIN. A team-less USER's
// empty TeamIDs renders as `IN (NULL)` — matches nothing, same as Mongo's
// normalized `$in: []`.
func taskScope(u *model.CtxUser) taskCond {
	var c taskCond
	if u.SystemRole == model.RoleAdmin {
		return *c.and("owner_id = ? OR team_id IS NOT NULL", u.ID)
	}
	ids := u.TeamIDs
	if ids == nil {
		ids = []string{}
	}
	return *c.and("owner_id = ? OR team_id IN ?", u.ID, ids)
}

// forbidNonMember is the TeamBoard-pattern team gate: USER must belong to
// teamID; ADMIN (or no session) passes.
func forbidNonMember(ctx context.Context, teamID string) error {
	if u := model.UserFromContext(ctx); u != nil && u.SystemRole != model.RoleAdmin && !u.InTeam(teamID) {
		return forbiddenErr("not a member of that team")
	}
	return nil
}

// ---- time + error helpers ----

// localDate formats an instant as its machine-local "YYYY-MM-DD" day bucket.
// The .Local() is load-bearing on anything read back from the DB.
func localDate(t time.Time) string {
	return model.CurrentPeriodAt(model.HorizonDaily, t.Local())
}

// dbNow is the write timestamp for paths where store.go truncated to BSON's
// millisecond resolution so the response equals a later read: Postgres
// timestamptz is microsecond-resolution (accepted delta #4), so truncate to
// that instead.
func dbNow() time.Time { return time.Now().Truncate(time.Microsecond) }

// utcTask / utcActivity normalize timestamps read back from Postgres to UTC:
// the Mongo driver decoded datetimes as UTC, so every read-path JSON
// timestamp was "…Z". pgx hands timestamptz back in time.Local.
func utcTask(t *model.Task) {
	t.CreatedAt = t.CreatedAt.UTC()
	t.UpdatedAt = t.UpdatedAt.UTC()
	if t.CompletedAt != nil {
		c := t.CompletedAt.UTC()
		t.CompletedAt = &c
	}
}

func utcActivity(e *model.ActivityEntry) {
	e.At = e.At.UTC()
	if e.EditedAt != nil {
		x := e.EditedAt.UTC()
		e.EditedAt = &x
	}
}

// taskPGErrCode returns the SQLSTATE of a Postgres error, or "".
func taskPGErrCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

const (
	pgFKViolation      = "23503"
	pgCheckViolation   = "23514"
	pgNotNullViolation = "23502"
)

// ---- loaders ----

// getTaskRow reads one task row WITHOUT its activity (the projected shape).
// lock takes a row lock (SELECT … FOR UPDATE) — PatchTask's replacement for
// the updatedAt CAS.
func getTaskRow(db *gorm.DB, id string, lock bool) (*model.Task, error) {
	var t model.Task
	q := db
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := q.Where("id = ?", id).Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFoundErr("task not found")
	}
	if err != nil {
		return nil, err
	}
	utcTask(&t)
	return &t, nil
}

// loadActivity returns a task's timeline in append order (seq — NEVER at,
// which can tie). nil when empty, matching Mongo's omitted field.
func loadActivity(db *gorm.DB, taskID string) ([]model.ActivityEntry, error) {
	var acts []model.ActivityEntry
	if err := db.Where("task_id = ?", taskID).Order("seq").Find(&acts).Error; err != nil {
		return nil, err
	}
	if len(acts) == 0 {
		return nil, nil
	}
	for i := range acts {
		utcActivity(&acts[i])
	}
	return acts, nil
}

// loadActivityFor fills Activity on every task in one query (full-doc reads
// over many tasks: DeleteTaskCascade, Summary).
func loadActivityFor(db *gorm.DB, tasks []model.Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	var acts []model.ActivityEntry
	if err := db.Where("task_id IN ?", ids).Order("seq").Find(&acts).Error; err != nil {
		return err
	}
	byTask := map[string][]model.ActivityEntry{}
	for _, e := range acts {
		utcActivity(&e)
		byTask[e.TaskID] = append(byTask[e.TaskID], e)
	}
	for i := range tasks {
		tasks[i].Activity = byTask[tasks[i].ID]
	}
	return nil
}

// getTaskRaw is the full-doc read: row + activity.
func (s *Service) getTaskRaw(ctx context.Context, id string) (*model.Task, error) {
	return getTaskFull(s.db.WithContext(ctx), id, false)
}

func getTaskFull(db *gorm.DB, id string, lock bool) (*model.Task, error) {
	t, err := getTaskRow(db, id, lock)
	if err != nil {
		return nil, err
	}
	if t.Activity, err = loadActivity(db, id); err != nil {
		return nil, err
	}
	return t, nil
}

// listTasks is the list-read primitive: activity is never loaded (the
// Postgres equivalent of findViews' {activity: 0} projection — these Tasks
// are INCOMPLETE and must never be written back).
func listTasks(db *gorm.DB, c taskCond, order string, offset, limit int) ([]model.Task, error) {
	var tasks []model.Task
	q := c.apply(db.Model(&model.Task{})).Order(order)
	if offset > 0 {
		q = q.Offset(offset)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&tasks).Error; err != nil {
		return nil, err
	}
	for i := range tasks {
		utcTask(&tasks[i])
	}
	return tasks, nil
}

// ---- validation ----

// validateTaskFields validates a task's fields and cross-references
// (parent/team/assignee existence, horizon rules). selfID, when non-nil, is
// the task's own id (for patch revalidation), used to reject self-parenting.
// db is s.db or the patch tx. keepTags are tags the stored row already
// carries (patch: orig.Tags; create: nil): only tags NOT in it are checked
// against the catalog, so keeping or removing a tag never fails.
func (s *Service) validateTaskFields(ctx context.Context, db *gorm.DB, t *model.Task, selfID *string, keepTags []string) error {
	if strings.TrimSpace(t.Title) == "" {
		return badRequest("title is required")
	}
	if !model.ValidHorizon(t.Horizon) {
		return badRequest("invalid horizon %q", t.Horizon)
	}
	if err := model.ValidatePeriod(t.Horizon, t.Period); err != nil {
		return badRequest("%s", err.Error())
	}
	if t.DueDate != "" {
		if _, err := model.ParseDate(string(t.DueDate)); err != nil {
			return badRequest("invalid dueDate: %s", err.Error())
		}
	}
	if !validStatuses[t.Status] {
		return badRequest("invalid status %q", t.Status)
	}
	if !validPriorities[t.Priority] {
		return badRequest("invalid priority %q", t.Priority)
	}
	tags, err := model.NormalizeTags(t.Tags)
	if err != nil {
		return badRequest("%s", err.Error())
	}
	t.Tags = tags
	// v11: only catalog tags (docs/DESIGN_V11_TAG_CATALOG.md decision #1),
	// checked for newly added ones only (an orphan already on the row stays).
	var added []string
	for _, tag := range t.Tags {
		if !slices.Contains(keepTags, tag) {
			added = append(added, tag)
		}
	}
	if missing, err := unknownTag(db, added); err != nil {
		return err
	} else if missing != "" {
		return badRequest("unknown tag %q", missing)
	}

	// Backlog invariants (docs/DESIGN_V7_BACKLOG.md).
	if t.Horizon == model.HorizonBacklog {
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
		parent, err := getTaskRow(db, *t.ParentID, false)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Status == 404 {
				return badRequest("parent task not found")
			}
			return err
		}
		// Personal-task privacy also governs attachment (see store.go).
		if u := model.UserFromContext(ctx); u != nil && !canAccessTask(u, parent) {
			return forbiddenErr("not allowed to attach to that parent")
		}
		parentRank, _ := model.HorizonRank(parent.Horizon)
		childRank, _ := model.HorizonRank(t.Horizon)
		if childRank > parentRank {
			return badRequest("child horizon %q cannot be coarser than parent horizon %q", t.Horizon, parent.Horizon)
		}

		if selfID != nil {
			// Walk up the new parent's ancestor chain; if selfID appears,
			// re-parenting would create a cycle. A visited guard protects
			// against pre-existing bad data forming an unrelated loop.
			visited := map[string]bool{}
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
				next, err := getTaskRow(db, *cur.ParentID, false)
				if err != nil {
					var ae *APIError
					if errors.As(err, &ae) && ae.Status == 404 {
						break
					}
					return err
				}
				cur = next
			}
		}
	}

	if t.TeamID != nil {
		var cnt int64
		if err := db.Table("teams").Where("id = ?", *t.TeamID).Count(&cnt).Error; err != nil {
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
		var cnt int64
		if err := db.Table("members").Where("id = ?", *t.AssigneeID).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt == 0 {
			return badRequest("assignee not found")
		}
		if err := db.Table("member_teams").
			Where("member_id = ? AND team_id = ?", *t.AssigneeID, *t.TeamID).
			Count(&cnt).Error; err != nil {
			return err
		}
		if cnt == 0 {
			return badRequest("assignee does not belong to team")
		}
	}

	if t.Recurrence != nil {
		if err := model.ValidateRecurrence(t.Recurrence); err != nil {
			return badRequest("%s", err.Error())
		}
		h, _ := model.RecurrenceHorizon(t.Recurrence.Freq)
		if t.Horizon != h {
			return badRequest("recurrence freq %q requires horizon %q", t.Recurrence.Freq, h)
		}
	}
	return nil
}

// ---- task CRUD ----

func (s *Service) CreateTask(ctx context.Context, t *model.Task) (*model.TaskView, error) {
	db := s.db.WithContext(ctx)
	t.ID = NewID()
	t.SeriesID = nil // system-managed; ignore any client-supplied value
	t.Activity = nil // system-managed; a task starts with an empty timeline
	if t.Status == "" {
		t.Status = model.StatusTodo
	}
	now := time.Now()
	t.CreatedAt, t.UpdatedAt = now, now
	if t.Status == model.StatusDone {
		t.CompletedAt = &now
	} else {
		t.CompletedAt = nil
	}

	// ownerId is system-managed (docs/AUTH_FEATURES.md decision #3 + #5).
	if u := model.UserFromContext(ctx); u != nil {
		if t.TeamID == nil {
			oid := u.ID
			t.OwnerID = &oid
		} else {
			t.OwnerID = nil
			if u.SystemRole != model.RoleAdmin && !u.InTeam(*t.TeamID) {
				return nil, forbiddenErr("not a member of that team")
			}
		}
	}

	// weekOf is system-managed: present iff TeamID is set, always the current
	// ISO week on create (docs/DESIGN_V6_WEEK_ROLLOVER.md).
	if t.TeamID != nil {
		t.WeekOf = model.NullStr(model.ISOWeekString(now))
	} else {
		t.WeekOf = ""
	}

	if err := s.validateTaskFields(ctx, db, t, nil, nil); err != nil {
		return nil, err
	}

	if t.Recurrence != nil {
		sid := t.ID
		t.SeriesID = &sid // first instance's id
		anchor, err := model.ComputeAnchor(t.Recurrence.Freq, t.Period)
		if err != nil {
			return nil, err
		}
		t.Recurrence.Anchor = anchor
	}

	if err := db.Create(t).Error; err != nil {
		return nil, err
	}
	return &model.TaskView{Task: *t, Progress: model.Progress{}}, nil
}

// progress counts direct children (done + cancelled count as done) of one
// task — the single-task path, via the same grouped query as toViews.
func progress(db *gorm.DB, parentID string) (model.Progress, error) {
	byParent, err := progressFor(db, []string{parentID})
	if err != nil {
		return model.Progress{}, err
	}
	return byParent[parentID], nil
}

// progressFor counts direct children (done + cancelled count as done) for
// every id in ONE grouped query — replaces the per-task query toViews used
// to run (the P3 N+1 fix). Ids with no children are absent from the map;
// the zero Progress{} a lookup yields is exactly what the per-task count(*)
// returned for them.
func progressFor(db *gorm.DB, parentIDs []string) (map[string]model.Progress, error) {
	out := make(map[string]model.Progress, len(parentIDs))
	if len(parentIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ParentID string
		Total    int
		Done     int
	}
	err := db.Raw(`SELECT parent_id, count(*) AS total,
		count(*) FILTER (WHERE status IN ?) AS done
		FROM tasks WHERE parent_id IN ? GROUP BY parent_id`, terminalStatuses, parentIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ParentID] = model.Progress{Done: r.Done, Total: r.Total}
	}
	return out, nil
}

func toView(db *gorm.DB, t model.Task) (model.TaskView, error) {
	p, err := progress(db, t.ID)
	if err != nil {
		return model.TaskView{}, err
	}
	return model.TaskView{Task: t, Progress: p}, nil
}

// toViews attaches progress to every task with one grouped query per call.
func toViews(db *gorm.DB, tasks []model.Task) ([]model.TaskView, error) {
	views := make([]model.TaskView, 0, len(tasks))
	if len(tasks) == 0 {
		return views, nil
	}
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	byParent, err := progressFor(db, ids)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		views = append(views, model.TaskView{Task: t, Progress: byParent[t.ID]})
	}
	return views, nil
}

// findViews: createdAt ascending (Mongo's only list sort; id breaks ties
// deterministically where Mongo's natural order was unspecified), activity
// never loaded.
func (s *Service) findViews(ctx context.Context, c taskCond) ([]model.TaskView, error) {
	db := s.db.WithContext(ctx)
	tasks, err := listTasks(db, c, "created_at ASC, id ASC", 0, 0)
	if err != nil {
		return nil, err
	}
	return toViews(db, tasks)
}

// findPersonal is findViews restricted to tasks with no teamId, further
// scoped to the session user's own tasks — Day/Week/Month planning views are
// personal to the signed-in caller for EVERYONE, admin included.
func (s *Service) findPersonal(ctx context.Context, c taskCond) ([]model.TaskView, error) {
	var f taskCond
	f.and("team_id IS NULL")
	if u := model.UserFromContext(ctx); u != nil {
		f.and("owner_id = ?", u.ID)
	}
	f.andCond(c)
	return s.findViews(ctx, f)
}

// Backlog returns the caller's own backlog tasks, newest-first by createdAt
// (the ascending findViews result, reversed — verbatim).
func (s *Service) Backlog(ctx context.Context, tags []string) ([]model.TaskView, error) {
	var c taskCond
	c.and("horizon = ?", model.HorizonBacklog)
	tasks, err := s.findPersonal(ctx, *c.andCond(tagCond(tags)))
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(tasks)-1; i < j; i, j = i+1, j-1 {
		tasks[i], tasks[j] = tasks[j], tasks[i]
	}
	return tasks, nil
}

func (s *Service) GetTaskDetail(ctx context.Context, id string) (*model.TaskDetail, error) {
	db := s.db.WithContext(ctx)
	t, err := s.getTaskRaw(ctx, id)
	if err != nil {
		return nil, err
	}
	u := model.UserFromContext(ctx)
	if u != nil && !canAccessTask(u, t) {
		return nil, forbiddenErr("not allowed")
	}
	view, err := toView(db, *t)
	if err != nil {
		return nil, err
	}
	// Children are a list payload: no activity. The detail's OWN activity
	// came from getTaskRaw above.
	var c taskCond
	children, err := listTasks(db, *c.and("parent_id = ?", id), "created_at ASC, id ASC", 0, 0)
	if err != nil {
		return nil, err
	}
	if u != nil {
		visible := make([]model.Task, 0, len(children))
		for _, ch := range children {
			if canAccessTask(u, &ch) {
				visible = append(visible, ch)
			}
		}
		children = visible
	}
	childViews, err := toViews(db, children)
	if err != nil {
		return nil, err
	}
	return &model.TaskDetail{TaskView: view, Children: childViews}, nil
}

// patchLockedHook, when non-nil, runs inside PatchTask's transaction right
// after the task row is locked. Test-only seam for the concurrency test; nil
// in production.
var patchLockedHook func(taskID string)

// PatchTask applies a partial JSON update onto the existing task and
// revalidates the merged result.
//
// Mongo did this as an optimistic read-modify-write guarded by an updatedAt
// CAS, retried up to 8 times and then 409. Postgres replaces the whole loop
// with ONE transaction: SELECT … FOR UPDATE on the task row, the verbatim
// merge/validate/derive sequence, UPDATE + status-activity INSERT, commit. A
// concurrent patch now waits on the row lock instead of 409-ing (accepted
// delta #5). The activity log lives in its own table and the UPDATE never
// rewrites it, so a concurrent note can't be clobbered by construction —
// and note writes bump updated_at through this same row, so they serialize
// behind (or ahead of) the lock rather than interleaving.
func (s *Service) PatchTask(ctx context.Context, id string, raw []byte) (*model.TaskView, error) {
	var view *model.TaskView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		v, err := s.patchTaskTx(ctx, tx, id, raw)
		view = v
		return err
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

func (s *Service) patchTaskTx(ctx context.Context, tx *gorm.DB, id string, raw []byte) (*model.TaskView, error) {
	t, err := getTaskFull(tx, id, true)
	if err != nil {
		return nil, err
	}
	if patchLockedHook != nil {
		patchLockedHook(id)
	}
	orig := *t
	u := model.UserFromContext(ctx)
	if u != nil && !canAccessTask(u, &orig) {
		return nil, forbiddenErr("not allowed")
	}
	now := dbNow()

	// weekOf patch detection via raw key presence (case-insensitive, like
	// json.Unmarshal's field matching) — see store.go.
	var patchFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patchFields); err != nil {
		return nil, badRequest("invalid JSON: %s", err.Error())
	}
	weekOfPatched := false
	for k := range patchFields {
		if strings.EqualFold(k, "weekOf") {
			weekOfPatched = true
			break
		}
	}

	// Independent copy of recurrence before unmarshal merges into it in place.
	origRecurrence := model.CloneRecurrence(t.Recurrence)
	// activity is system-managed and never client-settable. Nil it BEFORE the
	// unmarshal (a non-nil slice would be appended into the SAME backing array
	// orig.Activity aliases), then restore orig's below. Activity is
	// gorm:"-" now, but the response carries it, so the rule still holds.
	t.Activity = nil
	// Same hazard for tags: `orig := *t` shares t.Tags' backing array, and
	// json.Unmarshal decodes a JSON array INTO the existing slice in place —
	// a tags patch would silently rewrite orig.Tags too. Give t its own copy.
	t.Tags = slices.Clone(t.Tags)
	if err := json.Unmarshal(raw, t); err != nil {
		return nil, badRequest("invalid JSON: %s", err.Error())
	}
	patchedWeekOf := t.WeekOf
	// Immutable/system-managed fields: never client-settable via patch.
	t.ID = orig.ID
	t.SeriesID = orig.SeriesID
	t.CreatedAt = orig.CreatedAt
	t.Activity = orig.Activity // discard anything the client sent

	if u != nil && u.SystemRole != model.RoleAdmin && !sameTeamID(orig.TeamID, t.TeamID) {
		return nil, forbiddenErr("only an admin can change a task's team")
	}

	switch {
	case orig.TeamID == nil && t.TeamID != nil: // personal -> team
		t.OwnerID = nil
		t.WeekOf = model.NullStr(model.ISOWeekString(now))
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
	if u != nil && u.SystemRole != model.RoleAdmin && t.TeamID != nil && !u.InTeam(*t.TeamID) {
		return nil, forbiddenErr("not a member of that team")
	}

	if weekOfPatched {
		if u != nil && u.SystemRole != model.RoleAdmin {
			return nil, forbiddenErr("only an admin can change weekOf")
		}
		t.WeekOf = patchedWeekOf
		if t.TeamID == nil {
			return nil, badRequest("weekOf is not valid on a personal task")
		}
		if _, _, err := model.ParseISOWeek(string(t.WeekOf)); err != nil {
			return nil, badRequest("invalid weekOf: %s", err.Error())
		}
	}

	// completedAt: set ONLY on transition into done; cleared on transition
	// out of done; otherwise preserved. Cancelled never gets one.
	switch {
	case orig.Status != model.StatusDone && t.Status == model.StatusDone:
		t.CompletedAt = &now
	case orig.Status == model.StatusDone && t.Status != model.StatusDone:
		t.CompletedAt = nil
	default:
		t.CompletedAt = orig.CompletedAt
	}
	t.UpdatedAt = now

	// Transition into terminal bumps a team task's weekOf unconditionally,
	// overriding an explicit weekOf patch validated above.
	wasTerminal := orig.Status == model.StatusDone || orig.Status == model.StatusCancelled
	isTerminal := t.Status == model.StatusDone || t.Status == model.StatusCancelled
	if !wasTerminal && isTerminal && t.TeamID != nil {
		t.WeekOf = model.NullStr(model.ISOWeekString(now))
	}

	if err := s.validateTaskFields(ctx, tx, t, &id, orig.Tags); err != nil {
		return nil, err
	}

	if t.Horizon != orig.Horizon {
		if err := validateChildrenHorizon(tx, id, t.Horizon); err != nil {
			return nil, err
		}
	}

	// Recurrence anchor: re-anchor only on freq/interval change.
	if t.Recurrence != nil {
		changed := origRecurrence == nil ||
			origRecurrence.Freq != t.Recurrence.Freq ||
			model.EffectiveInterval(origRecurrence) != model.EffectiveInterval(t.Recurrence)
		if changed {
			anchor, err := model.ComputeAnchor(t.Recurrence.Freq, t.Period)
			if err != nil {
				return nil, err
			}
			t.Recurrence.Anchor = anchor
		} else {
			t.Recurrence.Anchor = origRecurrence.Anchor
		}
	}

	// Everything validated: write.
	if orig.Status == t.Status {
		// Status unchanged: the FSM is never entered. The UPDATE never
		// touches task_activity.
		res := tx.Model(&model.Task{ID: id}).Select("*").Omit("id", "created_at").Updates(t)
		if res.Error != nil {
			return nil, res.Error
		}
	} else {
		// Status change: through the FSM (fsm.go) — status CAS, the rest of
		// the row, then the status auto-log, all in this tx (savepoint).
		// The log is written LAST (after all validation) so a rejected patch
		// or transition leaves no trace.
		e, err := s.transitionTaskStatus(ctx, tx, t, orig.Status, now, u)
		if err != nil {
			return nil, err
		}
		t.Activity = append(t.Activity, *e)
	}

	view, err := toView(tx, *t)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// validateChildrenHorizon returns a 400 error if any direct child of
// parentID has a horizon finer-grained than newHorizon.
func validateChildrenHorizon(db *gorm.DB, parentID, newHorizon string) error {
	newRank, _ := model.HorizonRank(newHorizon)
	var horizons []string
	if err := db.Model(&model.Task{}).Where("parent_id = ?", parentID).
		Order("created_at, id").Pluck("horizon", &horizons).Error; err != nil {
		return err
	}
	for _, h := range horizons {
		childRank, _ := model.HorizonRank(h)
		if childRank > newRank {
			return badRequest("horizon %q is coarser than an existing child's horizon %q", newHorizon, h)
		}
	}
	return nil
}

// DeleteTaskCascade deletes id and its full descendant subtree inside one
// transaction, returning the deleted tasks (root first, then descendants
// breadth-first) as full TaskViews — activity included, since the response
// is the client's undo snapshot that RestoreTasks replays. Progress is
// computed before the delete. parent_id is ON DELETE RESTRICT, so rows are
// deleted children-first (reverse BFS order).
//
// DELETE is route-gated to ADMIN with deliberately no canAccessTask (see
// store.go: an admin must be able to delete any task).
func (s *Service) DeleteTaskCascade(ctx context.Context, id string) ([]model.TaskView, error) {
	var views []model.TaskView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		root, err := getTaskRow(tx, id, true)
		if err != nil {
			return err
		}
		subtree := []model.Task{*root}
		visited := map[string]bool{id: true}
		queue := []string{id}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			var c taskCond
			kids, err := listTasks(tx, *c.and("parent_id = ?", cur), "created_at ASC, id ASC", 0, 0)
			if err != nil {
				return err
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
		if err := loadActivityFor(tx, subtree); err != nil {
			return err
		}
		views, err = toViews(tx, subtree)
		if err != nil {
			return err
		}
		for i := len(subtree) - 1; i >= 0; i-- {
			if err := tx.Where("id = ?", subtree[i].ID).Delete(&model.Task{}).Error; err != nil {
				// A child attached after the BFS read trips the RESTRICT FK:
				// the subtree moved underneath us.
				if taskPGErrCode(err) == pgFKViolation {
					return conflictErr("task changed concurrently, retry")
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return views, nil
}

// RestoreTasks reinserts previously-deleted tasks with their original ids
// and BOTH timestamps verbatim (model.Task has gorm auto-time off), plus any
// activity entries they carry (Mongo's embedded-doc replay parity). Returns
// the count of tasks actually inserted.
//
// Now VALIDATED (locked decision #3): every parentId must resolve to an
// existing task or one earlier in the payload, and teamId/assigneeId/ownerId
// to existing rows — otherwise 400 and nothing is written (one tx). Tasks are
// inserted parent-before-child. A task whose id (or series/period slot)
// already exists is skipped silently, as Mongo skipped duplicate keys — and
// its activity is not replayed. An activity author (by) that no longer
// exists is written as NULL (byName kept) — the same outcome the by_id
// ON DELETE SET NULL would have produced had the row been live.
func (s *Service) RestoreTasks(ctx context.Context, tasks []model.Task) (int, error) {
	if len(tasks) == 0 {
		return 0, nil
	}
	ordered, err := restoreOrder(tasks)
	if err != nil {
		return 0, err
	}
	// Restore bypasses validateTaskFields, so normalise tags here: a
	// hand-crafted payload must not store un-normalised or invalid tags.
	for i := range ordered {
		tags, err := model.NormalizeTags(ordered[i].Tags)
		if err != nil {
			return 0, badRequest("restore: task %s: %s", ordered[i].ID, err.Error())
		}
		ordered[i].Tags = tags
	}
	inserted := 0
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateRestoreRefs(tx, ordered); err != nil {
			return err
		}
		// v11: a restored task may carry only catalog tags. Unknown ones
		// (deleted from the catalog since the snapshot) are stripped, not
		// rejected, so Undo never fails permanently.
		for i := range ordered {
			kept, dropped, err := splitCatalogTags(tx, ordered[i].Tags)
			if err != nil {
				return err
			}
			if len(dropped) > 0 {
				slog.Warn("restore: dropped tags not in the catalog", "task", ordered[i].ID, "tags", dropped)
				ordered[i].Tags = kept
			}
		}
		for i := range ordered {
			t := ordered[i]
			acts := t.Activity
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&t)
			if res.Error != nil {
				return restoreErr(res.Error)
			}
			if res.RowsAffected == 0 {
				continue // duplicate: already present
			}
			inserted++
			for _, e := range acts {
				e.TaskID = t.ID
				e.Seq = 0
				if e.ID == "" {
					e.ID = NewID()
				}
				if e.By != nil {
					var cnt int64
					if err := tx.Table("members").Where("id = ?", *e.By).Count(&cnt).Error; err != nil {
						return err
					}
					if cnt == 0 {
						e.By = nil
					}
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&e).Error; err != nil {
					return restoreErr(err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// restoreOrder sorts a restore payload parent-before-child (a stable
// topological order: payload order preserved among ready tasks). A parent
// cycle inside the payload cannot be inserted under the FK and is rejected.
func restoreOrder(tasks []model.Task) ([]model.Task, error) {
	inPayload := map[string]bool{}
	for _, t := range tasks {
		inPayload[t.ID] = true
	}
	placed := map[string]bool{}
	out := make([]model.Task, 0, len(tasks))
	remaining := tasks
	for len(remaining) > 0 {
		var next []model.Task
		for _, t := range remaining {
			if t.ParentID == nil || !inPayload[*t.ParentID] || placed[*t.ParentID] || *t.ParentID == t.ID {
				if t.ParentID != nil && *t.ParentID == t.ID {
					return nil, badRequest("restore: task %s is its own parent", t.ID)
				}
				out = append(out, t)
				placed[t.ID] = true
			} else {
				next = append(next, t)
			}
		}
		if len(next) == len(remaining) {
			return nil, badRequest("restore: parent cycle in payload")
		}
		remaining = next
	}
	return out, nil
}

// validateRestoreRefs rejects dangling references with a clear 400 before
// anything is written.
func validateRestoreRefs(tx *gorm.DB, tasks []model.Task) error {
	inPayload := map[string]bool{}
	for _, t := range tasks {
		inPayload[t.ID] = true
	}
	exists := func(table, id string) (bool, error) {
		var cnt int64
		err := tx.Table(table).Where("id = ?", id).Count(&cnt).Error
		return cnt > 0, err
	}
	for _, t := range tasks {
		if t.ParentID != nil && !inPayload[*t.ParentID] {
			ok, err := exists("tasks", *t.ParentID)
			if err != nil {
				return err
			}
			if !ok {
				return badRequest("restore: task %s references missing parent %s", t.ID, *t.ParentID)
			}
		}
		if t.TeamID != nil {
			ok, err := exists("teams", *t.TeamID)
			if err != nil {
				return err
			}
			if !ok {
				return badRequest("restore: task %s references missing team %s", t.ID, *t.TeamID)
			}
		}
		if t.AssigneeID != nil {
			ok, err := exists("members", *t.AssigneeID)
			if err != nil {
				return err
			}
			if !ok {
				return badRequest("restore: task %s references missing assignee %s", t.ID, *t.AssigneeID)
			}
		}
		if t.OwnerID != nil {
			ok, err := exists("members", *t.OwnerID)
			if err != nil {
				return err
			}
			if !ok {
				return badRequest("restore: task %s references missing owner %s", t.ID, *t.OwnerID)
			}
		}
	}
	return nil
}

// restoreErr maps integrity violations the pre-check can't see (a row
// deleted between check and insert, a CHECK-constraint value Mongo would
// have accepted) to 400; anything else passes through.
func restoreErr(err error) error {
	switch taskPGErrCode(err) {
	case pgFKViolation:
		return badRequest("restore: dangling reference: %s", err.Error())
	case pgCheckViolation, pgNotNullViolation:
		return badRequest("restore: invalid task: %s", err.Error())
	}
	return err
}

// Reschedule moves each visible, non-backlog task to the current period
// (and a set dueDate to today). Verbatim wart (server/CLAUDE.md): a raw
// period write that does NOT re-anchor recurrence — ported as-is, not fixed.
func (s *Service) Reschedule(ctx context.Context, ids []string) (int, error) {
	db := s.db.WithContext(ctx)
	u := model.UserFromContext(ctx)
	updated := 0
	for _, id := range ids {
		t, err := getTaskRow(db, id, false)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Status == 404 {
				continue
			}
			return updated, err
		}
		if u != nil && !canAccessTask(u, t) {
			continue // out of the caller's visible scope: skip silently, like a missing id
		}
		if t.Horizon == model.HorizonBacklog {
			continue // no period to reschedule to: a no-op, not a real update
		}
		set := map[string]any{"period": model.CurrentPeriod(t.Horizon), "updated_at": time.Now()}
		if t.DueDate != "" {
			set["due_date"] = model.CurrentPeriod(model.HorizonDaily)
		}
		res := db.Model(&model.Task{}).Where("id = ?", id).Updates(set)
		if res.Error != nil {
			return updated, res.Error
		}
		if res.RowsAffected > 0 {
			updated++
		}
	}
	return updated, nil
}
