.PHONY: db run up down clean

# Start the database only (in background)
db:
	docker compose up -d postgres

# Run the Go application locally (starts db first)
run: db
	go run ./cmd/api

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


