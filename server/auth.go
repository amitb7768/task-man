package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/crypto/bcrypt"
)

// ---- sessions ----

const (
	sessionCookieName = "taskman_session"
	sessionTTL        = 12 * time.Hour
	bcryptCost        = 10
)

// Session is a Mongo-backed opaque-token session (docs/AUTH_FEATURES.md
// decision #9): token is a random 256-bit hex string, unique-indexed.
// expiresAt slides forward on every authenticated request (see
// Store.LoadSessionUser) and is enforced both defensively in code and by a
// Mongo TTL index (see EnsureIndexes) so idle sessions self-clean.
type Session struct {
	ID        bson.ObjectID `bson:"_id"`
	Token     string        `bson:"token"`
	UserID    bson.ObjectID `bson:"userId"`
	ExpiresAt time.Time     `bson:"expiresAt"`
}

func generateSessionToken() (string, error) {
	b := make([]byte, 32) // 256 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateSession mints a new session for userID and returns its token +
// expiry (now + sessionTTL).
func (s *Store) CreateSession(ctx context.Context, userID bson.ObjectID) (token string, expiresAt time.Time, err error) {
	token, err = generateSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(sessionTTL)
	sess := Session{ID: bson.NewObjectID(), Token: token, UserID: userID, ExpiresAt: expiresAt}
	if _, err := s.sessions.InsertOne(ctx, sess); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// LoadSessionUser resolves a session token to its ctxUser, sliding the
// session's expiry forward (now + sessionTTL) on success. Returns
// ok=false for a missing/expired session, or one whose member no longer
// exists / has been disabled / lost login (defensive: disable and
// password-reset already delete sessions synchronously, so this path is a
// backstop, not the primary enforcement).
func (s *Store) LoadSessionUser(ctx context.Context, token string) (*ctxUser, time.Time, bool) {
	var sess Session
	if err := s.sessions.FindOne(ctx, bson.M{"token": token}).Decode(&sess); err != nil {
		return nil, time.Time{}, false
	}
	if !time.Now().Before(sess.ExpiresAt) {
		return nil, time.Time{}, false
	}
	var m Member
	if err := s.members.FindOne(ctx, bson.M{"_id": sess.UserID}).Decode(&m); err != nil {
		return nil, time.Time{}, false
	}
	if m.Disabled || m.PasswordHash == "" {
		return nil, time.Time{}, false
	}
	newExpiry := time.Now().Add(sessionTTL)
	_, _ = s.sessions.UpdateOne(ctx, bson.M{"_id": sess.ID}, bson.M{"$set": bson.M{"expiresAt": newExpiry}})
	return memberToCtxUser(&m), newExpiry, true
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.sessions.DeleteOne(ctx, bson.M{"token": token})
	return err
}

// DeleteSessionsForUser removes every session belonging to userID — used by
// password reset/disable (docs/AUTH_FEATURES.md decision #9: "ALL sessions
// invalidated on password change/reset/disable").
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID bson.ObjectID) error {
	_, err := s.sessions.DeleteMany(ctx, bson.M{"userId": userID})
	return err
}

// DeleteSessionsForUserExcept is DeleteSessionsForUser but keeps the caller's
// own current session alive — used by self-service change-password.
func (s *Store) DeleteSessionsForUserExcept(ctx context.Context, userID bson.ObjectID, keepToken string) error {
	_, err := s.sessions.DeleteMany(ctx, bson.M{"userId": userID, "token": bson.M{"$ne": keepToken}})
	return err
}

func setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

// ---- passwords ----

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func checkPassword(hash, pw string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyPasswordHash is a fixed bcrypt hash with no real backing password,
// computed once at startup. Authenticate compares against it on an unknown
// email so that path costs about the same wall-clock time as a real
// wrong-password rejection — otherwise skipping bcrypt entirely on unknown
// emails is a timing oracle an attacker can use to enumerate valid login
// emails, even though the response body/status stay identical either way.
var dummyPasswordHash = mustHashForTiming()

func mustHashForTiming() string {
	h, err := hashPassword("timing-oracle-mitigation-fixed-dummy")
	if err != nil {
		panic(err)
	}
	return h
}

// tempPasswordChars excludes visually-confusable characters (0/O, 1/l/I) —
// temp passwords are shown once to an admin who has to read/copy them.
const tempPasswordChars = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

// generateTempPassword returns a random 12-character password from
// tempPasswordChars, for admin-provisioned accounts (enable-login /
// reset-password / env-seed bootstrap).
func generateTempPassword() (string, error) {
	const n = 12
	out := make([]byte, n)
	max := big.NewInt(int64(len(tempPasswordChars)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = tempPasswordChars[idx.Int64()]
	}
	return string(out), nil
}

// ---- login backoff ----

// loginLimiter is a naive per-IP failure counter (docs/AUTH_FEATURES.md
// decision #10): each login attempt sleeps 500ms * recent-failure-count
// (capped 5s) before being processed, and the counter resets on success.
// No time-decay — deliberately simple, matching "naive" in the contract; a
// shared/NAT IP that fails once stays slowed down until someone from that IP
// succeeds.
type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]int
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string]int{}}
}

