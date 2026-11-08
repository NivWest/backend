.PHONY: db run build up down clean logs api-lint

VERSION := $(shell cat VERSION 2>/dev/null || echo dev)
LDFLAGS := -X backend/internal/version.Version=$(VERSION)
export VERSION

# Start the database only (in background)
db:
	docker compose up -d postgres

# Run the Go application locally (starts db first)
run: db
	go run -ldflags "$(LDFLAGS)" ./cmd/api

# Build the API binary with the version from VERSION
build:
	go build -ldflags "$(LDFLAGS)" -o bin/nivwest-api ./cmd/api

# Start the full application stack (database + api) via Docker Compose
up:
	docker compose up --build -d

# Stop all containers and remove them
down:
	docker compose down

# Stop containers and remove volumes/orphans
clean:
	docker compose down -v --remove-orphans

# View logs for the running application
logs:
	docker compose logs -f api



# Lint the OpenAPI contract (api/openapi.yaml) with Redocly; rules in redocly.yaml. Needs Node and network
# for npx, so it runs in CI (job api-contract), not in the offline fast checks.
REDOCLY_VERSION := 2.11.0
api-lint:
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint api/openapi.yaml
