package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"

	"taskman/internal/model"
)

// ---- sessions ----
//
// Session storage, token minting, bcrypt and temp-password generation live
// in internal/repo (CreateSession/LoadSessionUser/Authenticate/...); this
// file keeps only the HTTP side: the cookie, the login backoff, and the
// auth handlers.

const sessionCookieName = "taskman_session"

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

func (a *api) meUser(ctx context.Context, u *model.CtxUser) (*MeUser, error) {
	teams, err := a.store.TeamsForUser(ctx, u)
	if err != nil {
		return nil, err
	}
	return &MeUser{
		ID:                 u.ID,
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
	u := model.UserFromContext(r.Context())
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
	u := model.UserFromContext(r.Context())
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
