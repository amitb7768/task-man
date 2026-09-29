package model

// TeamView is a Team plus derived member/task counts for the teams landing
// page. Mirrors server/store.go's TeamView; JSON tags byte-identical (the
// embedded Team's fields are promoted, exactly as in the Mongo struct).
//
// MeUser (server/auth.go) is deliberately NOT ported here: it is assembled
// by the HTTP handler (api.meUser) from a CtxUser + Store.TeamsForUser, not
// returned by any Store method, so it moves with the handlers in wave 3.1.
type TeamView struct {
	Team
	MemberCount  int `json:"memberCount"`
	OpenCount    int `json:"openCount"`
	OverdueCount int `json:"overdueCount"`
}
