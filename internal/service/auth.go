package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"taskman/internal/model"
)

// loadMemberByID reads one member (with TeamIDs from the join), mapping
// absence to the Mongo-era "member not found" 404.
func (s *Service) loadMemberByID(ctx context.Context, id string) (*model.Member, error) {
	var m model.Member
	err := s.db.WithContext(ctx).Where("id = ?", id).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFoundErr("member not found")
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// Authenticate verifies email+password against a login-enabled member.
// Same generic 401 for unknown email / wrong password / not-login-enabled;
// a disabled account gets a distinct 403; lastLoginAt is stamped on
// success. The email match is byte-exact, as in Mongo (the unique index is
// lower()-scoped, so at most one login-enabled row can match).
//
// On success it also sweeps expired sessions (replaces the Mongo TTL index;
// best-effort — a sweep failure never fails the login).
func (s *Service) Authenticate(ctx context.Context, email, password string) (*model.CtxUser, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, unauthorizedErr("invalid email or password")
	}
	db := s.db.WithContext(ctx)
	var m model.Member
	err := db.Where("email = ? AND password_hash IS NOT NULL AND password_hash <> ''", email).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Burn the same bcrypt cost a real wrong-password check would.
		checkPassword(dummyPasswordHash, password)
		return nil, unauthorizedErr("invalid email or password")
	}
	if err != nil {
		return nil, err
	}
	if !checkPassword(string(m.PasswordHash), password) {
		return nil, unauthorizedErr("invalid email or password")
	}
	if m.Disabled {
		return nil, forbiddenErr("account disabled")
	}
	now := time.Now()
	if err := db.Model(&model.Member{}).Where("id = ?", m.ID).Update("last_login_at", now).Error; err != nil {
		return nil, err
	}
	m.LastLoginAt = &now
	teamIDs, err := s.memberTeamIDs(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	m.TeamIDs = teamIDs
	_ = s.sweepExpiredSessions(ctx)
	return memberToCtxUser(&m), nil
}

// ChangePassword verifies the current password, sets the new one (clearing
// mustChangePassword), and kills every other session for the user, keeping
// only keepToken alive. The update and the session kill commit together.
func (s *Service) ChangePassword(ctx context.Context, id string, current, newPW, keepToken string) error {
	m, err := s.loadMemberByID(ctx, id)
	if err != nil {
		return err
	}
	if !checkPassword(string(m.PasswordHash), current) {
		return badRequest("current password is incorrect")
	}
	hash, err := hashPassword(newPW)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`UPDATE members SET password_hash = ?, must_change_password = FALSE WHERE id = ?`, hash, id).Error
		if err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM sessions WHERE user_id = ? AND token <> ?`, id, keepToken).Error
	})
}

// EnableLogin provisions login for an assignable-only member: temp
// password, systemRole, mustChangePassword=true, disabled=false. Returns
// the temp password (shown once). 409 if login is already enabled.
func (s *Service) EnableLogin(ctx context.Context, id string, systemRole string) (string, error) {
	if !validSystemRole(systemRole) {
		return "", badRequest("systemRole must be %q or %q", model.RoleAdmin, model.RoleUser)
	}
	m, err := s.loadMemberByID(ctx, id)
	if err != nil {
		return "", err
	}
	if m.PasswordHash != "" {
		return "", conflictErr("login already enabled for this member")
	}
	if strings.TrimSpace(string(m.Email)) == "" {
		return "", badRequest("member has no email; set one before enabling login")
	}
	tempPW, err := generateTempPassword()
	if err != nil {
		return "", err
	}
	hash, err := hashPassword(tempPW)
	if err != nil {
		return "", err
	}
	err = s.db.WithContext(ctx).Exec(`UPDATE members SET password_hash = ?, system_role = ?,
		must_change_password = TRUE, disabled = FALSE WHERE id = ?`, hash, systemRole, id).Error
	if err != nil {
		if violatedConstraint(err, sqlstateUniqueViolation) == "members_email_login_unique" {
			return "", conflictErr("email already in use by another login-enabled member")
		}
		return "", err
	}
	return tempPW, nil
}

// ResetPassword generates a new temp password for a login-enabled member,
// sets mustChangePassword, and kills all of their sessions (one tx).
func (s *Service) ResetPassword(ctx context.Context, id string) (string, error) {
	m, err := s.loadMemberByID(ctx, id)
	if err != nil {
		return "", err
	}
	if m.PasswordHash == "" {
		return "", conflictErr("login not enabled for this member")
	}
	tempPW, err := generateTempPassword()
	if err != nil {
		return "", err
	}
	hash, err := hashPassword(tempPW)
	if err != nil {
		return "", err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`UPDATE members SET password_hash = ?, must_change_password = TRUE WHERE id = ?`, hash, id).Error
		if err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id).Error
	})
	if err != nil {
		return "", err
	}
	return tempPW, nil
}

// TeamsForUser returns the teams u belongs to (for GET /api/auth/me). Mongo
// returned $in matches in natural (≈insertion) order; created_at, id is the
// stand-in.
func (s *Service) TeamsForUser(ctx context.Context, u *model.CtxUser) ([]model.Team, error) {
	if len(u.TeamIDs) == 0 {
		return nil, nil
	}
	var teams []model.Team
	err := s.db.WithContext(ctx).Where("id IN ?", u.TeamIDs).Order("created_at, id").Find(&teams).Error
	if err != nil {
		return nil, err
	}
	return teams, nil
}