// backoffDelay is the pure computation loginLimiter.wait sleeps for, split
// out for testability.
func backoffDelay(recentFailures int) time.Duration {
	if recentFailures <= 0 {
		return 0
	}
	d := time.Duration(recentFailures) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

func (l *loginLimiter) wait(ip string) {
	l.mu.Lock()
	n := l.fails[ip]
	l.mu.Unlock()
	if d := backoffDelay(n); d > 0 {
		time.Sleep(d)
	}
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	l.fails[ip]++
	l.mu.Unlock()
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.fails, ip)
	l.mu.Unlock()
}

// clientIP extracts the request's peer IP from RemoteAddr. This is a LAN
// tool with no reverse proxy in front of it, so RemoteAddr (not
// X-Forwarded-For, which a client could spoof) is the right source.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- ctxUser <-> Member ----

func memberToCtxUser(m *Member) *ctxUser {
	return &ctxUser{
		ID:                 m.ID,
		Name:               m.Name,
		Email:              m.Email,
		SystemRole:         m.SystemRole,
		TeamIDs:            append([]bson.ObjectID(nil), m.TeamIDs...),
		MustChangePassword: m.MustChangePassword,
		Disabled:           m.Disabled,
	}
}

// ---- store: authenticate / change / provision ----

// Authenticate verifies email+password against a login-enabled member.
// Always returns the same generic 401 for unknown email / wrong password /
// not-login-enabled, to avoid leaking which case applied; a disabled
// account gets a distinct 403 (per docs/AUTH_FEATURES.md: "disabled ->
// 403"), and lastLoginAt is stamped on success.
func (s *Store) Authenticate(ctx context.Context, email, password string) (*ctxUser, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, unauthorizedErr("invalid email or password")
	}
	var m Member
	err := s.members.FindOne(ctx, bson.M{
		"email":        email,
		"passwordHash": bson.M{"$exists": true, "$ne": ""},
	}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		// Burn the same bcrypt cost a real wrong-password check would, so
		// the generic 401 doesn't arrive measurably faster for an email
		// that doesn't exist (see dummyPasswordHash).
		checkPassword(dummyPasswordHash, password)
		return nil, unauthorizedErr("invalid email or password")
	}
	if err != nil {
		return nil, err
	}
	if !checkPassword(m.PasswordHash, password) {
		return nil, unauthorizedErr("invalid email or password")
	}
	if m.Disabled {
		return nil, forbiddenErr("account disabled")
	}
	now := time.Now()
	if _, err := s.members.UpdateOne(ctx, bson.M{"_id": m.ID}, bson.M{"$set": bson.M{"lastLoginAt": now}}); err != nil {
		return nil, err
	}
	m.LastLoginAt = &now
	return memberToCtxUser(&m), nil
}

// ChangePassword verifies the current password, sets the new one (clearing
// mustChangePassword), and kills every other session for the user, keeping
// only keepToken (the session making this request) alive.
func (s *Store) ChangePassword(ctx context.Context, id bson.ObjectID, current, newPW, keepToken string) error {
	var m Member
	err := s.members.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return notFoundErr("member not found")
	}
	if err != nil {
		return err
	}
	if !checkPassword(m.PasswordHash, current) {
		return badRequest("current password is incorrect")
	}
	hash, err := hashPassword(newPW)
	if err != nil {
		return err
	}
	_, err = s.members.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"passwordHash":       hash,
		"mustChangePassword": false,
	}})
	if err != nil {
		return err
	}
	return s.DeleteSessionsForUserExcept(ctx, id, keepToken)
}

func validSystemRole(r string) bool {
	return r == RoleAdmin || r == RoleUser
}

// EnableLogin provisions login for a previously assignable-only member:
// generates a temp password, sets systemRole + mustChangePassword, and
// returns the temp password (shown once). 409s if login is already enabled.
func (s *Store) EnableLogin(ctx context.Context, id bson.ObjectID, systemRole string) (string, error) {
	if !validSystemRole(systemRole) {
		return "", badRequest("systemRole must be %q or %q", RoleAdmin, RoleUser)
	}
	var m Member
	err := s.members.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", notFoundErr("member not found")
	}
	if err != nil {
		return "", err
	}
	if m.PasswordHash != "" {
		return "", conflictErr("login already enabled for this member")
	}
	if strings.TrimSpace(m.Email) == "" {
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
	_, err = s.members.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"passwordHash":       hash,
		"systemRole":         systemRole,
		"mustChangePassword": true,
		"disabled":           false,
	}})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return "", conflictErr("email already in use by another login-enabled member")
		}
		return "", err
	}
	return tempPW, nil
}

// ResetPassword generates a new temp password for an already login-enabled
// member, sets mustChangePassword, and kills all of their sessions.
func (s *Store) ResetPassword(ctx context.Context, id bson.ObjectID) (string, error) {
	var m Member
	err := s.members.FindOne(ctx, bson.M{"_id": id}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", notFoundErr("member not found")
	}
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
	_, err = s.members.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{
		"passwordHash":       hash,
		"mustChangePassword": true,
	}})
	if err != nil {
		return "", err
	}
	if err := s.DeleteSessionsForUser(ctx, id); err != nil {
		return "", err
	}
	return tempPW, nil
}

// TeamsForUser returns the teams u belongs to (for GET /api/auth/me).
func (s *Store) TeamsForUser(ctx context.Context, u *ctxUser) ([]Team, error) {
	if len(u.TeamIDs) == 0 {
		return nil, nil
	}
	cur, err := s.teams.Find(ctx, bson.M{"_id": bson.M{"$in": u.TeamIDs}})
	if err != nil {
		return nil, err
	}
	var teams []Team
	if err := cur.All(ctx, &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

// ---- HTTP handlers ----

// MeUser is the wire shape of the authenticated user, returned by both
// POST /api/auth/login and GET /api/auth/me.
type MeUser struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Email              string `json:"email"`
	SystemRole         string `json:"systemRole"`
	MustChangePassword bool   `json:"mustChangePassword"`
	Teams              []Team `json:"teams"`
}

func (a *api) meUser(ctx context.Context, u *ctxUser) (*MeUser, error) {
	teams, err := a.store.TeamsForUser(ctx, u)
	if err != nil {
		return nil, err
	}
	return &MeUser{
		ID:                 u.ID.Hex(),
		Name:               u.Name,
		Email:              u.Email,
		SystemRole:         u.SystemRole,
		MustChangePassword: u.MustChangePassword,
		Teams:              orEmpty(teams),
	}, nil
}

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	a.loginLimiter.wait(ip)

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.loginLimiter.fail(ip)
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	u, err := a.store.Authenticate(r.Context(), body.Email, body.Password)
	if err != nil {
		a.loginLimiter.fail(ip)
		writeErr(w, err)
		return
	}
	a.loginLimiter.reset(ip)

	token, expiresAt, err := a.store.CreateSession(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	setSessionCookie(w, token, expiresAt)

	me, err := a.meUser(r.Context(), u)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": me})
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		_ = a.store.DeleteSession(r.Context(), c.Value)
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	me, err := a.meUser(r.Context(), u)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": me})
}

func (a *api) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	if len(body.New) < 8 {
		writeErr(w, badRequest("new password must be at least 8 characters"))
		return
	}
	u := userFromContext(r.Context())
	token := ""
	if c, err := r.Cookie(sessionCookieName); err == nil {
		token = c.Value
	}
	if err := a.store.ChangePassword(r.Context(), u.ID, body.Current, body.New, token); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) enableLogin(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		SystemRole string `json:"systemRole"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, badRequest("invalid JSON: %s", err.Error()))
		return
	}
	tempPW, err := a.store.EnableLogin(r.Context(), id, body.SystemRole)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tempPassword": tempPW})
}

func (a *api) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	tempPW, err := a.store.ResetPassword(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tempPassword": tempPW})
}
