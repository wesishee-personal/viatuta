# Viatuta — common development tasks.
#
# `make help` lists everything. Variables can be overridden inline, e.g.
#   make migrate DATABASE_URL=postgres://localhost/viatuta_test

DATABASE_URL ?= postgres://localhost:5432/viatuta?sslmode=disable
GOOSE_DIR    := db/migrations
AUSTIN_PBF   := data/austin.osm.pbf

# Load .env if present so local overrides work without exporting by hand.
ifneq (,$(wildcard .env))
include .env
export
endif

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---- Setup ---------------------------------------------------------------

.PHONY: tools
tools: ## Install goose and sqlc into $(go env GOPATH)/bin
	go install github.com/pressly/goose/v3/cmd/goose@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

.PHONY: db-create
db-create: ## Create the local database and enable PostGIS
	createdb viatuta || true
	psql viatuta -c 'CREATE EXTENSION IF NOT EXISTS postgis;'

.PHONY: data
data: $(AUSTIN_PBF) ## Download the Austin OSM extract
$(AUSTIN_PBF):
	@mkdir -p data
	curl -fSL -o $@ https://download.bbbike.org/osm/bbbike/Austin/Austin.osm.pbf

# ---- Database ------------------------------------------------------------

.PHONY: migrate
migrate: ## Apply all pending migrations
	goose -dir $(GOOSE_DIR) postgres "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back the most recent migration
	goose -dir $(GOOSE_DIR) postgres "$(DATABASE_URL)" down

.PHONY: migrate-status
migrate-status: ## Show which migrations have been applied
	goose -dir $(GOOSE_DIR) postgres "$(DATABASE_URL)" status

.PHONY: sqlc
sqlc: ## Regenerate typed Go from db/migrations + internal/store/queries
	sqlc generate

.PHONY: psql
psql: ## Open a psql shell on the dev database
	psql "$(DATABASE_URL)"

# ---- Ingest (Phase 2+) ---------------------------------------------------

.PHONY: ingest
ingest: ## Load the OSM extract into Postgres
	go run ./cmd/viatuta ingest-osm --input $(AUSTIN_PBF)

.PHONY: score
score: ## Recompute Level of Traffic Stress for every edge
	go run ./cmd/viatuta score-edges

.PHONY: ingest-crashes
ingest-crashes: ## Load Austin cyclist crash records and match them to edges
	go run ./cmd/viatuta ingest-crashes

.PHONY: ingest-elevation
ingest-elevation: ## Download terrain tiles and derive per-edge gradient
	go run ./cmd/viatuta ingest-elevation

.PHONY: data-all
data-all: ingest score ingest-crashes ingest-elevation ## Full pipeline from a fresh extract

# ---- Build / run ---------------------------------------------------------

.PHONY: run
run: ## Run the API server
	go run ./cmd/api

.PHONY: build
build: ## Build binaries into ./bin
	go build -o bin/api ./cmd/api
	go build -o bin/viatuta ./cmd/viatuta

# ---- Frontend ------------------------------------------------------------

.PHONY: web-install
web-install: ## Install frontend dependencies
	cd web && npm install

.PHONY: web-dev
web-dev: ## Run the frontend dev server on :5173 (proxies /v1 to the API)
	cd web && npm run dev

.PHONY: web-build
web-build: ## Type-check and build the frontend into web/dist
	cd web && npm run build

# ---- Quality -------------------------------------------------------------

.PHONY: test
test: ## Run all tests
	go test ./...

.PHONY: check
check: ## Format check, vet, and test — run before calling a phase done
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	go test ./...

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy
