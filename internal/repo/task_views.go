package repo

// Task domain, part 2: recurrence materialization and every list read
// (planning views, attention, search, team board/rollover/history). Every
// public read runs Materialize first, exactly as store.go does.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"taskman/internal/model"
)

// ---- recurrence materialization ----

// seriesPeriodConflict is the ON CONFLICT target for spawned instances. It
// must name the partial unique index's predicate (tasks_series_period_unique
// is `(series_id, period) WHERE series_id IS NOT NULL`) or Postgres will not
// infer that index as the arbiter and the INSERT errors instead.
var seriesPeriodConflict = clause.OnConflict{
	Columns: []clause.Column{{Name: "series_id"}, {Name: "period"}},
	TargetWhere: clause.Where{Exprs: []clause.Expression{
		clause.Expr{SQL: "series_id IS NOT NULL"},
	}},
	DoNothing: true,
}

// Materialize lazily/idempotently spawns missing instances for every
// recurring series, up to "now"'s period. Called before every read that
// surfaces tasks. Idempotency is the partial unique (series_id, period) +
// ON CONFLICT DO NOTHING — the Postgres form of Mongo's ignored duplicate
// key. A series ends ONLY when its latest instance's recurrence is nil;
// cancelling an instance never stops it.
func (s *Store) Materialize(ctx context.Context) error {
	db := s.db.WithContext(ctx)
	var series []model.Task
	if err := db.Where("series_id IS NOT NULL").Find(&series).Error; err != nil {
		return err
	}
	latest := map[string]model.Task{}
	for _, t := range series {
		if t.SeriesID == nil {
			continue
		}
		if ex, ok := latest[*t.SeriesID]; !ok || t.Period > ex.Period {
			latest[*t.SeriesID] = t
		}
	}

	now := time.Now()
	for _, t := range latest {
		if t.Recurrence == nil {
			continue // recurrence removed from the live instance: series ended
		}
		horizon, ok := model.RecurrenceHorizon(t.Recurrence.Freq)
		if !ok {
			continue
		}
		cp := model.CurrentPeriodAt(horizon, now)
		periods, err := model.MissingPeriods(t.Recurrence, horizon, t.Period, cp)
		if err != nil {
			return err
		}
		for _, p := range periods {
			spawn := &model.Task{
				ID:         NewID(),
				Title:      t.Title,
				Notes:      t.Notes,
				Horizon:    horizon,
				Period:     p,
				Status:     model.StatusTodo,
				Priority:   t.Priority,
				OwnerID:    t.OwnerID, // spawned instances copy ownerId from the series task
				TeamID:     t.TeamID,
				AssigneeID: t.AssigneeID,
				Recurrence: t.Recurrence,
				SeriesID:   t.SeriesID,
				CreatedAt:  now,
				UpdatedAt:  now,
				// dueDate NOT copied; parentId NOT copied (per DESIGN.md);
				// activity starts empty.
			}
			if d := model.DueDateForSpawn(t.Recurrence, horizon, p); d != "" {
				spawn.DueDate = model.NullStr(d)
			}
			if spawn.TeamID != nil {
				spawn.WeekOf = model.NullStr(model.ISOWeekString(now))
			}
			if err := db.Clauses(seriesPeriodConflict).Create(spawn).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- views ----

func (s *Store) ViewDay(ctx context.Context, date string) (tasks, weekContext []model.TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, err
	}
	if err = model.ValidatePeriod(model.HorizonDaily, date); err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	tasks, err = s.findPersonal(ctx, horizonPeriod(model.HorizonDaily, date))
	if err != nil {
		return nil, nil, err
	}
	week, err := model.WeekOfDate(date)
	if err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	weekContext, err = s.findPersonal(ctx, horizonPeriod(model.HorizonWeekly, week))
	return tasks, weekContext, err
}

func (s *Store) ViewWeek(ctx context.Context, week string) (tasks []model.TaskView, days map[string][]model.TaskView, monthContext []model.TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, nil, err
	}
	if err = model.ValidatePeriod(model.HorizonWeekly, week); err != nil {
		return nil, nil, nil, badRequest("%s", err.Error())
	}
	tasks, err = s.findPersonal(ctx, horizonPeriod(model.HorizonWeekly, week))
	if err != nil {
		return nil, nil, nil, err
	}
	dates, err := model.DatesInWeek(week)
	if err != nil {
		return nil, nil, nil, badRequest("%s", err.Error())
	}
	days = map[string][]model.TaskView{}
	for _, d := range dates {
		dv, err := s.findPersonal(ctx, horizonPeriod(model.HorizonDaily, d))
		if err != nil {
			return nil, nil, nil, err
		}
		days[d] = dv
	}
	month, err := model.MonthOfWeek(week)
	if err != nil {
		return nil, nil, nil, badRequest("%s", err.Error())
	}
	monthContext, err = s.findPersonal(ctx, horizonPeriod(model.HorizonMonthly, month))
	return tasks, days, monthContext, err
}

func (s *Store) ViewMonth(ctx context.Context, month string) (tasks []model.TaskView, weeks map[string][]model.TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, err
	}
	if err = model.ValidatePeriod(model.HorizonMonthly, month); err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	tasks, err = s.findPersonal(ctx, horizonPeriod(model.HorizonMonthly, month))
	if err != nil {
		return nil, nil, err
	}
	ws, err := model.WeeksInMonth(month)
	if err != nil {
		return nil, nil, badRequest("%s", err.Error())
	}
	weeks = map[string][]model.TaskView{}
	for _, w := range ws {
		dates, err := model.DatesInWeek(w)
		if err != nil {
			return nil, nil, badRequest("%s", err.Error())
		}
		// Month-view daily rollup (docs/DESIGN_V7_BACKLOG.md): daily tasks
		// land only in the month their date belongs to.
		monthDates := []string{}
		for _, d := range dates {
			if strings.HasPrefix(d, month) {
				monthDates = append(monthDates, d)
			}
		}
		var c taskCond
		c.and("(horizon = ? AND period = ?) OR (horizon = ? AND period IN ?)",
			model.HorizonWeekly, w, model.HorizonDaily, monthDates)
		wv, err := s.findPersonal(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		weeks[w] = wv
	}
	return tasks, weeks, nil
}

func horizonPeriod(h, p string) taskCond {
	var c taskCond
	return *c.and("horizon = ? AND period = ?", h, p)
}

// ViewAttention returns overdue + slipped tasks: own personal tasks plus
// visible team tasks (ADMIN: every team; USER: own teams only).
func (s *Store) ViewAttention(ctx context.Context) (overdue, slipped []model.TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, nil, err
	}
	today := model.CurrentPeriod(model.HorizonDaily)
	var overdueC, slippedC taskCond
	overdueC.and("due_date IS NOT NULL AND due_date < ?", today)
	overdueC.and("status IN ?", openStatuses)
	slippedC.and("due_date IS NULL")
	slippedC.and("status IN ?", openStatuses)
	slippedC.and("(horizon = ? AND period < ?) OR (horizon = ? AND period < ?) OR (horizon = ? AND period < ?)",
		model.HorizonDaily, today,
		model.HorizonWeekly, model.CurrentPeriod(model.HorizonWeekly),
		model.HorizonMonthly, model.CurrentPeriod(model.HorizonMonthly))
	if u := model.UserFromContext(ctx); u != nil {
		scope := taskScope(u)
		overdueC.andCond(scope)
		slippedC.andCond(scope)
	}
	overdue, err = s.findViews(ctx, overdueC)
	if err != nil {
		return nil, nil, err
	}
	slipped, err = s.findViews(ctx, slippedC)
	return overdue, slipped, err
}

