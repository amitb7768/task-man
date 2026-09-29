.PHONY: up down mongo pg ui run build test vet mac-dev mac-build

# Postgres DSNs (docs/DESIGN_PG_FSM_MIGRATION.md). Defaults match
# docker-compose's `postgres` service; override from the environment (or a
# sourced .env — see .env.example) for any other database.
TASKMAN_PG_DSN ?= postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
TASKMAN_TEST_PG_DSN ?= postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable

# whole stack: postgres container + fresh UI build + server on :8484
up: pg ui run

# stop the server (whatever holds :8484) and the compose containers; data volumes survive
down:
	-lsof -ti :8484 | xargs kill 2>/dev/null
	docker compose down

# Legacy: `docker compose up -d` (every compose service, mongo included).
# The server no longer uses Mongo; this target stays until the
# one-shot Mongo->Postgres data migration (P4, cmd/migrate-mongo) retires it.
mongo:
	docker compose up -d

# The server's database (docker-compose `postgres` service).
pg:
	docker compose up -d postgres

ui:
	cd ui && npm ci && npm run build

# Run from the repo root: the server serves the CWD-relative ui/dist.
run:
	TASKMAN_PG_DSN='$(TASKMAN_PG_DSN)' go run ./cmd/taskman

# The deployable binary (docs/DEPLOYMENT.md); run it from the repo root too.
build:
	go build -o taskman-bin ./cmd/taskman

# Postgres-backed tests (internal/repo, internal/service, internal/httpapi)
# each migrate a throwaway scratch schema on TASKMAN_TEST_PG_DSN and drop it
# afterwards; they skip when it's unset. Needs `make pg` up.
test:
	TASKMAN_TEST_PG_DSN='$(TASKMAN_TEST_PG_DSN)' go test ./...

vet:
	go vet ./...

# macOS desktop shell (Tauri v2, src-tauri/) — thin client over `make up`.
# Requires: rustup + `cargo install tauri-cli --version '^2'` (once).
mac-dev:
	cd src-tauri && cargo tauri dev

mac-build:
	cd src-tauri && cargo tauri build
