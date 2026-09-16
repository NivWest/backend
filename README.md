# NivWest Backend

Go backend for the NivWest stock simulator. The API manages simulator users and proxies stock search, quote, details, and price-chart data from Avanza. PostgreSQL is used for application data and is automatically migrated when the server starts.

## Requirements

- Go 1.25 or newer
- Docker and Docker Compose
- Network access to Avanza for stock endpoints

## Quick start

1. Start PostgreSQL:

	```sh
	docker compose up -d postgres
	```

2. Optionally create a `.env` file in the project root. The defaults work with the supplied Compose service:

	```dotenv
	DB_HOST=localhost
	DB_PORT=5432
	DB_USER=postgres
	DB_PASSWORD=postgres
	DB_NAME=avanza
	DB_SSLMODE=disable
	```

3. Start the API:

	```sh
	go run ./cmd/api
	```

The server listens on `http://localhost:8080` by default. Database tables are created or updated automatically during startup.

To stop PostgreSQL while preserving its data volume:

```sh
docker compose down
```

To remove the database volume as well:

```sh
docker compose down -v
```

## Configuration

Configuration is read from environment variables and, when present, `.env`. Invalid integer and duration values fall back to their defaults.

| Variable | Default | Description |
| --- | --- | --- |
| `SERVER_HOST` | `0.0.0.0` | HTTP bind host |
| `SERVER_PORT` | `8080` | HTTP bind port |
| `SERVER_READ_TIMEOUT` | `10s` | HTTP read timeout |
| `SERVER_WRITE_TIMEOUT` | `10s` | HTTP write timeout |
| `SERVER_IDLE_TIMEOUT` | `60s` | HTTP idle timeout |
| `DB_HOST` | `localhost` | PostgreSQL host |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_USER` | `postgres` | PostgreSQL user |
| `DB_PASSWORD` | `postgres` | PostgreSQL password |
| `DB_NAME` | `avanza` | PostgreSQL database |
| `DB_SSLMODE` | `disable` | PostgreSQL SSL mode |
| `DB_MAX_IDLE_CONNS` | `10` | Maximum idle database connections |
| `DB_MAX_OPEN_CONNS` | `100` | Maximum open database connections |
| `REDIS_ADDR` | `localhost:6379` | Redis address reserved for future use |
| `REDIS_PASSWORD` | empty | Redis password reserved for future use |
| `REDIS_DB` | `0` | Redis database reserved for future use |
| `AVANZA_BASE_URL` | `https://www.avanza.se` | Configured upstream URL |
| `AVANZA_TIMEOUT` | `10s` | Avanza request timeout |
| `RATE_LIMIT_REQUESTS_PER_SECOND` | `10` | Reserved rate-limit setting |
| `RATE_LIMIT_BURST` | `20` | Reserved rate-limit setting |

The current Avanza integration uses `https://www.avanza.se` directly. `AVANZA_BASE_URL` is loaded but is not yet applied to outbound requests. Stock routes have a per-IP limiter of five requests per second with a burst of ten.

## API

All routes are prefixed with `/api/v1`.

### Users

Create a user:

```sh
curl -X POST http://localhost:8080/api/v1/users/ \
  -H 'Content-Type: application/json' \
  -d '{"google_id":"google-user-123","email":"user@example.com","name":"Example User"}'
```

Get a user by numeric ID:

```sh
curl http://localhost:8080/api/v1/users/1
```

User creation returns `201 Created`. Duplicate Google IDs or email addresses return `409 Conflict`.

### Stocks

Search Avanza instruments:

```sh
curl 'http://localhost:8080/api/v1/stocks/search?q=volvo'
```

Get a quote, details, or price chart by Avanza order book ID:

```sh
curl 'http://localhost:8080/api/v1/stocks/quote?orderbookID=12345'
curl 'http://localhost:8080/api/v1/stocks/details?orderbookID=12345'
curl 'http://localhost:8080/api/v1/stocks/chart?orderbookID=12345&timePeriod=one_year'
```

Stock endpoint errors are returned as JSON with an `error` field. Responses from Avanza are passed through as JSON.

## Project structure

```text
cmd/api/                 HTTP server entry point
internal/app/             Application wiring and lifecycle
internal/config/          Environment configuration
internal/domain/          Domain contracts and errors
internal/handlers/        HTTP handlers and route registration
internal/integrations/    PostgreSQL and Avanza integrations
internal/middleware/      HTTP middleware
internal/models/          GORM persistence models
internal/repository/      Database repositories
internal/services/        Application services
```

## Development

Format and compile the project with:

```sh
gofmt -w cmd internal
go test ./...
go build ./cmd/api
```

There are currently no test files in the repository, so `go test ./...` acts as a package compilation check.