// ---- search ----

// likeEscaper escapes LIKE metacharacters (and the escape char itself) so a
// query is matched as a literal substring.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Search: Mongo's $text is replaced by a case-insensitive substring match
// over title/notes (accepted delta #2: substring, not stemmed word match).
// Every other filter, the scoping, and the createdAt-asc sort are verbatim.
// title is COLLATE "C" (binary order for sorting); the ILIKE runs it under
// the database default collation so case-folding isn't ASCII-only.
func (s *Store) Search(ctx context.Context, p model.SearchParams) ([]model.TaskView, error) {
	if err := s.Materialize(ctx); err != nil {
		return nil, err
	}
	var c taskCond
	if p.Q != "" {
		pat := "%" + likeEscaper.Replace(p.Q) + "%"
		c.and(`(title COLLATE "default") ILIKE ? ESCAPE '\' OR notes ILIKE ? ESCAPE '\'`, pat, pat)
	}
	if p.Status == "open" {
		c.and("status IN ?", openStatuses)
	} else if p.Status != "" {
		c.and("status = ?", p.Status)
	}
	if p.Priority != "" {
		c.and("priority = ?", p.Priority)
	}
	if p.Horizon != "" {
		c.and("horizon = ?", p.Horizon)
	} else {
		// Backlog hidden by default; only an explicit horizon=backlog opts in.
		c.and("horizon <> ?", model.HorizonBacklog)
	}
	if p.TeamID != nil {
		c.and("team_id = ?", *p.TeamID)
	}
	if p.AssigneeID != nil {
		c.and("assignee_id = ?", *p.AssigneeID)
	}
	if p.Overdue {
		c.and("due_date IS NOT NULL AND due_date < ? AND status IN ?",
			model.CurrentPeriod(model.HorizonDaily), openStatuses)
	}
	if u := model.UserFromContext(ctx); u != nil {
		c.andCond(taskScope(u))
	}
	return s.findViews(ctx, c)
}

