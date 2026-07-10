package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// testMongoURI points at the same local Mongo instance the dev server uses
// (docker-compose's taskman-mongo-1). Tests never touch the "taskman"
// database itself — see newTestAPI, which mints a disposable per-test
// database name and drops it on cleanup (docs/DESIGN.md: "no mocks-for-
// mongo"; the AUTH_FEATURES build instructions call for a separate database
// name for any destructive test flow).
func testMongoURI() string {
	if v := os.Getenv("MONGO_URI"); v != "" {
		return v
	}
	return "mongodb://localhost:27017"
}

var testDBCounter int64

// newTestStore connects to a fresh, uniquely-named scratch database,
// ensures indexes, and registers a drop+disconnect cleanup.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	n := atomic.AddInt64(&testDBCounter, 1)
	dbName := fmt.Sprintf("taskman_test_%d_%d", time.Now().UnixNano(), n)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := NewStore(ctx, testMongoURI(), dbName)
	if err != nil {
		t.Fatalf("connect test mongo (is taskman-mongo-1 running on %s?): %v", testMongoURI(), err)
	}
	if err := store.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure indexes: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = store.db.Drop(dropCtx)
		_ = store.Close(dropCtx)
	})
	return store
}

// newTestAPI wires a *api (with its own loginLimiter) around a fresh test
// store and returns it plus the store, for tests that want to seed data
// directly.
func newTestAPI(t *testing.T) (*api, *Store) {
	t.Helper()
	store := newTestStore(t)
	return &api{store: store, loginLimiter: newLoginLimiter()}, store
}

// testServer wraps a's routes with the same global middleware chain main.go
// uses, so tests exercise the real request pipeline (jsonGuard, sessionLoad,
// mustChangePasswordGate) end to end.
func testServer(a *api) *httptest.Server {
	mux := a.routes()
	handler := chain(mux, requestLog, jsonGuard, a.sessionLoad, mustChangePasswordGate)
	return httptest.NewServer(handler)
}

// jsonClient is an http.Client with a cookiejar (so it carries the session
// cookie automatically like a browser) and helpers that always set
// Content-Type: application/json, since jsonGuard requires it.
type jsonClient struct {
	c   *http.Client
	srv *httptest.Server
}

func newJSONClient(srv *httptest.Server) *jsonClient {
	jar, _ := cookiejar.New(nil)
	return &jsonClient{c: &http.Client{Jar: jar}, srv: srv}
}

func (j *jsonClient) do(method, path, body string) (*http.Response, error) {
	req, err := http.NewRequest(method, j.srv.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return j.c.Do(req)
}

// directMember inserts a member document straight into Mongo (bypassing
// HTTP/admin gating) so tests can seed fixtures without depending on the
// endpoints under test.
func directMember(t *testing.T, store *Store, m Member) Member {
	t.Helper()
	if m.ID.IsZero() {
		m.ID = bson.NewObjectID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := store.members.InsertOne(ctx, m); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	return m
}

func directTeam(t *testing.T, store *Store, name string) Team {
	t.Helper()
	team := Team{ID: bson.NewObjectID(), Name: name, CreatedAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := store.teams.InsertOne(ctx, team); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return team
}
