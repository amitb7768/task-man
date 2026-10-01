package service

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"taskman/internal/model"
)

// ListTags returns the filter bar's tag set (docs/DESIGN_V11_TAG_CATALOG.md
// decision #4, on top of DESIGN_V10_TAGS.md decision #5): EVERY catalog tag,
// ordered name asc, each with how many OPEN tasks the caller may see carry it
// (count 0 included). The counts stay scope-bound (taskScope — an ADMIN never
// counts another user's personal tasks); the names are global admin-authored
// catalog entries, so listing them leaks nothing. teamID, when set, narrows
// the counts to that team's tasks (an unknown team yields all-zero counts,
// not a 404). closed counts done/cancelled tasks instead of open ones (the
// History view's filter bar). A pure read: Materialize is deliberately not run.
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
	err := s.db.WithContext(ctx).Raw(`SELECT g.name AS tag, count(t.id) AS count
		FROM tags g
		LEFT JOIN (SELECT id, tags FROM tasks WHERE `+c.sql()+`) t
		  ON t.tags @> jsonb_build_array(g.name)
		GROUP BY g.name ORDER BY g.name ASC`, c.args...).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// unknownTag returns the first of tags (already normalised) that is not in
// the catalog, or "" when all are. db is s.db or the caller's tx.
func unknownTag(db *gorm.DB, tags []string) (string, error) {
	if len(tags) == 0 {
		return "", nil
	}
	var known []string
	if err := db.Raw(`SELECT name FROM tags WHERE name IN ?`, tags).Scan(&known).Error; err != nil {
		return "", err
	}
	have := make(map[string]bool, len(known))
	for _, n := range known {
		have[n] = true
	}
	for _, tag := range tags {
		if !have[tag] {
			return tag, nil
		}
	}
	return "", nil
}

// requireAdminCaller mirrors the route's requireAdmin inside the service (a
// nil user — no session, e.g. internal callers — is allowed, as elsewhere).
func requireAdminCaller(ctx context.Context) error {
	if u := model.UserFromContext(ctx); u != nil && u.SystemRole != model.RoleAdmin {
		return forbiddenErr("admin only")
	}
	return nil
}

// ListCatalog returns every catalog tag, name asc (the TaskDetail picker).
func (s *Service) ListCatalog(ctx context.Context) ([]model.Tag, error) {
	var out []model.Tag
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	for i := range out {
		out[i].CreatedAt = out[i].CreatedAt.UTC()
	}
	return out, nil
}

// CreateTag adds name to the catalog (ADMIN only). name is normalised with
// the same rules as task tags and must yield exactly one tag.
func (s *Service) CreateTag(ctx context.Context, name string) (*model.Tag, error) {
	if err := requireAdminCaller(ctx); err != nil {
		return nil, err
	}
	norm, err := model.NormalizeTags([]string{name})
	if err != nil {
		return nil, badRequest("%s", err.Error())
	}
	if len(norm) != 1 {
		return nil, badRequest("name is required")
	}
	tag := &model.Tag{Name: norm[0], CreatedAt: dbNow().UTC()}
	if u := model.UserFromContext(ctx); u != nil {
		id := u.ID
		tag.CreatedBy = &id
	}
	if err := s.db.WithContext(ctx).Create(tag).Error; err != nil {
		if violatedConstraint(err, sqlstateUniqueViolation) == "tags_pkey" {
			return nil, conflictErr("tag %q already exists", tag.Name)
		}
		return nil, err
	}
	return tag, nil
}

// DeleteTag removes name from the catalog (ADMIN only). Refused (409) while
// any task carries it; 404 when it is not in the catalog. name is matched
// exactly as given (catalog names are already normalised).
func (s *Service) DeleteTag(ctx context.Context, name string) error {
	if err := requireAdminCaller(ctx); err != nil {
		return err
	}
	b, _ := json.Marshal([]string{name}) // a []string cannot fail to marshal
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the catalog row first so the in-use count and the delete see
		// one consistent state against concurrent catalog edits.
		var found []string
		if err := tx.Raw(`SELECT name FROM tags WHERE name = ? FOR UPDATE`, name).Scan(&found).Error; err != nil {
			return err
		}
		if len(found) == 0 {
			return notFoundErr("tag not found")
		}
		var n int64
		if err := tx.Raw(`SELECT count(*) FROM tasks WHERE tags @> ?::jsonb`, string(b)).Scan(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return conflictErr("tag %q is in use by %d tasks", name, n)
		}
		return tx.Exec(`DELETE FROM tags WHERE name = ?`, name).Error
	})
}
