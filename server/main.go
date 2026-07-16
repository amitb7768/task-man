package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	uri := envOr("MONGO_URI", "mongodb://localhost:27017")
	dbName := envOr("MONGO_DB", "taskman")
	port := envOr("PORT", "8484")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := NewStore(ctx, uri, dbName)
	if err != nil {
		log.Fatalf("connect mongo: %v", err)
	}
	defer store.Close(context.Background())

	if err := store.EnsureIndexes(ctx); err != nil {
		log.Fatalf("ensure indexes: %v", err)
	}
	if err := store.BackfillWeekOf(ctx); err != nil {
		log.Fatalf("backfill weekOf: %v", err)
	}
	if err := SeedAdmin(ctx, store); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	a := &api{store: store, loginLimiter: newLoginLimiter()}
	mux := a.routes()

	const distDir = "ui/dist"
	if info, err := os.Stat(distDir); err == nil && info.IsDir() {
		mux.Handle("/", spaHandler(distDir))
		log.Printf("serving static UI from %s", distDir)
	} else {
		log.Printf("%s not found; running API-only (no static UI)", distDir)
	}

	// Global middleware chain wraps the whole mux (docs/AUTH_FEATURES.md
	// server architecture): requestLog -> jsonGuard -> sessionLoad ->
	// mustChangePasswordGate -> mux (which applies its own per-route
	// requireAuth/requireAdmin wrappers).
	handler := chain(mux, requestLog, jsonGuard, a.sessionLoad, mustChangePasswordGate)

	addr := ":" + port
	log.Printf("taskman listening on %s (db %q)", addr, dbName)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// spaHandler serves static files from dir, falling back to dir/index.html
// for any path that doesn't resolve to a real file (SPA client-side routing).
func spaHandler(dir string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			http.ServeFile(w, r, index)
			return
		}
		fs.ServeHTTP(w, r)
	}
}
