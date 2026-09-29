package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Mongo document shapes, copied from the last Mongo-era tree
// (git show 2e438fc:server/store.go, server/recur.go). Only the bson tags
// matter here; these structs are decode targets and never leave this
// package. They are deliberately NOT shared with internal/model (whose
// Recurrence still carries transitional bson tags that may be removed).

type mTask struct {
	ID          bson.ObjectID    `bson:"_id"`
	Title       string           `bson:"title"`
	Notes       string           `bson:"notes,omitempty"`
	Horizon     string           `bson:"horizon"`
	Period      string           `bson:"period"`
	DueDate     string           `bson:"dueDate,omitempty"`
	Status      string           `bson:"status"`
	Priority    string           `bson:"priority"`
	ParentID    *bson.ObjectID   `bson:"parentId,omitempty"`
	TeamID      *bson.ObjectID   `bson:"teamId,omitempty"`
	AssigneeID  *bson.ObjectID   `bson:"assigneeId,omitempty"`
	WeekOf      string           `bson:"weekOf,omitempty"`
	OwnerID     *bson.ObjectID   `bson:"ownerId,omitempty"`
	Recurrence  *mRecurrence     `bson:"recurrence,omitempty"`
	SeriesID    *bson.ObjectID   `bson:"seriesId,omitempty"`
	CreatedAt   time.Time        `bson:"createdAt"`
	UpdatedAt   time.Time        `bson:"updatedAt"`
	CompletedAt *time.Time       `bson:"completedAt,omitempty"`
	Activity    []mActivityEntry `bson:"activity,omitempty"`
}

type mActivityEntry struct {
	ID       bson.ObjectID  `bson:"_id"`
	Kind     string         `bson:"kind"`
	Date     string         `bson:"date"`
	At       time.Time      `bson:"at"`
	By       *bson.ObjectID `bson:"by,omitempty"`
	ByName   string         `bson:"byName,omitempty"`
	Text     string         `bson:"text,omitempty"`
	From     string         `bson:"from,omitempty"`
	To       string         `bson:"to,omitempty"`
	EditedAt *time.Time     `bson:"editedAt,omitempty"`
}

type mRecurrence struct {
	Freq       string `bson:"freq"`
	Interval   *int   `bson:"interval,omitempty"`
	Anchor     string `bson:"anchor,omitempty"`
	Weekdays   []int  `bson:"weekdays,omitempty"`
	DayOfMonth int    `bson:"dayOfMonth,omitempty"`
}

type mTeam struct {
	ID        bson.ObjectID `bson:"_id"`
	Name      string        `bson:"name"`
	CreatedAt time.Time     `bson:"createdAt"`
}

type mMember struct {
	ID                 bson.ObjectID   `bson:"_id"`
	Name               string          `bson:"name"`
	Email              string          `bson:"email,omitempty"`
	Role               string          `bson:"role,omitempty"`
	TeamIDs            []bson.ObjectID `bson:"teamIds,omitempty"`
	CreatedAt          time.Time       `bson:"createdAt"`
	PasswordHash       string          `bson:"passwordHash,omitempty"`
	SystemRole         string          `bson:"systemRole,omitempty"`
	MustChangePassword bool            `bson:"mustChangePassword,omitempty"`
	Disabled           bool            `bson:"disabled,omitempty"`
	LastLoginAt        *time.Time      `bson:"lastLoginAt,omitempty"`
}

// Known field names per document level. A field outside these sets would
// be silently dropped by the struct decode — so it is reported instead.
var (
	knownTaskKeys = keySet("_id", "title", "notes", "horizon", "period", "dueDate", "status", "priority",
		"parentId", "teamId", "assigneeId", "weekOf", "ownerId", "recurrence", "seriesId",
		"createdAt", "updatedAt", "completedAt", "activity")
	knownActivityKeys   = keySet("_id", "kind", "date", "at", "by", "byName", "text", "from", "to", "editedAt")
	knownRecurrenceKeys = keySet("freq", "interval", "anchor", "weekdays", "dayOfMonth")
	knownTeamKeys       = keySet("_id", "name", "createdAt")
	knownMemberKeys     = keySet("_id", "name", "email", "role", "teamIds", "createdAt", "passwordHash",
		"systemRole", "mustChangePassword", "disabled", "lastLoginAt")
)

