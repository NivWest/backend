# NivWest Backend — Agent Development Guidelines (`AGENTS.md`)

This document serves as the repository-specific guide for AI coding agents (and human contributors) working on the **NivWest Backend**. It defines architectural invariants, code standards, directory conventions, development workflows, and step-by-step recipes for extending this codebase consistently.

---

## 1. Project Overview & Tech Stack

NivWest Backend is a Go API service powering the NivWest stock trading simulator. It proxies market data from Avanza, manages simulator accounts, tracks virtual portfolios with market frictions (simulated execution latency and volume slippage), handles watchlists, and authenticates users via Google OAuth 2.0 (PKCE) and opaque PostgreSQL-backed sessions.

- **Language / Runtime:** Go 1.25 (`module backend`)
- **HTTP Web Framework:** Gin (`github.com/gin-gonic/gin`)
- **ORM / Database:** GORM (`gorm.io/gorm`) with PostgreSQL 16 driver (`gorm.io/driver/postgres`)
- **Authentication:** Google OAuth 2.0 with PKCE (`golang.org/x/oauth2`), secure HTTP-only cookie sessions
- **Rate Limiting:** `golang.org/x/time/rate` (in-memory per-IP token bucket)
- **External Integration:** Avanza public web API (`https://www.avanza.se`)
- **Containerization:** Multi-stage Dockerfile (`golang:1.25-alpine` -> `distroless/static-debian12`), Docker Compose

---

## 2. Architecture & Layered Structure

The project strictly follows a **Clean / Layered Architecture**. Dependencies flow **inward** toward the domain layer.

```text
HTTP Request
     │
     ▼
[cmd/api/main.go]           HTTP Server & Router bootstrap
     │
     ▼
[internal/middleware/]      Auth, Roles, Rate Limiting
     │
     ▼
[internal/handlers/]        HTTP Presentation Layer (Gin JSON endpoints)
     │
     ▼
[internal/services/]        Business Logic Layer (market simulation, calculations)
     │
     ▼
[internal/domain/]          Domain Contracts (interfaces, DTOs, errors)
     │
     ├──────────────────────────┐
     ▼                          ▼
[internal/repository/]     [internal/integrations/]
(GORM / Postgres)          (Avanza HTTP client, DB pooling)
     │
     ▼
[internal/models/]         Database schema models & tags
```

### Directory Responsibilities

| Path | Responsibility | Permitted Dependencies | Forbidden Patterns |
|---|---|---|---|
| `cmd/api/` | Server entry point, config loading, graceful shutdown, handler mounting. | `internal/app`, `internal/config`, `github.com/gin-gonic/gin` | Business logic, direct DB calls, SQL queries. |
| `internal/app/` | Dependency injection container (`NewApp`). Wires DB, repositories, services, and handlers. | All `internal/*` packages | HTTP request handling, business logic. |
| `internal/config/` | Environment variables parsing and configuration defaults. | `github.com/joho/godotenv`, stdlib | Database, services, handlers. |
| `internal/domain/` | Core domain interfaces, request/response DTOs, domain sentinel errors. | `internal/models`, stdlib | GORM queries, Gin context, external HTTP clients. |
| `internal/models/` | GORM entity definitions, table schemas, JSON/GORM tags. | `gorm.io/gorm`, stdlib | Business logic, handlers, services. |
| `internal/integrations/` | Low-level external clients (`avanza/`, `database/`). | `internal/config`, `internal/models`, `gorm.io/*`, stdlib | Gin handlers, domain services. |
| `internal/repository/` | Concrete implementations of `domain.*Repository` using GORM. | `internal/domain`, `internal/models`, `gorm.io/*` | Gin context, HTTP status codes, console printing. |
| `internal/services/` | Business rules, calculations, market frictions, orchestration. | `internal/domain`, `internal/models`, `internal/config`, stdlib | Gin context (`gin.Context`), raw SQL queries, direct GORM access. |
| `internal/handlers/` | HTTP request binding, status codes, JSON responses. Implements `Registrar`. | `internal/domain`, `internal/services`, `internal/middleware`, `internal/models`, `gin` | GORM queries, direct DB access, heavy business logic. |
| `internal/middleware/` | Cross-cutting HTTP middleware (auth verification, role checks, rate limiting). | `internal/domain`, `internal/models`, `gin` | Heavy database writes, business logic. |

---

## 3. Core Design Patterns & Invariants

When extending or modifying this repository, you **MUST** uphold the following invariants:

