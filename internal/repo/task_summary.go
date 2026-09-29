package repo

// Task domain, part 4: GET /api/summary (docs/DESIGN_V9_NOTES_SUMMARY.md).

import (
	"context"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"taskman/internal/model"
)

// summaryClosedDate returns the local date a terminal task closed on: done ->
// completedAt's local date; cancelled -> the date of the LAST kind:"status"
// entry that moved it to cancelled. Falls back to updatedAt's local date only
// for tasks with no log at all.
func summaryClosedDate(t model.Task) string {
	switch t.Status {
	case model.StatusDone:
		if t.CompletedAt != nil {
			return localDate(*t.CompletedAt)
		}
	case model.StatusCancelled:
		for i := len(t.Activity) - 1; i >= 0; i-- {
			if e := t.Activity[i]; e.Kind == model.ActivityStatus && e.To == model.StatusCancelled {
				return e.Date
			}
		}
	default:
		return ""
	}
	if len(t.Activity) > 0 {
		return ""
	}
	return localDate(t.UpdatedAt)
}

// Summary reports what happened to the caller's tasks between two dates. One
// query (scope AND candidate) fetches the rows, their activity is loaded in
// one more (full docs — Summary needs the log), and classification happens
// in Go, verbatim from store.go.
func (s *Store) Summary(ctx context.Context, p model.SummaryParams) (*model.SummaryResult, error) {
	if p.From == "" || p.To == "" {
		return nil, badRequest("from and to are required")
	}
	fromT, err := model.ParseDate(p.From)
	if err != nil {
		return nil, badRequest("invalid from: %s", err.Error())
	}
	toT, err := model.ParseDate(p.To)
	if err != nil {
		return nil, badRequest("invalid to: %s", err.Error())
	}
	if p.From > p.To {
		return nil, badRequest("from must not be after to")
	}
	if p.AssigneeID != nil && p.TeamID == nil {
		return nil, badRequest("assigneeId requires teamId")
	}
	fromStart := time.Date(fromT.Year(), fromT.Month(), fromT.Day(), 0, 0, 0, 0, time.Local)
	toEnd := time.Date(toT.Year(), toT.Month(), toT.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)

	res := &model.SummaryResult{
		From:      p.From,
		To:        p.To,
		Completed: []model.SummaryTask{},
		Updated:   []model.SummaryTask{},
		Added:     []model.SummaryTask{},
	}

	db := s.db.WithContext(ctx)
	u := model.UserFromContext(ctx)
	var scope taskCond
	switch {
	case p.TeamID != nil:
		if err := forbidNonMember(ctx, *p.TeamID); err != nil {
			return nil, err
		}
		team, err := teamByID(db, *p.TeamID)
		if err != nil {
			return nil, err
		}
		res.TeamID, res.TeamName = p.TeamID, team.Name
		scope.and("team_id = ?", *p.TeamID)
		if p.AssigneeID != nil {
			scope.and("assignee_id = ?", *p.AssigneeID)
			res.AssigneeID = p.AssigneeID
		}
	case u != nil:
		scope.andCond(taskScope(u))
		scope.and("owner_id = ? OR assignee_id = ?", u.ID, u.ID)
	}

	// Materialize writes, so it goes AFTER every validation.
	if err := s.Materialize(ctx); err != nil {
		return nil, err
	}

	// Candidate filter. The activity arm is an EXISTS over ONE row whose date
	// is inside the range — the $elemMatch semantics (both bounds on the same
	// entry).
	var cand taskCond
	cand.and(`(completed_at >= ? AND completed_at < ?)
		OR (status = ? AND updated_at >= ? AND updated_at < ?)
		OR EXISTS (SELECT 1 FROM task_activity a WHERE a.task_id = tasks.id AND a.date >= ? AND a.date <= ?)
		OR (created_at >= ? AND created_at < ?)`,
		fromStart, toEnd,
		model.StatusCancelled, fromStart, toEnd,
		p.From, p.To,
		fromStart, toEnd)
	var where taskCond
	where.andCond(scope)
	where.andCond(cand)
	tasks, err := listTasks(db, where, "created_at ASC, id ASC", 0, 0)
	if err != nil {
		return nil, err
	}
	if err := loadActivityFor(db, tasks); err != nil {
		return nil, err
	}

	dueCutoff := p.To
	if today := model.CurrentPeriod(model.HorizonDaily); today < dueCutoff {
		dueCutoff = today
	}

	for _, t := range tasks {
		open := t.Status == model.StatusTodo || t.Status == model.StatusInProgress
		st := model.SummaryTask{
			ID:         t.ID,
			Title:      t.Title,
			Status:     t.Status,
			Priority:   t.Priority,
			Horizon:    t.Horizon,
			DueDate:    string(t.DueDate),
			TeamID:     t.TeamID,
			AssigneeID: t.AssigneeID,
			CreatedAt:  t.CreatedAt,
			Overdue:    open && t.DueDate != "" && string(t.DueDate) < dueCutoff,
			Notes:      []model.ActivityEntry{},
		}
		for _, e := range t.Activity {
			if e.Date >= p.From && e.Date <= p.To {
				st.Notes = append(st.Notes, e)
			}
		}
		sort.SliceStable(st.Notes, func(i, j int) bool {
			a, b := st.Notes[i], st.Notes[j]
			if a.Date != b.Date {
				return a.Date < b.Date
			}
			return a.At.Before(b.At)
		})

		closed := summaryClosedDate(t)
		switch {
		case !open && closed >= p.From && closed <= p.To:
			st.ClosedDate = closed
			res.Completed = append(res.Completed, st)
		case len(st.Notes) > 0:
			res.Updated = append(res.Updated, st)
		case open && t.Horizon != model.HorizonBacklog &&
			!t.CreatedAt.Before(fromStart) && t.CreatedAt.Before(toEnd):
			res.Added = append(res.Added, st)
		}
	}

	sections := []*[]model.SummaryTask{&res.Completed, &res.Updated, &res.Added}
	for _, sec := range sections {
		rows := *sec
		sort.SliceStable(rows, func(i, j int) bool {
			return strings.ToLower(rows[i].Title) < strings.ToLower(rows[j].Title)
		})
	}
	if err := resolveSummaryNames(db, res, sections); err != nil {
		return nil, err
	}
	return res, nil
}

