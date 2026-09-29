package repo

import (
	"context"
	"reflect"
	"testing"
	"time"

	"taskman/internal/model"
)

func TestTeams_CRUDAndConflicts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.CreateTeam(ctx, "  ")
	mtWantAPIErr(t, err, 400, "name is required")
	beta := mtTeam(t, s, "beta")
	alpha := mtTeam(t, s, "Alpha")
	zeta := mtTeam(t, s, "Zeta")
	_, err = s.CreateTeam(ctx, "beta")
	mtWantAPIErr(t, err, 409, "team name already exists")

	// Binary (COLLATE "C") order: uppercase before lowercase, like Mongo.
	teams, err := s.ListTeams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{teams[0].Name, teams[1].Name, teams[2].Name}; !reflect.DeepEqual(got, []string{"Alpha", "Zeta", "beta"}) {
		t.Fatalf("order = %v", got)
	}

	_, err = s.PatchTeam(ctx, beta.ID, "Alpha")
	mtWantAPIErr(t, err, 409, "team name already exists")
	_, err = s.PatchTeam(ctx, "ghost", "x")
	mtWantAPIErr(t, err, 404, "team not found")
	_, err = s.PatchTeam(ctx, beta.ID, "")
	mtWantAPIErr(t, err, 400, "name is required")
	got, err := s.PatchTeam(ctx, beta.ID, "Beta2")
	if err != nil || got.Name != "Beta2" || got.ID != beta.ID {
		t.Fatalf("PatchTeam = %+v, %v", got, err)
	}

	// USER scope: own teams only; no teams → empty, not an error.
	userCtx := asUser(&model.CtxUser{ID: "u", SystemRole: model.RoleUser, TeamIDs: []string{zeta.ID}})
	teams, err = s.ListTeams(userCtx)
	if err != nil || len(teams) != 1 || teams[0].ID != zeta.ID {
		t.Fatalf("USER ListTeams = %+v, %v", teams, err)
	}
	teams, err = s.ListTeams(asUser(&model.CtxUser{ID: "u", SystemRole: model.RoleUser}))
	if err != nil || len(teams) != 0 {
		t.Fatalf("USER-without-teams ListTeams = %+v, %v", teams, err)
	}
	teams, _ = s.ListTeams(asUser(&model.CtxUser{ID: "a", SystemRole: model.RoleAdmin}))
	if len(teams) != 3 {
		t.Fatalf("ADMIN sees %d teams", len(teams))
	}
	_ = alpha
}

func TestDeleteTeam(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	busy := mtTeam(t, s, "busy")
	idle := mtTeam(t, s, "idle")
	mtTask(t, s, &busy.ID, nil, model.StatusDone, "")
	m := mtMember(t, s, "Ann", "", busy.ID, idle.ID)

	mtWantAPIErr(t, s.DeleteTeam(ctx, busy.ID), 409, "team has tasks")
	mtWantAPIErr(t, s.DeleteTeam(ctx, "ghost"), 404, "team not found")

	// The RESTRICT FK is named as DeleteTeam's race fallback expects.
	err := s.db.Exec(`DELETE FROM teams WHERE id = ?`, busy.ID).Error
	if c := violatedConstraint(err, sqlstateFKViolation); c != "tasks_team_id_fkey" {
		t.Fatalf("raw delete err = %v (constraint %q)", err, c)
	}

	if err := s.DeleteTeam(ctx, idle.ID); err != nil {
		t.Fatal(err)
	}
	// member_teams cascaded — no dangling teamIds (decision #3 fix).
	members, _ := s.ListMembers(ctx, nil)
	if len(members) != 1 || len(members[0].TeamIDs) != 1 || members[0].TeamIDs[0] != busy.ID {
		t.Fatalf("member teams after delete = %+v", members)
	}
	_ = m
}

func TestListTeamsWithCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mtTeam(t, s, "a")
	b := mtTeam(t, s, "b")
	mtMember(t, s, "m1", "", a.ID)
	mtMember(t, s, "m2", "", a.ID, b.ID)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")
	mtTask(t, s, &a.ID, nil, model.StatusTodo, yesterday)      // open, overdue
	mtTask(t, s, &a.ID, nil, model.StatusInProgress, today)    // open, not overdue
	mtTask(t, s, &a.ID, nil, model.StatusTodo, "")             // open, no due date
	mtTask(t, s, &a.ID, nil, model.StatusDone, yesterday)      // closed
	mtTask(t, s, &b.ID, nil, model.StatusCancelled, yesterday) // closed
	mtTask(t, s, nil, nil, model.StatusTodo, yesterday)        // personal: no team

	views, err := s.ListTeamsWithCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.TeamView{
		{Team: *a, MemberCount: 2, OpenCount: 3, OverdueCount: 1},
		{Team: *b, MemberCount: 1, OpenCount: 0, OverdueCount: 0},
	}
	if len(views) != 2 {
		t.Fatalf("views = %+v", views)
	}
	for i := range want {
		g, w := views[i], want[i]
		if g.ID != w.ID || g.Name != w.Name || g.MemberCount != w.MemberCount || g.OpenCount != w.OpenCount || g.OverdueCount != w.OverdueCount {
			t.Fatalf("view[%d] = %+v, want %+v", i, g, w)
		}
	}
	userViews, _ := s.ListTeamsWithCounts(asUser(&model.CtxUser{SystemRole: model.RoleUser, TeamIDs: []string{b.ID}}))
	if len(userViews) != 1 || userViews[0].ID != b.ID {
		t.Fatalf("USER views = %+v", userViews)
	}
}
