package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"taskman/internal/model"
)

// ---- member_teams join (replaces Member.teamIds) ----

// memberTeamOrder is the read order of a member's team ids: the pos column
// preserves Mongo's client-written teamIds array order (writeMemberTeams
// assigns it; ctid was rejected at review — physical order isn't a
// contract).
const memberTeamOrder = "pos"

// memberTeamIDs loads one member's team ids.
func (s *Store) memberTeamIDs(ctx context.Context, memberID string) ([]string, error) {
	m, err := loadMemberTeamIDs(s.db.WithContext(ctx), []string{memberID})
	if err != nil {
		return nil, err
	}
	return m[memberID], nil
}

// loadMemberTeamIDs loads team ids for many members in one query (no N+1).
// Members with no rows are absent from the map (TeamIDs nil — same as
// Mongo's omitempty-absent teamIds).
func loadMemberTeamIDs(db *gorm.DB, memberIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(memberIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		MemberID string
		TeamID   string
	}
	err := db.Raw(`SELECT member_id, team_id FROM member_teams WHERE member_id IN ? ORDER BY `+memberTeamOrder, memberIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.MemberID] = append(out[r.MemberID], r.TeamID)
	}
	return out, nil
}

// writeMemberTeams replaces memberID's join rows with teamIDs, in order.
// Must run inside the transaction that writes the member row. A duplicate
// id in teamIDs is collapsed (PK) rather than stored twice as Mongo's array
// would have.
func writeMemberTeams(tx *gorm.DB, memberID string, teamIDs []string) error {
	if err := tx.Exec(`DELETE FROM member_teams WHERE member_id = ?`, memberID).Error; err != nil {
		return err
	}
	if len(teamIDs) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString(`INSERT INTO member_teams (member_id, team_id, pos) VALUES `)
	args := make([]any, 0, 3*len(teamIDs))
	for i, tid := range teamIDs {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?, ?)")
		args = append(args, memberID, tid, i)
	}
	// A duplicate id keeps its FIRST position (first write wins the PK).
	sb.WriteString(` ON CONFLICT DO NOTHING`)
	return tx.Exec(sb.String(), args...).Error
}

// validateTeamIDs pre-checks every id exists, in order, with the Mongo-era
// "team not found: <id>" message (not left to the member_teams FK). The
// FOR KEY SHARE lock holds each team against a concurrent DeleteTeam until
// the enclosing transaction commits, so the check can't go stale before
// the join rows are written.
func validateTeamIDs(tx *gorm.DB, teamIDs []string) error {
	for _, tid := range teamIDs {
		var found []string
		if err := tx.Raw(`SELECT id FROM teams WHERE id = ? FOR KEY SHARE`, tid).Scan(&found).Error; err != nil {
			return err
		}
		if len(found) == 0 {
			return badRequest("team not found: %s", tid)
		}
	}
	return nil
}

// mapMemberWriteErr translates constraint violations on a member write to
// the Mongo-era responses.
func mapMemberWriteErr(err error, m *model.Member) error {
	if violatedConstraint(err, sqlstateUniqueViolation) == "members_email_login_unique" {
		return conflictErr("email already in use")
	}
	if violatedConstraint(err, sqlstateCheckViolation) == "members_system_role_check" {
		return badRequest("invalid systemRole %q", m.SystemRole)
	}
	return err
}

// ---- members ----

func (s *Store) CreateMember(ctx context.Context, m *model.Member) (*model.Member, error) {
	if strings.TrimSpace(m.Name) == "" {
		return nil, badRequest("name is required")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := validateTeamIDs(tx, m.TeamIDs); err != nil {
			return err
		}
		m.ID = NewID()
		m.CreatedAt = time.Now()
		if err := tx.Create(m).Error; err != nil {
			return err
		}
		return writeMemberTeams(tx, m.ID, m.TeamIDs)
	})
	if err != nil {
		return nil, mapMemberWriteErr(err, m)
	}
	return m, nil
}

// ListMembers returns all members, or those in teamID (via member_teams),
// sorted by name (COLLATE "C" = Mongo binary order); ties broken by
// creation order, standing in for Mongo's natural order.
func (s *Store) ListMembers(ctx context.Context, teamID *string) ([]model.Member, error) {
	db := s.db.WithContext(ctx)
	q := db.Model(&model.Member{})
	if teamID != nil {
		q = q.Where("id IN (SELECT member_id FROM member_teams WHERE team_id = ?)", *teamID)
	}
	var members []model.Member
	if err := q.Order("name, created_at, id").Find(&members).Error; err != nil {
		return nil, err
	}
	ids := make([]string, len(members))
	for i := range members {
		ids[i] = members[i].ID
	}
	teams, err := loadMemberTeamIDs(db, ids)
	if err != nil {
		return nil, err
	}
	for i := range members {
		members[i].TeamIDs = teams[members[i].ID]
	}
	return members, nil
}

// PatchMember applies a partial JSON update onto the stored member (same
// unmarshal-over-the-current-record semantics as the Mongo ReplaceOne), and
// writes row + join rows in one transaction. Disabling a previously enabled
// member kills all of their sessions (decision #8/#9), in the same tx.
func (s *Store) PatchMember(ctx context.Context, id string, raw []byte) (*model.Member, error) {
	var m model.Member
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&m).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notFoundErr("member not found")
		}
		if err != nil {
			return err
		}
		teams, err := loadMemberTeamIDs(tx, []string{id})
		if err != nil {
			return err
		}
		m.TeamIDs = teams[id]
		wasDisabled := m.Disabled
		if err := json.Unmarshal(raw, &m); err != nil {
			return badRequest("invalid JSON: %s", err.Error())
		}
		m.ID = id
		if strings.TrimSpace(m.Name) == "" {
			return badRequest("name is required")
		}
		if m.SystemRole != "" && !validSystemRole(m.SystemRole) {
			return badRequest("invalid systemRole %q", m.SystemRole)
		}
		if err := validateTeamIDs(tx, m.TeamIDs); err != nil {
			return err
		}
		// Whole-record replace, like ReplaceOne: every column is rewritten
		// from m (including createdAt/lastLoginAt if the patch carried them,
		// exactly as the Mongo unmarshal-then-replace allowed).
		err = tx.Exec(`UPDATE members SET name = ?, email = ?, role = ?, password_hash = ?,
			system_role = ?, must_change_password = ?, disabled = ?, last_login_at = ?, created_at = ?
			WHERE id = ?`,
			m.Name, m.Email, m.Role, m.PasswordHash,
			m.SystemRole, m.MustChangePassword, m.Disabled, m.LastLoginAt, m.CreatedAt,
			id).Error
		if err != nil {
			return err
		}
		if err := writeMemberTeams(tx, id, m.TeamIDs); err != nil {
			return err
		}
		if m.Disabled && !wasDisabled {
			return tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id).Error
		}
		return nil
	})
	if err != nil {
		return nil, mapMemberWriteErr(err, &m)
	}
	return &m, nil
}

// DeleteMember hard-deletes an assignable-only member. Login-enabled members
// (password_hash set) 409 — decision #8: disable instead. The Mongo code's
// explicit follow-ups are now FK actions: tasks.assignee_id SET NULL,
// task_activity.by_id SET NULL, member_teams + sessions CASCADE.
func (s *Store) DeleteMember(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m model.Member
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&m).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return notFoundErr("member not found")
		}
		if err != nil {
			return err
		}
		if m.PasswordHash != "" {
			return conflictErr("member has login enabled; disable instead of deleting")
		}
		return tx.Exec(`DELETE FROM members WHERE id = ?`, id).Error
	})
}