### 3.1. Handlers and the `Registrar` Interface
All handlers must implement the `Registrar` interface defined in `cmd/api/main.go`:
```go
type Registrar interface {
    Register(v1 *gin.RouterGroup)
}
```
- Handlers mount their sub-routes on the passed `v1` router group (which is prefixed with `/api/v1`).
- Handlers are instantiated in `internal/app/app.go` and mounted inside `cmd/api/main.go` via `mountHandlers(engine, app.Handler1, ...)`.
- Handlers **MUST NEVER** access GORM or execute database operations directly. Handlers only interact with Application Services (or the `domain.AuthRepository` for auth middleware checks).

### 3.2. Interfaces & Dependency Inversion
- Application Services must depend on **Domain Interfaces** (`domain.OrderRepository`, `domain.StockProvider`, `domain.UserRepository`, etc.), never on concrete GORM repository structs.
- Repositories return domain models or domain errors.
- Never use global database variables or reflection-based dependency injection frameworks. All dependencies are injected via constructor functions (`New<Component>`).

### 3.3. Context Propagation
- All repository methods, service methods, and external HTTP calls **MUST** accept `context.Context` as their first parameter.
- In GORM repositories, always use `r.db.WithContext(ctx)`.
- In HTTP clients, always use `http.NewRequestWithContext(ctx, ...)`.

### 3.4. Database Concurrency & Financial Transactions
- Any operation altering balances, positions, or transactions (e.g., buying or selling stocks) **MUST** execute inside a GORM transaction:
  ```go
  r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { ... })
  ```
- Use row-level locking (`clause.Locking{Strength: "UPDATE"}`) when reading records that will be updated in the same transaction (e.g., user balance, position):
  ```go
  tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID)
  ```
- Always verify balance or quantity sufficiency before deducting. Return clear errors on failure so the transaction automatically rolls back.

### 3.5. Concurrent Stock Quote Fetching
When fetching live stock quotes for multiple items (e.g. in `PortfolioService.GetDashboard`):
- Run parallel fetches in goroutines using `sync.WaitGroup`.
- Guard slice appends and shared data mutations with `sync.Mutex`.
- Include fallback logic (e.g., if live quote fails, fall back to `AvgPrice`).

### 3.6. Error Handling & HTTP Status Codes
- Define domain-specific errors in `internal/domain/errors.go` (e.g., `ErrUserNotFound`, `ErrUserAlreadyExists`).
- Repositories translate `gorm.ErrRecordNotFound` to domain errors (e.g., `domain.ErrUserNotFound`).
- Handlers check errors using `errors.Is(err, ...)` and map them to appropriate HTTP status codes:
  - `400 Bad Request`: Validation failure (`c.ShouldBindJSON`), invalid URL/query parameter, domain business rule violation (e.g. "insufficient funds").
  - `401 Unauthorized`: Missing or invalid session cookie (`middleware.RequireAuth`).
  - `403 Forbidden`: Authenticated user lacks required role (`middleware.RequireRole("admin")`).
  - `404 Not Found`: Entity not found (`domain.ErrUserNotFound`).
  - `409 Conflict`: Unique constraint violation (`domain.ErrUserAlreadyExists`).
  - `429 Too Many Requests`: Rate limiter triggered.
  - `500 Internal Server Error`: Unhandled database or system errors.
- Responses must always follow the standard JSON format:
  ```json
  {"error": "description of the error"}
  ```

### 3.7. GORM Models & Automatic Migrations
- All database entities live in `internal/models/models.go`.
- Use explicit GORM tags: `primaryKey`, `uniqueIndex`, `index`, `not null`, `constraint:OnDelete:CASCADE;`.
- Specify precision for numeric values:
  - Currency / Prices: `gorm:"type:numeric(15,2)"`
  - Fractional Quantities: `gorm:"type:numeric(15,4)"`
- Use `gorm.DeletedAt` for soft-deletable entities (`User`, `Stock`).
- Mark internal or sensitive fields with `json:"-"` (e.g. session IDs, PKCE verifiers, deleted timestamps).
- **Auto-migration:** Every new model **MUST** be added to `internal/integrations/database/migrate.go` in `db.AutoMigrate(...)`.

---

## 4. Step-by-Step Recipe: Implementing a New Feature

When tasked with adding a new entity or endpoint, follow this sequence:

1. **Model Definition (`internal/models/models.go`):**
   - Define the struct with proper GORM and JSON tags.
2. **Migration Registration (`internal/integrations/database/migrate.go`):**
   - Add `&models.NewEntity{}` to `db.AutoMigrate(...)`.
3. **Domain Contracts & DTOs (`internal/domain/`):**
   - Add request/response DTOs in `internal/domain/repo.go`.
   - Define domain repository interface (e.g., `NewEntityRepository`) in `internal/domain/repo.go`.
   - Add domain error variables in `internal/domain/errors.go` if needed.
4. **Repository Implementation (`internal/repository/<entity>.go`):**
   - Create private struct `newEntityRepo` and constructor `NewNewEntityRepository(db *gorm.DB) domain.NewEntityRepository`.
   - Implement all interface methods using `r.db.WithContext(ctx)`.
5. **Business Service (`internal/services/<entity>_service.go`):**
   - Create `NewEntityService` accepting the domain repository interface.
   - Implement business logic, validations, calculations, and simulations.
6. **HTTP Handler (`internal/handlers/<entity>_handler.go`):**
   - Create `NewEntityHandler` accepting the service and `domain.AuthRepository`.
   - Implement `Register(v1 *gin.RouterGroup)`.
   - Use `middleware.RequireAuth(h.auth)` for protected endpoints.
   - Use `middleware.CurrentUser(c)` to extract authenticated user.
   - Parse input with `c.ShouldBindJSON` or `strconv.ParseUint(c.Param("id"), 10, 32)`.
   - Return clean JSON payloads with appropriate HTTP status codes.
7. **Application Wiring:**
   - In `internal/app/app.go`:
     - Instantiate repository in `NewApp`.
     - Instantiate service.
     - Instantiate handler and assign to `App` struct.
   - In `cmd/api/main.go`:
     - Pass `app.NewEntityHandler` into `mountHandlers(...)`.
8. **Verification:**
   - Format: `gofmt -w cmd internal`
   - Test: `go test ./...`
   - Compile: `go build ./cmd/api`

---

## 5. Development & Testing Workflow

### 5.1. Common Commands

```sh
# Format all Go files
gofmt -w cmd internal

# Run tests
go test -v ./...

# Compile the application binary
go build ./cmd/api

# Run local database via Docker Compose
docker compose up -d postgres

# Run backend locally
go run ./cmd/api

# Build container image
docker compose up --build -d
```

### 5.2. Testing Guidelines
- Write unit tests alongside packages using the naming convention `*_test.go`.
- Test services by mocking the domain repository interfaces (`domain.UserRepository`, etc.).
- Test handlers using `httptest.NewRecorder()` and Gin's test context (`gin.SetMode(gin.TestMode)`).
- Always verify that `go test ./...` passes before completing any task.

---

## 6. Git & Agent Workflow Conventions

Automated agents (e.g. Jules, CI bots) and contributors must follow these Git standards:

### 6.1. Branch Naming
Branch directly from `main`:
- Bug fixes: `jules/fix/<issue-number>-<short-slug>` or `fix/<short-slug>`
- Features: `jules/feat/<issue-number>-<short-slug>` or `feat/<short-slug>`
- Refactors: `jules/refactor/<issue-number>-<short-slug>` or `refactor/<short-slug>`
- Chores & Docs: `jules/chore/<issue-number>-<short-slug>` or `docs/<short-slug>`

### 6.2. Commit Messages (Conventional Commits)
Use atomic commits with standard Conventional Commit prefixes:
- `feat(scope): add watchlist item reordering`
- `fix(scope): handle negative balances during sell orders`
- `refactor(scope): extract Avanza quote parsing to dedicated DTO`
- `test(scope): add unit tests for order service slippage`
- `docs(scope): update API endpoint documentation in README`
- `chore(scope): bump dependency versions`

Allowed scopes: `auth`, `order`, `portfolio`, `stock`, `user`, `watchlist`, `config`, `db`, `api`.

### 6.3. Pull Request Requirements
- **Title:** Conventional commit format matching the primary change (e.g. `feat(order): implement market limit order support`).
- **Body:**
  - Brief summary of changes and design decisions.
  - Linked issue reference: `Closes #<issue-number>`.
  - Confirmation of passing checks (`gofmt`, `go test ./...`, `go build ./cmd/api`).

---

## 7. Security & API Hygiene

- **Session Security:** Cookies must be `HttpOnly` and set with appropriate `SameSite` settings. Secure flag is enabled via `AUTH_COOKIE_SECURE=true` in production environments.
- **Sensitive Data:** Never return password hashes, Google tokens, or internal session tokens in API JSON responses. Models representing internal auth state must have `json:"-"` on secret attributes.
- **ID Parsing:** Always validate URL path identifiers using `strconv.ParseUint(c.Param("id"), 10, 32)`. Reject invalid IDs immediately with `400 Bad Request`.
- **Avanza Upstream Scraping:** The Avanza integration simulates browser headers (`User-Agent`, `Referer`, `Origin`). Rate limiting (`middleware.RateLimit()`) must always remain active on `/stocks` routes to prevent IP bans or throttling.
