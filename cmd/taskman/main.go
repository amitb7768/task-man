// Command taskman is the server: env config, migrate-at-boot, first-admin
// seed, then the JSON API + SPA on :8484 with graceful shutdown
// (docs/DESIGN_PG_FSM_MIGRATION.md package layout).
//
// Run it from the repo root: the static UI is served from the CWD-relative
// path ui/dist (make run / go run ./cmd/taskman do that).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"taskman/internal/httpapi"
	"taskman/internal/repo"
	"taskman/internal/service"
)

// shutdownTimeout bounds how long in-flight requests get to finish after
// SIGINT/SIGTERM before the listener is torn down regardless.
const shutdownTimeout = 10 * time.Second

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
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	svc := service.New(db)
	if err := service.SeedAdmin(ctx, svc); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	// CWD-relative on purpose: run from the repo root.
	const distDir = "ui/dist"
	var static http.Handler
	if info, err := os.Stat(distDir); err == nil && info.IsDir() {
		static = spaHandler(distDir)
		log.Printf("serving static UI from %s", distDir)
	} else {
		log.Printf("%s not found; running API-only (no static UI)", distDir)
	}

	addr := ":" + port
	srv := &http.Server{Addr: addr, Handler: httpapi.Handler(svc, static)}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("taskman listening on %s (postgres %s)", addr, redactDSN(dsn))
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		// Listener failed (e.g. port in use) before any signal arrived.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-sigCtx.Done():
		stop() // a second signal now kills the process the default way
		log.Printf("shutting down (draining up to %s)", shutdownTimeout)
		shCtx, shCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shCancel()
		if err := srv.Shutdown(shCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("close postgres: %v", err)
	}
	log.Printf("taskman stopped")
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
