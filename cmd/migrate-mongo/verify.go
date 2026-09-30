package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"
)

// verify re-reads both sides after commit and prints PASS/FAIL lines. The
// Mongo-side numbers come from fresh aggregations (not the in-memory
// snapshot), so the check is independent of the mapping code.
func verify(ctx context.Context, w io.Writer, mdb *mongo.Database, db *gorm.DB, p *plan) bool {
	ok := true
	check := func(pass bool, format string, args ...any) {
		tag := "PASS"
		if !pass {
			tag, ok = "FAIL", false
		}
		fmt.Fprintf(w, "  %s  %s\n", tag, fmt.Sprintf(format, args...))
	}

	pg := db.WithContext(ctx)
	counts, err := countTables(pg, dataTables)
	if err != nil {
		check(false, "read back row counts: %v", err)
		return false
	}
	want := map[string]int64{
		"teams": int64(len(p.Teams)), "members": int64(len(p.Members)), "member_teams": int64(len(p.MemberTeams)),
		"tasks": int64(len(p.Tasks)), "task_activity": int64(p.ActivityRows),
	}
	for _, t := range dataTables {
		check(counts[t] == want[t], "%-13s rows: planned %d, found %d", t, want[t], counts[t])
	}

	// total embedded activity, straight from Mongo
	var agg []struct {
		N int64 `bson:"n"`
	}
	cur, err := mdb.Collection("tasks").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: nil}, {Key: "n", Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$size", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$activity", bson.A{}}}}}}}}}}}},
	})
	if err == nil {
		err = cur.All(ctx, &agg)
	}
	if err != nil {
		check(false, "mongo activity aggregate: %v", err)
	} else {
		var mongoN int64
		if len(agg) == 1 {
			mongoN = agg[0].N
		}
		check(counts["task_activity"] == mongoN, "task_activity rows %d == sum of Mongo activity array lengths %d", counts["task_activity"], mongoN)
	}

	// per-status counts vs Mongo
	var mst []struct {
		Status string `bson:"_id"`
		N      int64  `bson:"n"`
	}
	cur, err = mdb.Collection("tasks").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$status"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
	})
	if err == nil {
		err = cur.All(ctx, &mst)
	}
	var pst []struct {
		Status string
		N      int64
	}
	if err == nil {
		err = pg.Raw(`SELECT status, count(*) AS n FROM tasks GROUP BY status`).Scan(&pst).Error
	}
	if err != nil {
		check(false, "per-status counts: %v", err)
	} else {
		mm, pm := map[string]int64{}, map[string]int64{}
		for _, r := range mst {
			mm[r.Status] = r.N
		}
		for _, r := range pst {
			pm[r.Status] = r.N
		}
		check(reflect.DeepEqual(mm, pm), "per-status task counts: mongo %v, postgres %v", sortedCounts(mm), sortedCounts(pm))
	}

	// field-by-field samples
	members := map[string]bool{}
	for _, m := range p.Members {
		members[m.ID] = true
	}
	for _, id := range pickSamples(p, 3) {
		diff, err := compareTask(ctx, mdb, pg, id, members)
		switch {
		case err != nil:
			check(false, "sample task %s: %v", id, err)
		case diff != "":
			check(false, "sample task %s differs: %s", id, diff)
		default:
			check(true, "sample task %s identical field-by-field (normalized JSON)", id)
		}
	}
	return ok
}

func sortedCounts(m map[string]int64) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	s := "{"
	for i, k := range ks {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:%d", k, m[k])
	}
	return s + "}"
}

// pickSamples chooses up to n tasks that exercise the most mapping paths
// (activity, recurrence, parent, then anything).
func pickSamples(p *plan, n int) []string {
	chosen := map[string]bool{}
	var out []string
	take := func(pred func(i int) bool) {
		best := -1
		for i := range p.Tasks {
			t := &p.Tasks[i]
			if chosen[t.ID] || !pred(i) {
				continue
			}
			if best < 0 || len(t.Activity) > len(p.Tasks[best].Activity) {
				best = i
			}
		}
		if best >= 0 && len(out) < n {
			chosen[p.Tasks[best].ID] = true
			out = append(out, p.Tasks[best].ID)
		}
	}
	take(func(i int) bool { return len(p.Tasks[i].Activity) > 0 })
	take(func(i int) bool { return p.Tasks[i].Recurrence != nil })
	take(func(i int) bool { return p.Tasks[i].ParentID != nil })
	for len(out) < n {
		before := len(out)
		take(func(int) bool { return true })
		if len(out) == before {
			break
		}
	}
	return out
}

