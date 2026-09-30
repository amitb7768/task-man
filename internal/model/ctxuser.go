package model

import "context"

// CtxUser is the authenticated caller, attached to the request context by the
// session middleware once the session cookie resolves to a live session +
// member. It carries just enough to drive every scoping decision in the repo
// layer without re-fetching the member row per call.
//
// It mirrors server/middleware.go's ctxUser with ids as strings; the server
// package switches to this type (and this context key) in wave 1.2 so the
// repo layer reads exactly what the middleware wrote.
type CtxUser struct {
	ID                 string
	Name               string
	Email              string
	SystemRole         string
	TeamIDs            []string
	MustChangePassword bool
	Disabled           bool
}

type ctxKey int

const userCtxKey ctxKey = iota

// WithUser attaches the authenticated caller to ctx.
func WithUser(ctx context.Context, u *CtxUser) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// UserFromContext returns the authenticated caller, or nil if the request
// carried no valid session (public routes, or session load found nothing).
func UserFromContext(ctx context.Context) *CtxUser {
	u, _ := ctx.Value(userCtxKey).(*CtxUser)
	return u
}

// InTeam reports whether u belongs to teamID.
func (u *CtxUser) InTeam(teamID string) bool {
	for _, id := range u.TeamIDs {
		if id == teamID {
			return true
		}
	}
	return false
}
