# Shortcuts for the commands in CONTRIBUTING.md. Everything here is a thin
# wrapper — nothing depends on make, and the raw commands are in the docs.

.DEFAULT_GOAL := help
.PHONY: help dev-api dev-web check check-api check-web fmt build-image schema-check compose-up compose-down

PG_URL ?= postgresql://postgres:postgres@localhost:5432/postgres

help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

dev-api: ## Run the Go API on :8080
	cd backend && go run ./cmd/server

dev-web: ## Run the Next.js frontend on :3000
	cd frontend && npm run dev

check: check-api check-web ## Everything CI checks, locally

check-api: ## Go build + vet + test
	cd backend && go build ./... && go vet ./... && go test -race ./...

check-web: ## Frontend typecheck + lint + build
	cd frontend && npm run typecheck && npm run lint && npm run build

fmt: ## gofmt the backend
	cd backend && gofmt -w .

build-image: ## Build the exact image Cloud Run builds
	docker build -t sprintly-api .

schema-check: ## Apply supabase/schema.sql twice against $(PG_URL) to prove idempotency
	psql "$(PG_URL)" -v ON_ERROR_STOP=1 -f supabase/schema.sql
	psql "$(PG_URL)" -v ON_ERROR_STOP=1 -f supabase/schema.sql
	@echo "schema.sql applied twice cleanly"

compose-up: ## API + web against a hosted Supabase project (needs .env)
	docker compose up --build

compose-down:
	docker compose down
