.PHONY: up down mongo pg ui run test vet mac-dev mac-build

# whole stack: mongo container + fresh UI build + server on :8484
up: mongo ui run

# stop the server (whatever holds :8484) and the mongo container; data volume survives
down:
	-lsof -ti :8484 | xargs kill 2>/dev/null
	docker compose down

mongo:
	docker compose up -d

# Postgres target for the Mongo->Postgres migration
# (docs/DESIGN_PG_FSM_MIGRATION.md). Not part of `up` yet.
pg:
	docker compose up -d postgres

ui:
	cd ui && npm ci && npm run build

run:
	go run ./server

test:
	go test ./...

vet:
	go vet ./...

# macOS desktop shell (Tauri v2, src-tauri/) — thin client over `make up`.
# Requires: rustup + `cargo install tauri-cli --version '^2'` (once).
mac-dev:
	cd src-tauri && cargo tauri dev

mac-build:
	cd src-tauri && cargo tauri build
