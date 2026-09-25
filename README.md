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
| `GOOGLE_CLIENT_ID` | empty | Google OAuth 2.0 client ID |
| `GOOGLE_CLIENT_SECRET` | empty | Google OAuth 2.0 client secret |
| `GOOGLE_REDIRECT_URL` | `http://localhost:8080/auth/callback` | Registered Google OAuth callback URL |
| `FRONTEND_URL` | `http://localhost:3000` | Frontend URL used after login |
| `AUTH_SESSION_TTL` | `24h` | PostgreSQL session lifetime |
| `AUTH_OAUTH_STATE_TTL` | `15m` | OAuth state and PKCE lifetime |
| `AUTH_COOKIE_SECURE` | `false` | Set `true` when serving over HTTPS |

The current Avanza integration uses `https://www.avanza.se` directly. `AVANZA_BASE_URL` is loaded but is not yet applied to outbound requests. Stock routes have a per-IP limiter of five requests per second with a burst of ten.

## API

All routes are prefixed with `/api/v1`.

### Users

Start Google login by navigating to `GET /auth/login`. The callback creates an opaque PostgreSQL session and redirects to `{FRONTEND_URL}/dashboard`. The browser must send the `HttpOnly` `session_id` cookie on API requests. `POST /auth/logout` deletes the session.

Authenticated profile and admin routes:

```sh
curl http://localhost:8080/api/v1/user/profile
curl -X DELETE http://localhost:8080/api/v1/admin/users/123
```

New Google users are created with the `member` role. Promote an account to `admin` directly in PostgreSQL when appropriate; admin routes return `403 Forbidden` for members.

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

## Containers

Build and start the API and PostgreSQL locally with:

```sh
docker compose up --build -d
```

The API is available on port `8080`. PostgreSQL data is stored in `./data/postgres` by default. Set `POSTGRES_DATA_DIR` to change the host directory. The API waits for PostgreSQL's health check before starting.

The Compose file also accepts `IMAGE_NAME`, `IMAGE_TAG`, and `APP_ENV_FILE`, which are used by the deployment workflow.

## CI/CD and Google Compute Engine

`.github/workflows/ci-cd.yml` runs on pull requests and pushes to `main`:

- verifies formatting, dependencies, tests, Go compilation, and Compose configuration;
- builds and publishes `ghcr.io/<owner>/<repository>:<commit-sha>` and `:latest` on `main`;
- deploys the image to a Google Compute Engine VM over `gcloud compute ssh`.

Configure these GitHub repository **Variables**:

| Variable | Description |
| --- | --- |
| `GCP_PROJECT_ID` | Google Cloud project ID |
| `GCE_INSTANCE` | Target Compute Engine instance name |
| `GCE_ZONE` | Instance zone, for example `europe-west1-b` |
| `GCE_USER` | Linux username configured on the VM |
| `DB_SSLMODE` | Usually `disable` for a local PostgreSQL container |

Configure these GitHub repository **Secrets**:

| Secret | Description |
| --- | --- |
| `GCP_SERVICE_ACCOUNT_KEY` | JSON key for a service account allowed to SSH to and manage the VM |
| `GHCR_READ_TOKEN` | GitHub token with read access to the private container package |
| `DB_USER` | PostgreSQL application user |
| `DB_PASSWORD` | PostgreSQL password |
| `DB_NAME` | PostgreSQL database name |

The VM must have an external IP, SSH access for the configured user, and firewall access to TCP port `8080` if the API should be publicly reachable. The workflow installs Docker if needed but requires the Docker Compose plugin. Attach and mount a Google persistent disk at `/var/lib/nivwest` before the first deployment; PostgreSQL is stored at `/var/lib/nivwest/postgres`, so replacing containers does not remove database data.

The deployment creates `/opt/nivwest/.env` with mode `0600`, pulls the commit image, and runs `docker compose up -d --remove-orphans`. Do not use `docker compose down -v` on the VM, because removing the storage volume would delete application data.