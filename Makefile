.PHONY: db run build up down clean

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


