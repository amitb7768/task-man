# taskman — Makefile in the pbh-services shape (help-first, .env-driven),
# with only the commands that are real here: no Docker deploy, no Flyway
# (migrations are embedded SQL run at server boot), no Wire.
#
# Prod runs on THIS machine: ./taskman-bin on :8484, Postgres in the
# `taskman-pg-dev` container (NOT compose-managed — never `docker rm` it),
# database `taskman`. docs/DEPLOYMENT.md + docs/DESIGN_PG_FSM_MIGRATION.md.

-include .env
export

# Defaults match prod; .env (if present) wins, environment wins over both.
TASKMAN_PG_DSN      ?= postgres://postgres:postgres@localhost:5432/taskman?sslmode=disable
TASKMAN_TEST_PG_DSN ?= postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
PG_CONTAINER        ?= taskman-pg-dev
PG_DB               ?= taskman

.PHONY: help up down start stop restart status logs build run ui deps \
        test test-race vet check pg psql backup create-migration mongo \
        mac-dev mac-build

.DEFAULT_GOAL := help

help: ## Show this help message
	@echo "taskman - Available Commands:"
	@echo ""
	@echo "Server (prod runs on this machine):"
	@echo "  start           Build + start the server in the background (:8484, logs to server.log)"
	@echo "  stop            Stop the server (SIGTERM = graceful drain)"
	@echo "  restart         stop + start (the loopback-403 fix: restarts from the repo dir)"
	@echo "  status          Server pid + taskman containers"
	@echo "  logs            Follow server.log"
	@echo ""
	@echo "Build & test:"
	@echo "  build           Compile ./cmd/taskman -> taskman-bin"
	@echo "  run             Run the server in the FOREGROUND (dev)"
	@echo "  ui              npm ci + build the SPA (served fresh from ui/dist, no restart needed)"
	@echo "  up              pg + ui + run (dev stack, foreground)"
	@echo "  down            Stop the server and compose containers (volumes survive)"
	@echo "  test            go test ./... (PG tests need a reachable TASKMAN_TEST_PG_DSN)"
	@echo "  test-race       Same, under the race detector"
	@echo "  vet / check     go vet  /  vet + test"
	@echo "  deps            go mod download + tidy"
	@echo ""
	@echo "Database (migrations run automatically at server boot):"
	@echo "  psql            Interactive psql into the prod DB ($(PG_CONTAINER)/$(PG_DB))"
	@echo "  backup          pg_dump the prod DB into backups/ (timestamped)"
	@echo "  create-migration  Scaffold internal/repo/migrations/NNNN_<desc>.up.sql"
	@echo "  pg              Start the compose postgres service (fresh machines ONLY --"
	@echo "                  it binds :5432 and conflicts with the running $(PG_CONTAINER))"
	@echo ""
	@echo "Legacy (Mongo soak window, retire after the P4 soak):"
	@echo "  mongo           docker compose up -d (includes the old mongo service)"
	@echo ""
	@echo "macOS app:"
	@echo "  mac-dev / mac-build   Tauri shell (needs rustup + cargo tauri-cli v2)"

# ========================================
# Server operations (prod)
# ========================================

start: build ## Build and start the server in the background
	@if lsof -ti :8484 >/dev/null 2>&1; then echo "✗ already running on :8484 — use make restart"; exit 1; fi
	@nohup env TASKMAN_PG_DSN='$(TASKMAN_PG_DSN)' ./taskman-bin > server.log 2>&1 & \
	sleep 1; \
	if lsof -ti :8484 >/dev/null 2>&1; then echo "✅ started (pid $$(lsof -ti :8484)) — log: server.log"; else echo "✗ failed to start:"; tail -5 server.log; exit 1; fi

stop: ## Stop the server (graceful SIGTERM)
	-@lsof -ti :8484 | xargs kill 2>/dev/null
	@echo "✅ stopped (anything on :8484)"

restart: stop start ## Stop then start

status: ## Server + container status
	@lsof -ti :8484 >/dev/null 2>&1 && echo "server: UP (pid $$(lsof -ti :8484 | head -1))" || echo "server: DOWN"
	@docker ps --format '{{.Names}}\t{{.Status}}' | grep -Ei 'taskman|postgres' || echo "no taskman containers running"

logs: ## Follow the server log
	tail -f server.log

# ========================================
# Build & test
# ========================================

build: ## Compile the deployable binary (run it from the repo root: it serves CWD-relative ui/dist)
	go build -o taskman-bin ./cmd/taskman

run: ## Run in the foreground (dev; Ctrl-C to stop)
	TASKMAN_PG_DSN='$(TASKMAN_PG_DSN)' go run ./cmd/taskman

ui: ## Build the SPA; the server serves ui/dist fresh per request
	cd ui && npm ci && npm run build

up: pg ui run ## Dev stack, foreground

down: ## Stop server + compose containers; data volumes (and taskman-pg-dev) survive
	-lsof -ti :8484 | xargs kill 2>/dev/null
	docker compose down

deps: ## Download and tidy Go dependencies
	go mod download
	go mod tidy

# Postgres-backed tests (repo/service/httpapi) each migrate a throwaway
# scratch schema on TASKMAN_TEST_PG_DSN and drop it; they skip when unset.
test: ## Run all tests
	TASKMAN_TEST_PG_DSN='$(TASKMAN_TEST_PG_DSN)' go test ./...

test-race: ## All tests under -race
	TASKMAN_TEST_PG_DSN='$(TASKMAN_TEST_PG_DSN)' go test -race ./...

vet: ## go vet
	go vet ./...

check: vet test ## vet + test

# ========================================
# Database
# ========================================

psql: ## Interactive psql into the prod DB
	docker exec -it $(PG_CONTAINER) psql -U postgres -d $(PG_DB)

backup: ## Timestamped pg_dump into backups/
	@mkdir -p backups
	docker exec $(PG_CONTAINER) pg_dump -U postgres $(PG_DB) > backups/pgdump-$$(date +%Y%m%d-%H%M).sql
	@ls -lh backups/ | tail -3

# Scaffold the next sequential golang-migrate file. It runs automatically
# at the next server boot (repo.Migrate) — there is no separate migrate step.
create-migration: ## Create internal/repo/migrations/NNNN_<desc>.up.sql
	@read -p "Enter description (e.g. add_labels_table): " desc; \
	desc=$${desc// /_}; \
	dir=internal/repo/migrations; \
	last=$$(ls $$dir | grep -oE '^[0-9]{4}' | sort -n | tail -1 | sed 's/^0*//'); \
	next=$$(printf '%04d' $$(( $${last:-0} + 1 ))); \
	f="$$dir/$${next}_$${desc}.up.sql"; \
	touch "$$f"; \
	echo "Created $$f (applies at next server boot/restart)"

# Compose postgres (named volume taskman-pgdata). For FRESH machines only:
# prod currently runs on the plain $(PG_CONTAINER) container, which already
# holds :5432 — don't run both.
pg: ## Start compose postgres (fresh machine only; see help)
	docker compose up -d postgres

# ========================================
# Legacy — Mongo soak window (P4). Retire this target, the compose mongo
# service, cmd/migrate-mongo and the mongo-driver dep after the soak.
# ========================================

mongo: ## Legacy: compose up -d (includes old mongo)
	docker compose up -d

# ========================================
# macOS app (Tauri v2, src-tauri/)
# ========================================

mac-dev: ## Tauri dev shell
	cd src-tauri && cargo tauri dev

mac-build: ## Tauri release build
	cd src-tauri && cargo tauri build
