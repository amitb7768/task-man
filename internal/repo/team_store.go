package repo

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"taskman/internal/model"
)

func mapTeamNameErr(err error) error {
	if violatedConstraint(err, sqlstateUniqueViolation) == "teams_name_unique" {
		return conflictErr("team name already exists")
	}
	return err
}

func (s *Store) CreateTeam(ctx context.Context, name string) (*model.Team, error) {
	if strings.TrimSpace(name) == "" {
		return nil, badRequest("name is required")
	}
	t := &model.Team{ID: NewID(), Name: name, CreatedAt: time.Now()}
	if err := s.db.WithContext(ctx).Create(t).Error; err != nil {
		return nil, mapTeamNameErr(err)
	}
	return t, nil
}

// teamScope applies the docs/AUTH_FEATURES.md matrix ("GET /api/teams | all
// teams | own teams only") to a teams query aliased as `col`'s table.
func teamScope(ctx context.Context, q *gorm.DB, col string) *gorm.DB {
	if u := model.UserFromContext(ctx); u != nil && u.SystemRole != model.RoleAdmin {
		// Empty TeamIDs renders as IN (NULL): no rows. (Mongo's $in over a
		// nil slice encoded as null and errored; an empty list is the
		// intended answer.)
		return q.Where(col+" IN ?", u.TeamIDs)
	}
	return q
}

// ListTeams returns every team for ADMIN (or no session), or only the
// caller's own teams for USER; sorted by name (COLLATE "C").
func (s *Store) ListTeams(ctx context.Context) ([]model.Team, error) {
	var teams []model.Team
	q := teamScope(ctx, s.db.WithContext(ctx).Model(&model.Team{}), "id")
	if err := q.Order("name").Find(&teams).Error; err != nil {
		return nil, err
	}
	return teams, nil
}

// ListTeamsWithCounts returns ListTeams plus memberCount (member_teams
// rows), openCount (todo/in_progress tasks with that team) and overdueCount
// (of those, dueDate present and < today). One query instead of the Mongo
// 3-counts-per-team loop; same numbers.
//
// NOTE: the Mongo method runs Materialize first so counts see spawned
// recurring instances. Materialize belongs to the task-domain repo (wave
// 1.1a); the caller must run it before this until it is wired in here.
func (s *Store) ListTeamsWithCounts(ctx context.Context) ([]model.TeamView, error) {
	// Mongo parity: counts must include recurring instances spawned for the
	// current period, so materialize first like every other read path.
	if err := s.Materialize(ctx); err != nil {
		return nil, err
	}
	today := model.CurrentPeriod(model.HorizonDaily)
	open := []string{model.StatusTodo, model.StatusInProgress}
	var rows []struct {
		ID           string
		Name         string
		CreatedAt    time.Time
		MemberCount  int
		OpenCount    int
		OverdueCount int
	}
	q := s.db.WithContext(ctx).Table("teams AS t").Select(`t.id, t.name, t.created_at,
		(SELECT count(*) FROM member_teams mt WHERE mt.team_id = t.id) AS member_count,
		(SELECT count(*) FROM tasks k WHERE k.team_id = t.id AND k.status IN ?) AS open_count,
		(SELECT count(*) FROM tasks k WHERE k.team_id = t.id AND k.status IN ?
			AND k.due_date IS NOT NULL AND k.due_date < ?) AS overdue_count`,
		open, open, today)
	q = teamScope(ctx, q, "t.id")
	if err := q.Order("t.name").Scan(&rows).Error; err != nil {
		return nil, err
	}
	views := make([]model.TeamView, 0, len(rows))
	for _, r := range rows {
		views = append(views, model.TeamView{
			Team:         model.Team{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt},
			MemberCount:  r.MemberCount,
			OpenCount:    r.OpenCount,
			OverdueCount: r.OverdueCount,
		})
	}
	return views, nil
}

func (s *Store) PatchTeam(ctx context.Context, id string, name string) (*model.Team, error) {
	if strings.TrimSpace(name) == "" {
		return nil, badRequest("name is required")
	}
	db := s.db.WithContext(ctx)
	res := db.Exec(`UPDATE teams SET name = ? WHERE id = ?`, name, id)
	if res.Error != nil {
		return nil, mapTeamNameErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, notFoundErr("team not found")
	}
	var team model.Team
	if err := db.Where("id = ?", id).Take(&team).Error; err != nil {
		return nil, err
	}
	return &team, nil
}

// DeleteTeam pre-counts tasks exactly like Mongo ("team has tasks" 409); the
// tasks.team_id RESTRICT FK maps to the same 409 if a task lands between the
// count and the delete. member_teams rows cascade (Mongo left dangling
// teamIds — accepted fix, decision #3).
func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	db := s.db.WithContext(ctx)
	var count int64
	if err := db.Table("tasks").Where("team_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return conflictErr("team has tasks")
	}
	res := db.Exec(`DELETE FROM teams WHERE id = ?`, id)
	if res.Error != nil {
		if violatedConstraint(res.Error, sqlstateFKViolation) == "tasks_team_id_fkey" {
			return conflictErr("team has tasks")
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return notFoundErr("team not found")
	}
	return nil
}