// ---- team board ----

// boardVisibility is "task state visible on the team board this week" (W =
// current ISO week): open tasks with a dueDate always; open undated tasks
// only if weekOf == W; terminal tasks only if weekOf == W. The dueDate arm is
// IS NOT NULL — $exists parity (NullStr never writes an empty string).
func boardVisibility(W string) taskCond {
	var c taskCond
	return *c.and("(status IN ? AND (due_date IS NOT NULL OR week_of = ?)) OR (status IN ? AND week_of = ?)",
		openStatuses, W, terminalStatuses, W)
}

// staleOpenCond: open, undated, weekOf strictly before W (a NULL weekOf
// never matches `<`, as a missing field never matched Mongo's $lt).
func staleOpenCond(teamID, W string) taskCond {
	var c taskCond
	c.and("team_id = ?", teamID)
	c.and("status IN ?", openStatuses)
	c.and("due_date IS NULL")
	c.and("week_of < ?", W)
	return c
}

// teamByID loads a team or 404s "team not found".
func teamByID(db *gorm.DB, id string) (*model.Team, error) {
	var team model.Team
	err := db.Where("id = ?", id).Take(&team).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFoundErr("team not found")
	}
	if err != nil {
		return nil, err
	}
	team.CreatedAt = team.CreatedAt.UTC()
	return &team, nil
}

// TeamBoard returns a team's board. USER callers must belong to the team;
// ADMIN may view any. Visibility is scoped to the current week
// (boardVisibility); progress() stays unscoped by design.
func (s *Store) TeamBoard(ctx context.Context, teamID string) (*model.Board, error) {
	if err := s.Materialize(ctx); err != nil {
		return nil, err
	}
	if err := forbidNonMember(ctx, teamID); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	team, err := teamByID(db, teamID)
	if err != nil {
		return nil, err
	}
	members, err := s.ListMembers(ctx, &teamID)
	if err != nil {
		return nil, err
	}

	W := model.CurrentPeriod(model.HorizonWeekly)
	visibility := boardVisibility(W)

	board := &model.Board{Team: *team, Members: []model.BoardMemberTasks{}, Week: W}
	for _, m := range members {
		var c taskCond
		c.and("team_id = ? AND assignee_id = ?", teamID, m.ID)
		c.andCond(visibility)
		tasks, err := s.findViews(ctx, c)
		if err != nil {
			return nil, err
		}
		sortBoardTasks(tasks)
		board.Members = append(board.Members, model.BoardMemberTasks{Member: m, Tasks: tasks})
	}
	var uc taskCond
	uc.and("team_id = ? AND assignee_id IS NULL", teamID)
	uc.andCond(visibility)
	unassigned, err := s.findViews(ctx, uc)
	if err != nil {
		return nil, err
	}
	sortBoardTasks(unassigned)
	board.Unassigned = unassigned

	var staleOpen int64
	so := staleOpenCond(teamID, W)
	if err := so.apply(db.Model(&model.Task{})).Count(&staleOpen).Error; err != nil {
		return nil, err
	}
	board.StaleOpen = int(staleOpen)
	return board, nil
}

