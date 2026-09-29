package main

import (
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Pure validation tests: no databases needed.

func findingsText(p *plan) string {
	var sb strings.Builder
	for _, f := range p.Findings {
		sb.WriteString(f.Sev.String() + " " + f.Coll + " " + f.ID + " " + f.Msg + "\n")
	}
	return sb.String()
}

func TestBuildPlanValidations(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	team := mTeam{ID: bson.NewObjectID(), Name: "A", CreatedAt: now}
	dupTeam := mTeam{ID: bson.NewObjectID(), Name: "A", CreatedAt: now}
	m := mMember{ID: bson.NewObjectID(), Name: "m", CreatedAt: now, SystemRole: "ROOT"}
	series := bson.NewObjectID()
	a, b := bson.NewObjectID(), bson.NewObjectID()
	actID := bson.NewObjectID()
	tasks := []mTask{
		// a <-> b parent cycle
		{ID: a, ParentID: &b, Status: "todo", Horizon: "daily", CreatedAt: now, UpdatedAt: now, OwnerID: &m.ID},
		{ID: b, ParentID: &a, Status: "todo", Horizon: "daily", CreatedAt: now, UpdatedAt: now, OwnerID: &m.ID},
		{ID: bson.NewObjectID(), Status: "todo", Horizon: "yearly", Priority: "urgent", SeriesID: &series, Period: "2026-09-01", CreatedAt: now, UpdatedAt: now,
			Activity: []mActivityEntry{{ID: actID, Kind: "comment", At: now}}},
		{ID: bson.NewObjectID(), Status: "todo", Horizon: "daily", SeriesID: &series, Period: "2026-09-01", CreatedAt: now, UpdatedAt: now,
			TeamID: &team.ID, OwnerID: &m.ID, // both set: INFO only
			Activity: []mActivityEntry{{ID: actID, Kind: "note", At: now}}},
		{ID: bson.NewObjectID(), Status: "todo", Horizon: "daily"}, // missing timestamps, neither owner nor team
	}
	p := buildPlan(&source{Teams: []mTeam{team, dupTeam}, Members: []mMember{m}, Tasks: tasks})
	txt := findingsText(p)
	for _, want := range []string{
		"FATAL teams " + dupTeam.ID.Hex() + ` name "A" duplicates team`,
		`systemRole "ROOT"`,
		"parentId cycle",
		`horizon "yearly"`,
		`priority "urgent"`,
		`activity[0].kind "comment"`,
		"duplicates an entry on task",
		"tasks_series_period_unique",
		"createdAt missing",
		"updatedAt missing",
		"INFO tasks  personal/team invariant (ownerId XOR teamId) violated by 3 task(s): 1 with both set, 2 with neither",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("findings missing %q\n%s", want, txt)
		}
	}
	if strings.Count(txt, "parentId cycle") != 1 {
		t.Errorf("cycle should be reported once:\n%s", txt)
	}
	if p.Findings[0].Sev != sevFatal || p.Findings[len(p.Findings)-1].Sev != sevInfo {
		t.Errorf("findings not grouped by severity:\n%s", txt)
	}
}

func TestTopoOrderParentsFirst(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	g, c, par := bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID() // g < c < par in _id order
	owner := bson.NewObjectID()
	mk := func(id bson.ObjectID, parent *bson.ObjectID) mTask {
		return mTask{ID: id, ParentID: parent, Status: "todo", Horizon: "daily", CreatedAt: now, UpdatedAt: now, OwnerID: &owner}
	}
	p := buildPlan(&source{
		Members: []mMember{{ID: owner, Name: "o", CreatedAt: now}},
		Tasks:   []mTask{mk(g, &c), mk(c, &par), mk(par, nil)},
	})
	if n := p.fatalCount(); n != 0 {
		t.Fatalf("unexpected fatals:\n%s", findingsText(p))
	}
	var got []string
	for _, tk := range p.Tasks {
		got = append(got, tk.ID)
	}
	want := []string{par.Hex(), c.Hex(), g.Hex()}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want parents first %v", got, want)
	}
}

func TestMemberTeamsKeepFirstPosition(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	t1, t2 := bson.NewObjectID(), bson.NewObjectID()
	m := mMember{ID: bson.NewObjectID(), Name: "m", CreatedAt: now, TeamIDs: []bson.ObjectID{t2, t1, t2}}
	p := buildPlan(&source{Teams: []mTeam{{ID: t1, Name: "1", CreatedAt: now}, {ID: t2, Name: "2", CreatedAt: now}}, Members: []mMember{m}})
	if len(p.MemberTeams) != 2 || p.MemberTeams[0] != (memberTeamRow{m.ID.Hex(), t2.Hex(), 0}) || p.MemberTeams[1] != (memberTeamRow{m.ID.Hex(), t1.Hex(), 1}) {
		t.Fatalf("member_teams = %+v", p.MemberTeams)
	}
}