// resolveSummaryNames fills in assignee and team display names with one IN
// query each rather than a lookup per row.
func resolveSummaryNames(db *gorm.DB, res *model.SummaryResult, sections []*[]model.SummaryTask) error {
	memberIDs := map[string]bool{}
	teamIDs := map[string]bool{}
	if res.AssigneeID != nil {
		memberIDs[*res.AssigneeID] = true
	}
	for _, sec := range sections {
		for _, row := range *sec {
			if row.AssigneeID != nil {
				memberIDs[*row.AssigneeID] = true
			}
			if row.TeamID != nil {
				teamIDs[*row.TeamID] = true
			}
		}
	}
	memberNames, err := summaryLookupNames(db, "members", memberIDs)
	if err != nil {
		return err
	}
	teamNames, err := summaryLookupNames(db, "teams", teamIDs)
	if err != nil {
		return err
	}
	if res.AssigneeID != nil {
		res.AssigneeName = memberNames[*res.AssigneeID]
	}
	for _, sec := range sections {
		rows := *sec
		for i := range rows {
			if rows[i].AssigneeID != nil {
				rows[i].AssigneeName = memberNames[*rows[i].AssigneeID]
			}
			if rows[i].TeamID != nil {
				rows[i].TeamName = teamNames[*rows[i].TeamID]
			}
		}
	}
	return nil
}

// summaryLookupNames fetches id -> name from teams or members in one query.
func summaryLookupNames(db *gorm.DB, table string, ids map[string]bool) (map[string]string, error) {
	names := map[string]string{}
	if len(ids) == 0 {
		return names, nil
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var rows []struct{ ID, Name string }
	if err := db.Table(table).Select("id, name").Where("id IN ?", list).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	return names, nil
}
