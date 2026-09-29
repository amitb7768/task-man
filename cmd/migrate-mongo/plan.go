package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"taskman/internal/model"
)

type severity int

const (
	sevFatal severity = iota // refuses --apply
	sevWarn                  // migrated with a documented transformation
	sevInfo                  // statistics only
)

func (s severity) String() string {
	switch s {
	case sevFatal:
		return "FATAL"
	case sevWarn:
		return "WARN"
	default:
		return "INFO"
	}
}

type finding struct {
	Sev  severity
	Coll string
	ID   string
	Msg  string
}

// memberTeamRow is one member_teams row.
type memberTeamRow struct {
	MemberID string
	TeamID   string
	Pos      int
}

// plan is the full, validated set of rows to write, in insert order.
type plan struct {
	Teams       []model.Team
	Members     []model.Member // TeamIDs left nil; see MemberTeams
	MemberTeams []memberTeamRow
	Tasks       []model.Task // topologically ordered, parents first; Activity populated

	ActivityRows     int            // planned task_activity rows
	EmbeddedActivity int            // sum of Mongo activity array lengths
	StatusCounts     map[string]int // per-status task counts as read from Mongo

	Findings []finding
}

func (p *plan) add(sev severity, coll, id, format string, args ...any) {
	p.Findings = append(p.Findings, finding{Sev: sev, Coll: coll, ID: id, Msg: fmt.Sprintf(format, args...)})
}

func (p *plan) fatalCount() int {
	n := 0
	for _, f := range p.Findings {
		if f.Sev == sevFatal {
			n++
		}
	}
	return n
}

// Allowed values: must match the CHECK constraints in
// internal/repo/migrations/0001_init.up.sql.
var (
	allowedStatus     = keySet("todo", "in_progress", "done", "cancelled")
	allowedHorizon    = keySet("daily", "weekly", "monthly", "backlog")
	allowedPriority   = keySet("", "low", "medium", "high")
	allowedKind       = keySet("note", "status")
	allowedSystemRole = keySet("", "ADMIN", "USER")
)