func keySet(ks ...string) map[string]bool {
	m := make(map[string]bool, len(ks))
	for _, k := range ks {
		m[k] = true
	}
	return m
}

// source is everything read from Mongo, in _id order.
type source struct {
	Tasks    []mTask
	Teams    []mTeam
	Members  []mMember
	Sessions int64 // counted only — sessions are not migrated

	// DecodeFindings are per-document problems found while reading
	// (decode errors = FATAL, unknown fields = WARN).
	DecodeFindings []finding
}

func connectMongo(ctx context.Context, uri string) (*mongo.Client, error) {
	c, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(10 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := c.Ping(ctx, nil); err != nil {
		_ = c.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	return c, nil
}

// readSource reads the four collections. It is read-only against Mongo.
func readSource(ctx context.Context, db *mongo.Database) (*source, error) {
	src := &source{}
	var err error
	if src.Teams, err = readColl[mTeam](ctx, db, "teams", src, func(raw bson.Raw) { src.unknownKeys("teams", raw, knownTeamKeys) }); err != nil {
		return nil, err
	}
	if src.Members, err = readColl[mMember](ctx, db, "members", src, func(raw bson.Raw) { src.unknownKeys("members", raw, knownMemberKeys) }); err != nil {
		return nil, err
	}
	if src.Tasks, err = readColl[mTask](ctx, db, "tasks", src, src.unknownTaskKeys); err != nil {
		return nil, err
	}
	if src.Sessions, err = db.Collection("sessions").CountDocuments(ctx, bson.D{}); err != nil {
		return nil, fmt.Errorf("count sessions: %w", err)
	}
	return src, nil
}

func readColl[T any](ctx context.Context, db *mongo.Database, coll string, src *source, inspect func(bson.Raw)) ([]T, error) {
	cur, err := db.Collection(coll).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("find %s: %w", coll, err)
	}
	defer cur.Close(ctx)
	var out []T
	for cur.Next(ctx) {
		raw := cur.Current
		var v T
		if err := bson.Unmarshal(raw, &v); err != nil {
			src.DecodeFindings = append(src.DecodeFindings, finding{Sev: sevFatal, Coll: coll, ID: rawID(raw),
				Msg: fmt.Sprintf("document does not decode into the %s shape: %v", coll, err)})
			continue
		}
		inspect(raw)
		out = append(out, v)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", coll, err)
	}
	return out, nil
}

func rawID(raw bson.Raw) string {
	v, err := raw.LookupErr("_id")
	if err != nil {
		return "<no _id>"
	}
	if oid, ok := v.ObjectIDOK(); ok {
		return oid.Hex()
	}
	return v.String()
}

func (s *source) unknownKeys(coll string, raw bson.Raw, known map[string]bool) {
	s.unknownKeysAt(coll, rawID(raw), "", raw, known)
}

func (s *source) unknownKeysAt(coll, id, prefix string, raw bson.Raw, known map[string]bool) {
	elems, err := raw.Elements()
	if err != nil {
		return
	}
	var unknown []string
	for _, e := range elems {
		if !known[e.Key()] {
			unknown = append(unknown, prefix+e.Key())
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		s.DecodeFindings = append(s.DecodeFindings, finding{Sev: sevWarn, Coll: coll, ID: id,
			Msg: fmt.Sprintf("unknown field(s) %v are NOT migrated (no column for them)", unknown)})
	}
}

func (s *source) unknownTaskKeys(raw bson.Raw) {
	id := rawID(raw)
	s.unknownKeysAt("tasks", id, "", raw, knownTaskKeys)
	if v, err := raw.LookupErr("recurrence"); err == nil {
		if doc, ok := v.DocumentOK(); ok {
			s.unknownKeysAt("tasks", id, "recurrence.", doc, knownRecurrenceKeys)
		}
	}
	if v, err := raw.LookupErr("activity"); err == nil {
		if arr, ok := v.ArrayOK(); ok {
			vals, _ := arr.Values()
			for i, av := range vals {
				if doc, ok := av.DocumentOK(); ok {
					s.unknownKeysAt("tasks", id, fmt.Sprintf("activity[%d].", i), doc, knownActivityKeys)
				}
			}
		}
	}
}
