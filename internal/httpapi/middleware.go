package httpapi

import (
	"log"
	"net/http"
	"strings"
	"time"

	"taskman/internal/model"
)

// ---- request-scoped user (ctx plumbing) ----
//
// The authenticated caller is a *model.CtxUser stored under internal/model's
// context key (model.WithUser / model.UserFromContext) — the service layer reads
// the caller from THAT key for every scoping decision, so the middleware
// writes it there directly.

// ---- middleware chain ----

// middleware wraps an http.Handler; a.sessionLoad (a bound method) also
// satisfies this shape, so the global chain can mix stateless functions and
// api-bound ones.
type middleware func(http.Handler) http.Handler

// chain applies mws around h in order, so mws[0] is the outermost handler
// (runs first). Mirrors the documented pipeline: requestLog -> jsonGuard ->
// sessionLoad -> mustChangePasswordGate -> mux.
func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// statusRecorder captures the status code written by the wrapped handler so
// requestLog can log it (http.ResponseWriter alone doesn't expose it).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// requestLog logs method, path, status, and duration for every request.
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

// jsonGuardExempt lists API routes that are POST/PATCH/DELETE but must not
// require a JSON body (logout takes none).
var jsonGuardExempt = map[string]bool{
	"/api/auth/logout": true,
}

func isMutatingMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPatch || m == http.MethodDelete || m == http.MethodPut
}

// jsonGuard requires Content-Type: application/json on state-changing API
// requests (POST/PATCH/DELETE under /api/, except the exempt set). Paired
// with SameSite=Lax cookies, this is the app's CSRF mitigation (see
// docs/AUTH_FEATURES.md decision #10): a cross-site form/script can't set an
// arbitrary Content-Type on a simple request without triggering a CORS
// preflight, which this server won't answer.
func jsonGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && isMutatingMethod(r.Method) && !jsonGuardExempt[r.URL.Path] {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				writeErr(w, unsupportedMediaErr("Content-Type must be application/json"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sessionLoad reads the taskman_session cookie, resolves it to a live
// session + member, and (on success) attaches a *model.CtxUser to the request
// context and slides the session's expiry. A missing/invalid/expired cookie
// is not an error here — it just leaves the context without a user; routes
// that require auth reject that downstream via requireAuth/requireAdmin.
func (a *api) sessionLoad(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, newExpiry, ok := a.store.LoadSessionUser(r.Context(), c.Value)
		if !ok {
			clearSessionCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		setSessionCookie(w, c.Value, newExpiry)
		next.ServeHTTP(w, r.WithContext(model.WithUser(r.Context(), u)))
	})
}

// mustChangeExempt is the set of paths a mustChangePassword session may
// still mutate: change-password itself, plus logout — a forced-change
// session must be able to sign out rather than being soft-locked with no
// escape besides completing the change (docs/AUTH_FEATURES.md decision #6).
var mustChangeExempt = map[string]bool{
	"/api/auth/change-password": true,
	"/api/auth/logout":          true,
}

// mustChangePasswordGate 403s any mutating request from a session flagged
// mustChangePassword, except change-password and logout. Reads always pass
// through so the SPA shell can still render (fetch /api/auth/me etc.).
func mustChangePasswordGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := model.UserFromContext(r.Context())
		if u != nil && u.MustChangePassword && isMutatingMethod(r.Method) && !mustChangeExempt[r.URL.Path] {
			writeErr(w, forbiddenErr("password change required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth is a route-level wrapper: 401 if sessionLoad didn't attach a
// user, otherwise delegates to next.
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if model.UserFromContext(r.Context()) == nil {
			writeErr(w, unauthorizedErr("authentication required"))
			return
		}
		next(w, r)
	}
}

// requireAdmin is requireAuth plus a systemRole==ADMIN check (403 otherwise).
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if model.UserFromContext(r.Context()).SystemRole != RoleAdmin {
			writeErr(w, forbiddenErr("admin only"))
			return
		}
		next(w, r)
	})
}
