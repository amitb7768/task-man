package main

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"taskman/internal/repo"
)

func main() {
	dsn := os.Getenv("TASKMAN_PG_DSN")
	if dsn == "" {
		log.Fatal("TASKMAN_PG_DSN is not set (postgres://user:pass@host:5432/db?sslmode=disable; see .env.example)")
	}
	port := envOr("PORT", "8484")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Boot order (docs/DESIGN_PG_FSM_MIGRATION.md): schema migrations first,
	// then the pool, then the first-admin bootstrap.
	if err := repo.Migrate(dsn); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	db, err := repo.Open(dsn)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	store := repo.New(db)
	if err := repo.SeedAdmin(ctx, store); err != nil {
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
	log.Printf("taskman listening on %s (postgres %s)", addr, redactDSN(dsn))
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

// redactDSN strips the password from a URL-form DSN for logging.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return "(dsn)"
	}
	return u.Redacted()
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
