package service

import (
	"context"

	"taskman/internal/model"
)

// ListTags returns the known-tag set for the filter bar
// (docs/DESIGN_V10_TAGS.md decision #5): every tag on an OPEN task the
// caller may see (taskScope — so an ADMIN never sees tag names from another
// user's personal tasks), with how many such tasks carry it, ordered count
// desc then tag asc. teamID, when set, narrows to that team's tasks (an
// unknown team yields an empty list, not a 404). closed counts done/cancelled
// tasks instead of open ones (the History view's filter bar). A pure read:
// Materialize is deliberately not run.
func (s *Service) ListTags(ctx context.Context, teamID *string, closed bool) ([]model.TagCount, error) {
	var c taskCond
	if u := model.UserFromContext(ctx); u != nil {
		c.andCond(taskScope(u))
	}
	statuses := openStatuses
	if closed {
		statuses = terminalStatuses
	}
	c.and("status IN ?", statuses)
	if teamID != nil {
		c.and("team_id = ?", *teamID)
	}
	var out []model.TagCount
	err := s.db.WithContext(ctx).Raw(`SELECT x.tag AS tag, count(*) AS count
		FROM tasks, jsonb_array_elements_text(tasks.tags) AS x(tag)
		WHERE `+c.sql()+`
		GROUP BY x.tag ORDER BY count DESC, x.tag ASC`, c.args...).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}