func hexPtr(id *bson.ObjectID) *string {
	if id == nil {
		return nil
	}
	s := id.Hex()
	return &s
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// buildPlan maps the Mongo snapshot onto target rows and runs every
// validation. It is pure (no I/O) so it is unit-testable without databases.
func buildPlan(src *source) *plan {
	p := &plan{StatusCounts: map[string]int{}}
	p.Findings = append(p.Findings, src.DecodeFindings...)

	// ---- teams ----
	teamIDs := map[string]bool{}
	teamNames := map[string]string{}
	for _, t := range src.Teams {
		id := t.ID.Hex()
		if !checkID(p, "teams", id, t.ID, teamIDs) {
			continue
		}
		if prev, ok := teamNames[t.Name]; ok {
			p.add(sevFatal, "teams", id, "name %q duplicates team %s (teams_name_unique)", t.Name, prev)
		}
		teamNames[t.Name] = id
		if t.CreatedAt.IsZero() {
			p.add(sevFatal, "teams", id, "createdAt missing (created_at is NOT NULL)")
		}
		p.Teams = append(p.Teams, model.Team{ID: id, Name: t.Name, CreatedAt: t.CreatedAt.UTC()})
	}

	// ---- members ----
	memberIDs := map[string]bool{}
	loginEmails := map[string]string{} // lower(email) -> member id, login-enabled only
	for _, m := range src.Members {
		id := m.ID.Hex()
		if !checkID(p, "members", id, m.ID, memberIDs) {
			continue
		}
		if !allowedSystemRole[m.SystemRole] {
			p.add(sevFatal, "members", id, "systemRole %q not in {'', ADMIN, USER}", m.SystemRole)
		}
		if m.CreatedAt.IsZero() {
			p.add(sevFatal, "members", id, "createdAt missing (created_at is NOT NULL)")
		}
		// Login-enabled in PG terms = password_hash IS NOT NULL, and '' maps
		// to NULL, so only a non-empty hash counts. NULL emails never collide.
		if m.PasswordHash != "" && m.Email != "" {
			k := strings.ToLower(m.Email)
			if prev, ok := loginEmails[k]; ok {
				p.add(sevFatal, "members", id, "login email %q collides case-insensitively with member %s (members_email_login_unique)", m.Email, prev)
			} else {
				loginEmails[k] = id
			}
		}
		p.Members = append(p.Members, model.Member{
			ID:                 id,
			Name:               m.Name,
			Email:              model.NullStr(m.Email),
			Role:               m.Role,
			CreatedAt:          m.CreatedAt.UTC(),
			PasswordHash:       model.NullStr(m.PasswordHash),
			SystemRole:         m.SystemRole,
			MustChangePassword: m.MustChangePassword,
			Disabled:           m.Disabled,
			LastLoginAt:        utcPtr(m.LastLoginAt),
		})
	}
	// teamIds -> member_teams, pos = array index, duplicates keep their
	// first position (writeMemberTeams' ON CONFLICT DO NOTHING semantics).
	for _, m := range src.Members {
		id := m.ID.Hex()
		seen := map[string]bool{}
		for i, tid := range m.TeamIDs {
			th := tid.Hex()
			if !teamIDs[th] {
				p.add(sevFatal, "members", id, "teamIds[%d] = %s → no such team", i, th)
				continue
			}
			if seen[th] {
				p.add(sevInfo, "members", id, "teamIds[%d] = %s duplicates an earlier entry; kept first position", i, th)
				continue
			}
			seen[th] = true
			p.MemberTeams = append(p.MemberTeams, memberTeamRow{MemberID: id, TeamID: th, Pos: i})
		}
	}

	// ---- tasks ----
	taskIDs := map[string]bool{}
	byID := map[string]*model.Task{}
	var order []string
	activityIDs := map[string]string{} // activity id -> task id (task_activity.id is a global PK)
	seriesPeriods := map[[2]string]string{}
	var bothSet, neitherSet, teamNoWeek, personalWithWeek int
	for _, mt := range src.Tasks {
		id := mt.ID.Hex()
		if !checkID(p, "tasks", id, mt.ID, taskIDs) {
			continue
		}
		p.StatusCounts[mt.Status]++
		p.EmbeddedActivity += len(mt.Activity)
		if !allowedStatus[mt.Status] {
			p.add(sevFatal, "tasks", id, "status %q not in {todo, in_progress, done, cancelled}", mt.Status)
		}
		if !allowedHorizon[mt.Horizon] {
			p.add(sevFatal, "tasks", id, "horizon %q not in {daily, weekly, monthly, backlog}", mt.Horizon)
		}
		if !allowedPriority[mt.Priority] {
			p.add(sevFatal, "tasks", id, "priority %q not in {'', low, medium, high}", mt.Priority)
		}
		if mt.CreatedAt.IsZero() {
			p.add(sevFatal, "tasks", id, "createdAt missing (created_at is NOT NULL)")
		}
		if mt.UpdatedAt.IsZero() {
			p.add(sevFatal, "tasks", id, "updatedAt missing (updated_at is NOT NULL)")
		}
		if mt.TeamID != nil && !teamIDs[mt.TeamID.Hex()] {
			p.add(sevFatal, "tasks", id, "teamId %s → no such team", mt.TeamID.Hex())
		}
		if mt.AssigneeID != nil && !memberIDs[mt.AssigneeID.Hex()] {
			p.add(sevFatal, "tasks", id, "assigneeId %s → no such member", mt.AssigneeID.Hex())
		}
		if mt.OwnerID != nil && !memberIDs[mt.OwnerID.Hex()] {
			p.add(sevFatal, "tasks", id, "ownerId %s → no such member", mt.OwnerID.Hex())
		}
		// parentId existence is checked after all tasks are indexed.
		if mt.SeriesID != nil {
			k := [2]string{mt.SeriesID.Hex(), mt.Period}
			if prev, ok := seriesPeriods[k]; ok {
				p.add(sevFatal, "tasks", id, "(seriesId %s, period %q) duplicates task %s (tasks_series_period_unique)", k[0], k[1], prev)
			} else {
				seriesPeriods[k] = id
			}
		}
		switch {
		case mt.OwnerID != nil && mt.TeamID != nil:
			bothSet++
		case mt.OwnerID == nil && mt.TeamID == nil:
			neitherSet++
		}
		if mt.TeamID != nil && mt.WeekOf == "" {
			teamNoWeek++
		}
		if mt.TeamID == nil && mt.WeekOf != "" {
			personalWithWeek++
		}

		t := model.Task{
			ID:          id,
			Title:       mt.Title,
			Notes:       model.NullStr(mt.Notes),
			Horizon:     mt.Horizon,
			Period:      mt.Period,
			DueDate:     model.NullStr(mt.DueDate),
			Status:      mt.Status,
			Priority:    mt.Priority,
			ParentID:    hexPtr(mt.ParentID),
			TeamID:      hexPtr(mt.TeamID),
			AssigneeID:  hexPtr(mt.AssigneeID),
			WeekOf:      model.NullStr(mt.WeekOf),
			OwnerID:     hexPtr(mt.OwnerID),
			SeriesID:    hexPtr(mt.SeriesID),
			CreatedAt:   mt.CreatedAt.UTC(),
			UpdatedAt:   mt.UpdatedAt.UTC(),
			CompletedAt: utcPtr(mt.CompletedAt),
		}
		if r := mt.Recurrence; r != nil {
			t.Recurrence = &model.Recurrence{Freq: r.Freq, Interval: r.Interval, Anchor: r.Anchor, Weekdays: r.Weekdays, DayOfMonth: r.DayOfMonth}
			if err := model.ValidateRecurrence(t.Recurrence); err != nil {
				p.add(sevWarn, "tasks", id, "recurrence fails ValidateRecurrence (%v); carried verbatim", err)
			}
		}
		for i, ma := range mt.Activity {
			aid := ma.ID.Hex()
			switch {
			case ma.ID.IsZero():
				p.add(sevFatal, "tasks", id, "activity[%d] has no _id", i)
			case activityIDs[aid] != "":
				p.add(sevFatal, "tasks", id, "activity[%d]._id %s duplicates an entry on task %s (task_activity.id is a global PK)", i, aid, activityIDs[aid])
			default:
				activityIDs[aid] = id
			}
			if !allowedKind[ma.Kind] {
				p.add(sevFatal, "tasks", id, "activity[%d].kind %q not in {note, status}", i, ma.Kind)
			}
			if ma.At.IsZero() {
				p.add(sevFatal, "tasks", id, "activity[%d].at missing (at is NOT NULL)", i)
			}
			e := model.ActivityEntry{
				ID: aid, TaskID: id, Kind: ma.Kind, Date: ma.Date, At: ma.At.UTC(),
				By: hexPtr(ma.By), ByName: ma.ByName, Text: ma.Text, From: ma.From, To: ma.To,
				EditedAt: utcPtr(ma.EditedAt),
			}
			if ma.By != nil && !memberIDs[ma.By.Hex()] {
				p.add(sevWarn, "tasks", id, "activity[%d].by %s → no such member; by_id written as NULL (byName %q kept)", i, ma.By.Hex(), ma.ByName)
				e.By = nil
			}
			t.Activity = append(t.Activity, e)
		}
		p.ActivityRows += len(t.Activity)
		byID[id] = &t
		order = append(order, id)
	}
	for _, id := range order {
		t := byID[id]
		if t.ParentID != nil && byID[*t.ParentID] == nil {
			p.add(sevFatal, "tasks", id, "parentId %s → no such task", *t.ParentID)
		}
	}
	p.Tasks = topoOrder(p, order, byID)

	if bothSet+neitherSet > 0 {
		p.add(sevInfo, "tasks", "", "personal/team invariant (ownerId XOR teamId) violated by %d task(s): %d with both set, %d with neither — migrated as-is (app tolerates legacy rows)", bothSet+neitherSet, bothSet, neitherSet)
	}
	if teamNoWeek+personalWithWeek > 0 {
		p.add(sevInfo, "tasks", "", "weekOf-iff-teamId violated by %d task(s): %d team without weekOf, %d personal with weekOf — migrated as-is", teamNoWeek+personalWithWeek, teamNoWeek, personalWithWeek)
	}

	sort.SliceStable(p.Findings, func(i, j int) bool {
		a, b := p.Findings[i], p.Findings[j]
		if a.Sev != b.Sev {
			return a.Sev < b.Sev
		}
		if a.Coll != b.Coll {
			return a.Coll < b.Coll
		}
		return a.ID < b.ID
	})
	return p
}

// checkID rejects zero and duplicate _ids (Mongo enforces _id uniqueness,
// so a duplicate here means the snapshot is corrupt — paranoia).
func checkID(p *plan, coll, id string, oid bson.ObjectID, seen map[string]bool) bool {
	if oid.IsZero() {
		p.add(sevFatal, coll, id, "_id is missing or the zero ObjectID")
		return false
	}
	if seen[id] {
		p.add(sevFatal, coll, id, "duplicate _id within collection")
		return false
	}
	seen[id] = true
	return true
}

// topoOrder returns tasks parents-first (stable: _id order otherwise). A
// parent cycle is a FATAL finding; its members are still emitted (the plan
// is refused anyway) so row counts stay truthful.
func topoOrder(p *plan, order []string, byID map[string]*model.Task) []model.Task {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	out := make([]model.Task, 0, len(order))
	var visit func(id string, path []string)
	visit = func(id string, path []string) {
		switch state[id] {
		case done:
			return
		case visiting:
			// cycle: path from the first occurrence of id
			start := 0
			for i, x := range path {
				if x == id {
					start = i
					break
				}
			}
			cyc := append(append([]string{}, path[start:]...), id)
			p.add(sevFatal, "tasks", id, "parentId cycle: %s", strings.Join(cyc, " → "))
			return
		}
		state[id] = visiting
		t := byID[id]
		if t.ParentID != nil && byID[*t.ParentID] != nil {
			visit(*t.ParentID, append(path, id))
		}
		if state[id] == visiting {
			state[id] = done
			out = append(out, *t)
		}
	}
	for _, id := range order {
		visit(id, nil)
	}
	return out
}
