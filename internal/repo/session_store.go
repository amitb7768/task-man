package repo

import (
	"context"
	"time"

	"taskman/internal/model"
)

// CreateSession mints a new session for userID and returns its token +
// expiry (now + SessionTTL). Port of server/auth.go CreateSession.
func (s *Store) CreateSession(ctx context.Context, userID string) (token string, expiresAt time.Time, err error) {
	token, err = generateSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(SessionTTL)
	sess := model.Session{ID: NewID(), Token: token, UserID: userID, ExpiresAt: expiresAt}
	if err := s.db.WithContext(ctx).Create(&sess).Error; err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// LoadSessionUser resolves a session token to its CtxUser, sliding the
// session's expiry forward (now + SessionTTL) on success. Returns ok=false
// for a missing/expired session, or one whose member no longer exists / has
// been disabled / lost login. The expiry check in code is the only
// enforcement between login sweeps (the Mongo TTL index is gone), so it is
// load-bearing now, not just defensive.
func (s *Store) LoadSessionUser(ctx context.Context, token string) (*model.CtxUser, time.Time, bool) {
	db := s.db.WithContext(ctx)
	var sess model.Session
	if err := db.Where("token = ?", token).Take(&sess).Error; err != nil {
		return nil, time.Time{}, false
	}
	if !time.Now().Before(sess.ExpiresAt) {
		return nil, time.Time{}, false
	}
	var m model.Member
	if err := db.Where("id = ?", sess.UserID).Take(&m).Error; err != nil {
		return nil, time.Time{}, false
	}
	if m.Disabled || m.PasswordHash == "" {
		return nil, time.Time{}, false
	}
	teamIDs, err := s.memberTeamIDs(ctx, m.ID)
	if err != nil {
		return nil, time.Time{}, false
	}
	m.TeamIDs = teamIDs
	newExpiry := time.Now().Add(SessionTTL)
	_ = db.Model(&model.Session{}).Where("id = ?", sess.ID).Update("expires_at", newExpiry).Error
	return memberToCtxUser(&m), newExpiry, true
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	return s.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE token = ?`, token).Error
}

// DeleteSessionsForUser removes every session belonging to userID (password
// reset/disable — docs/AUTH_FEATURES.md decision #9).
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID string) error {
	return s.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE user_id = ?`, userID).Error
}

// DeleteSessionsForUserExcept is DeleteSessionsForUser but keeps the caller's
// own current session alive — used by self-service change-password.
func (s *Store) DeleteSessionsForUserExcept(ctx context.Context, userID string, keepToken string) error {
	return s.db.WithContext(ctx).
		Exec(`DELETE FROM sessions WHERE user_id = ? AND token <> ?`, userID, keepToken).Error
}

// sweepExpiredSessions replaces the Mongo TTL index on expiresAt (contracted
// delta, docs/DESIGN_PG_FSM_MIGRATION.md): run opportunistically on every
// successful login. Best-effort — the caller ignores the error, and a
// stale row is harmless because LoadSessionUser rejects expired sessions in
// code. `<=` matches that check (expired iff !now.Before(expiresAt)).
func (s *Store) sweepExpiredSessions(ctx context.Context) error {
	return s.db.WithContext(ctx).Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now()).Error
}
