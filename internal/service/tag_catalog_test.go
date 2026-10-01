package service

import (
	"testing"

	"taskman/internal/model"
)

// v11 tag catalog (docs/DESIGN_V11_TAG_CATALOG.md).

func TestTagCatalogEnforcedOnTaskWrites(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "known", "other")

	// Create: unknown rejected for everyone (ADMIN included), after
	// normalisation; catalog tags accepted.
	_, err := e.s.CreateTask(e.asAlice, &model.Task{Title: "x", Horizon: model.HorizonDaily, Period: tkToday(), Tags: []string{"known", "Nope"}})
	tkWantErr(t, err, 400, `unknown tag "nope"`)
	_, err = e.s.CreateTask(e.asAdmin, &model.Task{Title: "x", Horizon: model.HorizonDaily, Period: tkToday(), Tags: []string{"nope"}})
	tkWantErr(t, err, 400, `unknown tag "nope"`)
	var n int64
	e.s.db.Raw("SELECT count(*) FROM tasks").Scan(&n)
	if n != 0 {
		t.Fatalf("rejected creates wrote %d rows", n)
	}
	v := tkCreate(t, e.asAlice, e.s, "ok", tgTags(" Known "))
	tgWantTags(t, "create", v.Tags, "known")

	// Patch: unknown → 400, row unchanged; catalog → ok.
	_, err = e.s.PatchTask(e.asAlice, v.ID, []byte(`{"tags":["other","ghost"]}`))
	tkWantErr(t, err, 400, `unknown tag "ghost"`)
	tgWantTags(t, "after rejected patch", tgStored(t, e, v.ID), "known")
	p := tkPatch(t, e.asAlice, e.s, v.ID, `{"tags":["OTHER","known"]}`)
	tgWantTags(t, "patch", p.Tags, "other", "known")
	// A non-tag patch on a tagged task still validates fine.
	tkPatch(t, e.asAlice, e.s, v.ID, `{"title":"renamed"}`)

	// Restore: unknown → 400 and nothing written; catalog → ok.
	now := v.CreatedAt
	bad := model.Task{ID: NewID(), Title: "r", Horizon: model.HorizonDaily, Period: tkToday(),
		Status: model.StatusTodo, OwnerID: tkStr(e.alice.ID), CreatedAt: now, UpdatedAt: now, Tags: model.Tags{"Ghost"}}
	_, err = e.s.RestoreTasks(e.asAdmin, []model.Task{bad})
	tkWantErr(t, err, 400, `unknown tag "ghost"`)
	e.s.db.Raw("SELECT count(*) FROM tasks WHERE id = ?", bad.ID).Scan(&n)
	if n != 0 {
		t.Fatal("rejected restore must write nothing")
	}
	bad.Tags = model.Tags{"Known"}
	if got, err := e.s.RestoreTasks(e.asAdmin, []model.Task{bad}); err != nil || got != 1 {
		t.Fatalf("restore with catalog tag: n=%d err=%v", got, err)
	}
	tgWantTags(t, "restored", tgStored(t, e, bad.ID), "known")
}

func TestTagCatalogCreate(t *testing.T) {
	e := tkSetup(t)

	tag, err := e.s.CreateTag(e.asAdmin, "  Backend ")
	if err != nil {
		t.Fatal(err)
	}
	if tag.Name != "backend" || tag.CreatedBy == nil || *tag.CreatedBy != e.admin.ID || tag.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", tag)
	}
	_, err = e.s.CreateTag(e.asAdmin, "BACKEND")
	tkWantErr(t, err, 409, `tag "backend" already exists`)
	_, err = e.s.CreateTag(e.asAdmin, "a b")
	tkWantErr(t, err, 400, `invalid tag "a b"`)
	_, err = e.s.CreateTag(e.asAdmin, "#x")
	tkWantErr(t, err, 400, `invalid tag "#x"`)
	_, err = e.s.CreateTag(e.asAdmin, "   ")
	tkWantErr(t, err, 400, "name is required")
	_, err = e.s.CreateTag(e.asAdmin, "abcdefghijabcdefghijabcdefghijk")
	tkWantErr(t, err, 400, `tag "abcdefghijabcdefghijabcdefghijk" is longer than 30 characters`)
	_, err = e.s.CreateTag(e.asAlice, "userland")
	tkWantErr(t, err, 403, "admin only")

	if _, err := e.s.CreateTag(e.asAdmin, "api"); err != nil {
		t.Fatal(err)
	}
	cat, err := e.s.ListCatalog(e.asAlice)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat) != 2 || cat[0].Name != "api" || cat[1].Name != "backend" {
		t.Fatalf("catalog = %+v", cat)
	}

	// The new tag is immediately usable on tasks and listed with count 0.
	counts, err := e.s.ListTags(e.asAlice, nil, false)
	if err != nil || len(counts) != 2 || counts[0] != (model.TagCount{Tag: "api"}) {
		t.Fatalf("ListTags = %+v err=%v", counts, err)
	}
	tkCreate(t, e.asAlice, e.s, "uses api", tgTags("api"))
}

func TestTagCatalogDelete(t *testing.T) {
	e := tkSetup(t)
	tkTags(t, e.s, "used", "free", "closedonly")
	tkCreate(t, e.asAlice, e.s, "a", tgTags("used"))
	tkCreate(t, e.asBob, e.s, "b", tgTags("used"))
	c := tkCreate(t, e.asAlice, e.s, "c", tgTags("closedonly")).ID
	tkPatch(t, e.asAlice, e.s, c, `{"status":"done"}`)

	tkWantErr(t, e.s.DeleteTag(e.asAlice, "free"), 403, "admin only")
	// In use counts every task (any owner, any status) — not the caller's scope.
	tkWantErr(t, e.s.DeleteTag(e.asAdmin, "used"), 409, `tag "used" is in use by 2 tasks`)
	tkWantErr(t, e.s.DeleteTag(e.asAdmin, "closedonly"), 409, `tag "closedonly" is in use by 1 tasks`)
	tkWantErr(t, e.s.DeleteTag(e.asAdmin, "ghost"), 404, "tag not found")
	if err := e.s.DeleteTag(e.asAdmin, "free"); err != nil {
		t.Fatal(err)
	}
	tkWantErr(t, e.s.DeleteTag(e.asAdmin, "free"), 404, "tag not found")
	cat, _ := e.s.ListCatalog(e.asAdmin)
	if len(cat) != 2 || cat[0].Name != "closedonly" || cat[1].Name != "used" {
		t.Fatalf("catalog after delete = %+v", cat)
	}
	_, err := e.s.CreateTask(e.asAlice, &model.Task{Title: "x", Horizon: model.HorizonDaily, Period: tkToday(), Tags: []string{"free"}})
	tkWantErr(t, err, 400, `unknown tag "free"`)
}
