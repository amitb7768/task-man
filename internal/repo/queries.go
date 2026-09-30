package repo

import "gorm.io/gorm"

// Pure query helpers: one read, no business rules, no business errors —
// safe to call on the pool or inside a caller's transaction.

// ---- member_teams join (replaces Member.teamIds) ----

// memberTeamOrder is the read order of a member's team ids: the pos column
// preserves Mongo's client-written teamIds array order (writeMemberTeams
// assigns it; ctid was rejected at review — physical order isn't a
// contract).
const memberTeamOrder = "pos"

// LoadMemberTeamIDs loads team ids for many members in one query (no N+1).
// Members with no rows are absent from the map (TeamIDs nil — same as
// Mongo's omitempty-absent teamIds).
func LoadMemberTeamIDs(db *gorm.DB, memberIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(memberIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		MemberID string
		TeamID   string
	}
	err := db.Raw(`SELECT member_id, team_id FROM member_teams WHERE member_id IN ? ORDER BY `+memberTeamOrder, memberIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.MemberID] = append(out[r.MemberID], r.TeamID)
	}
	return out, nil
}

// LookupNames fetches id -> name from a table with id/name columns (teams,
// members) in one query.
func LookupNames(db *gorm.DB, table string, ids map[string]bool) (map[string]string, error) {
	names := map[string]string{}
	if len(ids) == 0 {
		return names, nil
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var rows []struct{ ID, Name string }
	if err := db.Table(table).Select("id, name").Where("id IN ?", list).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	return names, nil
}