// TeamRollover returns a team's rollover candidates: open, undated tasks
// whose weekOf is before the current week, sorted weekOf asc then createdAt
// asc. Route-gated to ADMIN with no further scoping.
func (s *Store) TeamRollover(ctx context.Context, teamID string) (week string, tasks []model.TaskView, err error) {
	if err = s.Materialize(ctx); err != nil {
		return "", nil, err
	}
	db := s.db.WithContext(ctx)
	if _, err = teamByID(db, teamID); err != nil {
		return "", nil, err
	}
	W := model.CurrentPeriod(model.HorizonWeekly)
	rawTasks, err := listTasks(db, staleOpenCond(teamID, W), "week_of ASC, created_at ASC, id ASC", 0, 0)
	if err != nil {
		return "", nil, err
	}
	views, err := toViews(db, rawTasks)
	if err != nil {
		return "", nil, err
	}
	return W, views, nil
}

// defaultHistoryLimit/maxHistoryLimit bound TeamHistory's page size.
const (
	defaultHistoryLimit = 50
	maxHistoryLimit     = 200
)

// TeamHistory returns a team's completed-task history strictly before the
// current week, newest-first by createdAt, offset/limit paginated; hasMore
// via one extra row. Scoped like TeamBoard. Activity never loaded.
func (s *Store) TeamHistory(ctx context.Context, teamID string, offset, limit int) (tasks []model.TaskView, hasMore bool, err error) {
	if err = s.Materialize(ctx); err != nil {
		return nil, false, err
	}
	if err = forbidNonMember(ctx, teamID); err != nil {
		return nil, false, err
	}
	db := s.db.WithContext(ctx)
	if _, err = teamByID(db, teamID); err != nil {
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
	W := model.CurrentPeriod(model.HorizonWeekly)
	var c taskCond
	c.and("team_id = ?", teamID)
	c.and("status IN ?", terminalStatuses)
	c.and("week_of < ?", W)
	rawTasks, err := listTasks(db, c, "created_at DESC, id DESC", offset, limit+1)
	if err != nil {
		return nil, false, err
	}
	hasMore = len(rawTasks) > limit
	if hasMore {
		rawTasks = rawTasks[:limit]
	}
	views, err := toViews(db, rawTasks)
	if err != nil {
		return nil, false, err
	}
	return views, hasMore, nil
}

var priorityRank = map[string]int{"high": 3, "medium": 2, "low": 1, "": 0}

// sortBoardTasks sorts in place: open before closed, then priority desc,
// then dueDate ascending (tasks without a dueDate sort last).
func sortBoardTasks(tasks []model.TaskView) {
	closed := func(status string) bool { return status == model.StatusDone || status == model.StatusCancelled }
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if closed(a.Status) != closed(b.Status) {
			return !closed(a.Status)
		}
		if priorityRank[a.Priority] != priorityRank[b.Priority] {
			return priorityRank[a.Priority] > priorityRank[b.Priority]
		}
		ad, bd := string(a.DueDate), string(b.DueDate)
		if ad == "" {
			ad = "9999-99-99"
		}
		if bd == "" {
			bd = "9999-99-99"
		}
		return ad < bd
	})
}
