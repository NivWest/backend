# NivWest Backend — Architecture & Design (`doc/architecture.md`)

## 1. Project Overview & Tech Stack

NivWest Backend is a Go API service powering the NivWest stock trading simulator. It proxies market data from Avanza, manages simulator accounts, tracks virtual portfolios with market frictions (simulated execution latency and volume slippage), handles watchlists, and authenticates users via Google OAuth 2.0 (PKCE) and opaque PostgreSQL-backed sessions.

- **Language / Runtime:** Go 1.25 (`module backend`)
- **HTTP Web Framework:** Gin (`github.com/gin-gonic/gin`)
- **ORM / Database:** GORM (`gorm.io/gorm`) with PostgreSQL 16 driver
- **Authentication:** Google OAuth 2.0 with PKCE, secure HTTP-only cookie sessions
- **Rate Limiting:** `golang.org/x/time/rate` (in-memory per-IP token bucket)
- **External Integration:** Avanza public web API (`https://www.avanza.se`)
- **Containerization:** Multi-stage Dockerfile, Docker Compose

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
| `cmd/api/` | Server entry point, config loading, graceful shutdown. | `internal/app`, `internal/config`, `gin` | Business logic, DB calls. |
| `internal/app/` | Dependency injection container (`NewApp`). | All `internal/*` | HTTP request handling, business logic. |
| `internal/config/` | Environment variables parsing. | `godotenv`, stdlib | Database, services, handlers. |
| `internal/domain/` | Core interfaces, DTOs, domain errors. | `internal/models`, stdlib | GORM queries, Gin context. |
| `internal/models/` | GORM entity definitions, table schemas. | `gorm`, stdlib | Business logic, handlers, services. |
| `internal/integrations/` | Low-level external clients. | `internal/config`, `internal/models`, `gorm`, stdlib | Gin handlers, domain services. |
| `internal/repository/` | Concrete implementations using GORM. | `internal/domain`, `internal/models`, `gorm` | Gin context, HTTP status codes. |
| `internal/services/` | Business rules, orchestration. | `internal/domain`, `internal/models`, `internal/config`, stdlib | Gin context, GORM access. |
| `internal/handlers/` | HTTP request binding, status codes, JSON responses. | `internal/domain`, `internal/services`, `internal/middleware`, `internal/models`, `gin` | GORM queries, direct DB access. |
| `internal/middleware/` | Cross-cutting HTTP middleware. | `internal/domain`, `internal/models`, `gin` | Heavy DB writes, business logic. |

---

## 3. Core Design Patterns & Invariants

### 3.1. Handlers and the `Registrar` Interface
All handlers must implement:
```go
type Registrar interface {
    Register(v1 *gin.RouterGroup)
}
```
- Handlers mount their sub-routes on `v1` (prefixed `/api/v1`).
- Handlers **MUST NEVER** access GORM directly. They only interact with Application Services or `domain.AuthRepository`.

### 3.2. Interfaces & Dependency Inversion
- Services must depend on **Domain Interfaces** (`domain.OrderRepository`, etc.), never concrete GORM repository structs.
- Repositories return domain models or domain errors.
- Dependencies are injected via constructor functions (`New<Component>`).

### 3.3. Context Propagation
- All repository methods, service methods, and external HTTP calls **MUST** accept `context.Context` as their first parameter.
- Use `r.db.WithContext(ctx)` in GORM.

### 3.4. Database Concurrency & Financial Transactions
- Any operation altering balances, positions, or transactions **MUST** execute inside a GORM transaction:
  ```go
  r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { ... })
  ```
- Use row-level locking (`clause.Locking{Strength: "UPDATE"}`) when reading records that will be updated in the same transaction.
- Always verify balance or quantity sufficiency before deducting. Return clear errors to trigger automatic rollback.

### 3.5. Concurrent Stock Quote Fetching
- Run parallel fetches in goroutines using `sync.WaitGroup`.
- Guard slice appends and shared data mutations with `sync.Mutex`.
- Include fallback logic (e.g., if live quote fails, fall back to `AvgPrice`).

### 3.6. Error Handling & HTTP Status Codes
- Define domain-specific errors in `internal/domain/errors.go`.
- Repositories translate `gorm.ErrRecordNotFound` to domain errors.
- Handlers map them to HTTP status codes (400, 401, 403, 404, 409, 429, 500).
- Standard JSON error format: `{"error": "description of the error"}`

### 3.7. GORM Models & Automatic Migrations
- Use explicit GORM tags (`primaryKey`, `uniqueIndex`, `numeric(15,2)`, `numeric(15,4)`).
- Use `gorm.DeletedAt` for soft-deletable entities.
- Mark sensitive fields with `json:"-"`.
- **Auto-migration:** Every new model **MUST** be added to `internal/integrations/database/migrate.go`.

---

## 4. Security & API Hygiene

- **Session Security:** Cookies must be `HttpOnly`. Secure flag enabled via `AUTH_COOKIE_SECURE=true`.
- **Sensitive Data:** Never return internal session tokens or password hashes in API JSON responses.
- **ID Parsing:** Always validate URL path identifiers using `strconv.ParseUint`.
- **Avanza Upstream Scraping:** Rate limiting must always remain active on `/stocks` routes to prevent IP bans.