// compareTask normalizes the raw Mongo document and the app-shaped PG read
// (model.Task JSON, whose tags equal the Mongo field names except _id→id)
// into maps and diffs them. Unknown Mongo fields are excluded (they are
// reported as WARN findings already); empty strings, nulls and empty arrays
// are pruned on both sides (empty-vs-NULL is asserted by the SQL tests). The
// one reported transformation — activity.by pointing at a missing member is
// written as NULL — is applied to the Mongo side before comparing.
func compareTask(ctx context.Context, mdb *mongo.Database, pg *gorm.DB, id string, members map[string]bool) (string, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return "", err
	}
	raw, err := mdb.Collection("tasks").FindOne(ctx, bson.D{{Key: "_id", Value: oid}}).Raw()
	if err != nil {
		return "", fmt.Errorf("mongo read: %w", err)
	}
	mongoDoc := normDoc(raw, knownTaskKeys, "")
	if acts, ok := mongoDoc["activity"].([]any); ok {
		for _, a := range acts {
			if e, ok := a.(map[string]any); ok {
				if by, ok := e["by"].(string); ok && !members[by] {
					delete(e, "by")
				}
			}
		}
	}
	mongoSide := prune(mongoDoc).(map[string]any)

	t, err := loadTask(pg, id)
	if err != nil {
		return "", fmt.Errorf("postgres read: %w", err)
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	var pgMap any
	if err := json.Unmarshal(b, &pgMap); err != nil {
		return "", err
	}
	pgSide := prune(pgMap).(map[string]any)
	return firstDiff("", mongoSide, pgSide), nil
}

func normDoc(raw bson.Raw, known map[string]bool, ctxKey string) map[string]any {
	out := map[string]any{}
	elems, _ := raw.Elements()
	for _, e := range elems {
		k := e.Key()
		if !known[k] {
			continue
		}
		name := k
		if k == "_id" {
			name = "id"
		}
		out[name] = normValue(e.Value(), k)
	}
	return out
}

func normValue(v bson.RawValue, key string) any {
	switch v.Type {
	case bson.TypeObjectID:
		return v.ObjectID().Hex()
	case bson.TypeDateTime:
		return v.Time().UTC().Format(time.RFC3339Nano)
	case bson.TypeString:
		return v.StringValue()
	case bson.TypeInt32:
		return float64(v.Int32())
	case bson.TypeInt64:
		return float64(v.Int64())
	case bson.TypeDouble:
		return v.Double()
	case bson.TypeBoolean:
		return v.Boolean()
	case bson.TypeNull, bson.TypeUndefined:
		return nil
	case bson.TypeEmbeddedDocument:
		known := knownRecurrenceKeys
		if key == "activity" {
			known = knownActivityKeys
		}
		return normDoc(v.Document(), known, key)
	case bson.TypeArray:
		vals, _ := v.Array().Values()
		arr := make([]any, 0, len(vals))
		for _, av := range vals {
			arr = append(arr, normValue(av, key))
		}
		return arr
	default:
		return v.String()
	}
}

func prune(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			pv := prune(val)
			if isEmpty(pv) {
				continue
			}
			out[k] = pv
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, prune(e))
		}
		return out
	}
	return v
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func firstDiff(path string, a, b any) string {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		ks := make([]string, 0, len(keys))
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			if d := firstDiff(path+"."+k, am[k], bm[k]); d != "" {
				return d
			}
		}
		return ""
	}
	aa, aok := a.([]any)
	ba, bok := b.([]any)
	if aok && bok {
		if len(aa) != len(ba) {
			return fmt.Sprintf("%s: length mongo %d vs postgres %d", path, len(aa), len(ba))
		}
		for i := range aa {
			if d := firstDiff(fmt.Sprintf("%s[%d]", path, i), aa[i], ba[i]); d != "" {
				return d
			}
		}
		return ""
	}
	if !reflect.DeepEqual(a, b) {
		return fmt.Sprintf("%s: mongo %#v vs postgres %#v", path, a, b)
	}
	return ""
}
