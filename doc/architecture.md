# NivWest Backend — Architecture & Design (`doc/architecture.md`)

## 1. Project Overview & Tech Stack

NivWest Backend is a Go API service powering the NivWest stock trading simulator. It serves market data through a provider port (Avanza or a deterministic simulated market), manages simulator accounts, tracks virtual portfolios with market frictions (simulated execution latency and volume slippage), handles watchlists, and authenticates users via Google OAuth 2.0 (PKCE) and opaque PostgreSQL-backed sessions.

- **Language / Runtime:** Go 1.25 (`module backend`)
- **HTTP Web Framework:** Gin (`github.com/gin-gonic/gin`)
- **ORM / Database:** GORM (`gorm.io/gorm`) with PostgreSQL 16 driver
- **Authentication:** Google OAuth 2.0 with PKCE, secure HTTP-only cookie sessions
- **Rate Limiting:** `golang.org/x/time/rate` (in-memory per-IP token bucket)
- **External Integration:** Avanza public web API (`https://www.avanza.se`), or the in-process simulated market (`MARKET_DATA_PROVIDER`)
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
(GORM / Postgres)          (market data adapters, DB pooling)
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
| `internal/integrations/avanza/`, `internal/integrations/simmarket/` | Market data adapters implementing `domain.MarketDataProvider` (see 3.8). | `internal/domain` (to implement the port and return its DTOs/errors), stdlib | Gin, GORM, services. |
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
- **Order execution** (`orderRepo.Execute`, `internal/repository/order.go`, story NW-04) runs as **one** transaction with a fixed lock order, so orders of one user serialize and never deadlock:
  1. lock the user row (`SELECT … FOR UPDATE`; missing → `domain.ErrUserNotFound`);
  2. lock the position for `(user_id, stock_id)` (it may not exist yet);
  3. if an `Idempotency-Key` was sent, look up the user's order with that key; when it exists, return it with `created=false` and change nothing. Because step 1 serializes the user, a concurrent retry waits for the first order and then finds it here. A unique violation on `idx_orders_user_idempotency_key` (SQLSTATE 23505) is the last line of defence: the transaction rolls back and the existing order is returned;
  4. BUY: `balance < total` → `domain.ErrInsufficientFunds`; debit the total, add the shares; `avg_price` stays the weighted average **fill** price (rounded half-up to 2 decimals), while `avg_cost` becomes the cost basis **including commission**, `(avg_cost × old qty + total) / new qty` rounded half-up to 8 decimals (`domain.NextAvgCost`, `internal/domain/pnl.go`, story NW-05). SELL: no position or too few shares → `domain.ErrInsufficientShares`; credit the total, add `domain.RealizedPnL(total, avg_cost, qty)` (`total − avg_cost × qty`, 2 decimals) to `users.realized_pnl`, leave `avg_price` and `avg_cost` unchanged, delete the position at zero;
  5. update only the `balance` column and, on a SELL, `realized_pnl` of the user in one `UPDATE` (never `Save` the whole row), insert a `FILLED` `models.Order`, return it as a `domain.Order`.
- Domain errors leave the repository unwrapped (or wrapped with `%w`) so services and handlers can use `errors.Is`.
- Stocks are created race-safely with `INSERT … ON CONFLICT (orderbook_id) DO NOTHING` followed by an unscoped re-select (`EnsureStock`), so concurrent first orders for one instrument share one row.
- Behaviour that needs real Postgres (locks, unique indexes, migrations) is covered by `internal/repository/order_integration_test.go` (build tag `integration`, `TEST_DATABASE_URL`; skipped by `go test ./...`).
- **Order history** (`GET /orders`, story NW-05) is keyset-paginated, never `OFFSET`: `OrderRepository.ListByUser` (`internal/repository/order_list.go`) reads the user's orders by `(created_at, id)` descending over `idx_orders_user_created_id`, and from a cursor only rows with `(created_at, id) < (cursor)`. `OrderService.ListOrders` asks for `limit + 1` rows to know whether a `next_cursor` exists. The cursor is opaque base64url JSON (`{"c": created_at RFC3339Nano UTC, "i": id}`), encoded and decoded only in `internal/services/order_cursor.go`; a malformed one is a 400 `VALIDATION_ERROR`. `limit` is 1–100 (`domain.MaxOrderPageLimit`), default 20 (`domain.DefaultOrderPageLimit`). The response is `{items, next_cursor}`, `next_cursor` null on the last page.

### 3.4a. Watchlists (story NW-06)
- `domain.WatchlistRepository` (`internal/repository/watchlist.go`) is keyed by `orderbook_id` and **every query is scoped by `user_id`**: another user's list id is indistinguishable from a missing one (`ErrWatchlistNotFound`), never a 403.
- At most `domain.MaxWatchlists` (10) lists per user: `Create` counts them in a transaction that locks the user row, so concurrent creates cannot exceed the limit. Names are trimmed and 1–50 characters (`WatchlistService`).
- `watchlist_items` has the unique index `idx_watchlist_items_watchlist_stock (watchlist_id, stock_id)`; `AddItem` inserts with `ON CONFLICT DO NOTHING` and reports zero affected rows as `ErrDuplicateItem`. `migrate.go` deletes legacy duplicates (keeping the oldest) before AutoMigrate creates the index. Stocks come from the shared race-safe `ensureStockRow`.
- `AddItem` checks ownership before the instrument, so another user's list never reveals whether an instrument exists.
- Reads price every item from the live quote (each distinct orderbook id once, at most 8 in parallel); a failed quote leaves `last_price`/`change_percent` null instead of failing the read. Covered by `internal/repository/watchlist_integration_test.go` (tag `integration`).

### 3.5. Concurrent Quote Fetching
- Run parallel fetches in goroutines using `sync.WaitGroup`.
- Guard slice appends and shared data mutations with `sync.Mutex`, or let each goroutine write only its own pre-sized slice element.
- Include fallback logic (e.g., if a live quote fails, value the position at its `avg_cost`).
- **Portfolio** (`GET /portfolio`, `PortfolioService.GetPortfolio`, story NW-05; replaces `/portfolio/dashboard`): quotes every position concurrently by `orderbook_id`; a failed quote or one without a positive last price values the position at `avg_cost` with a day change of 0. All math is `decimal` (cost = `avg_cost × qty`, so unrealized P&L includes the buy commission) and rounds half-up only when the `domain.Portfolio` DTO is filled. It returns `cash`, `holdings_value`, `total_equity`, `unrealized_pnl`, `realized_pnl` (from `users.realized_pnl`) and `positions` sorted by market value desc, then `orderbook_id` asc; `top_movers` holds at most 5 (`domain.MaxTopMovers`) sorted by day change desc, then `orderbook_id`.

### 3.5a. Equity History & Risk Analytics (story NW-10)
- `models.EquitySnapshot` (`equity_snapshots`, index `idx_equity_snapshots_user_t (user_id, t)`) stores `cash`, `holdings_value`, `vault_value` (0 until NW-11) and `equity` per user and instant. `domain.SnapshotRepository` (`internal/repository/snapshot.go`): `InsertBatch` (`CreateInBatches`, 500 rows), `ListByUser(since)` (oldest first), `UserIDsWithOrders` (non-deleted users). Guest purges delete snapshots too.
- `services.SnapshotService` values a user exactly like `GET /portfolio` (shared `fetchPositionQuote`: last rounded to 2 decimals, `avg_cost` when the quote fails; `equity = round2(cash + holdings + vault)`), so the snapshot right after a fill equals `total_equity`.
  - **After every fill:** it is the `domain.FillObserver` of `OrderService` (`SetFillObserver`), called only for a newly created order (not for idempotent replays or rejections). It runs synchronously on a detached context (`context.WithoutCancel`, 5 s timeout) and only logs failures: the fill is already committed. A future matching engine (NW-08) must call the same observer.
  - **Periodic:** `runSnapshotScheduler` (`internal/app/snapshots.go`) calls `SnapshotAll` on every tick of `SNAPSHOT_INTERVAL` (default `1h`; the ticker is injected for tests, nothing runs at start-up). `SnapshotAll` values every user with an order at one shared instant with 8 workers, quotes each instrument once per round, skips (and logs) users it cannot value, and writes one batched insert.
- `internal/analytics` is a pure package (no I/O, float64 statistics on fractions): `DailySeries` (last point per UTC day), `DailyReturns`, `AnnualizedVolatility` (sample stdev × √252), `Sharpe` ((mean − rf/252) / stdev × √252; undefined for a zero stdev), `MaxDrawdown`, `TotalReturn`. Golden tests in `metrics_test.go` pin the results of a fixed series.
- `services.AnalyticsService` + `handlers.AnalyticsHandler` (session required):
  - `GET /portfolio/history?range=1d|1w|1m|all` (default `1d`; anything else, an empty value included, is 400 `VALIDATION_ERROR`): `1d` raw snapshots of the last 24 h, `1w`/`1m` the last snapshot per UTC hour of the last 7/30 days, `all` the last per UTC day.
  - `GET /portfolio/analytics`: `total_return_pct` from the live `total_equity` and `domain.StartingEquity` (100,000); volatility, Sharpe (`RISK_FREE_RATE`, default `0.02`) and max drawdown over the daily series once `data_points` (daily returns) ≥ 5 (`analytics.MinDailyReturns`), otherwise null with `sufficient_data: false`; `allocation` = positions (label = symbol, largest first) then `Cash`, weights in percent of total equity summing to exactly 100 (the rounding remainder goes to the largest slice). Results are rounded half-up to 2 decimals.

### 3.5b. Risk-adjusted Leaderboard (story NW-13)
- **Profile Opt-In:** `models.User` gains `display_name` (indexed via case-insensitive unique expression `lower(display_name)`), `leaderboard_opt_in` (bool), and `opted_in_at` (*time.Time). `PATCH /api/v1/me` (`UserHandler.UpdateProfile`) validates display names (3–20 characters, `^[A-Za-z0-9_-]{3,20}$`) and sets `opted_in_at` when newly opting in. Conflicts return 409 `CONFLICT`.
- **Rankings Materialization:** Materialized atomically per metric in `leaderboard_entries` table (`LeaderboardRepository.RebuildMetric` swaps old entries for new in a single transaction).
  - Background leader refresh job runs every `LEADERBOARD_REFRESH` (default `5m`, compose `5s`), coordinated across cluster replicas via a PostgreSQL session advisory lock (`LeaderboardLockKey = 0x4e573133`).
  - `metric=return`: Ranks all opted-in users by `total_return_pct` descending.
  - `metric=sharpe`: Ranks only opted-in users with `sufficient_data` (at least 5 daily returns from NW-10 snapshot analytics) by `sharpe_ratio` descending.
  - Ties break deterministically by earlier `opted_in_at` ascending, then `user_id` ascending.
- **Serving Leaderboards:** `GET /api/v1/leaderboard?metric=return|sharpe&limit=50` (`LeaderboardHandler.GetLeaderboard`) serves ranked entries. If authenticated and caller is opted-in but outside the top page, caller's entry is appended to the response items. Entries strictly omit PII, exposing only `rank`, `display_name`, `total_return_pct`, `sharpe_ratio`, `max_drawdown_pct`, and `is_me`.

### 3.6. Error Handling & HTTP Status Codes
- Define domain-specific errors in `internal/domain/errors.go`.
- Repositories translate `gorm.ErrRecordNotFound` to domain errors.
- Handlers map them to HTTP status codes (400, 401, 403, 404, 409, 422, 429, 500).
- Standard JSON error format (`domain.ErrorResponse`, `api/openapi.yaml#/components/schemas/Error`): `{"error": "description of the error", "code": "MACHINE_CODE"}`, on every error, the OAuth login/callback errors included. Codes live next to their errors (`internal/domain/auth.go`, `market.go`, `orders.go`, `watchlist.go`); a new `Code*` constant must also be added to the spec's `ErrorCode` enum (the parity test fails otherwise).
- Watchlist errors (`internal/domain/watchlist.go`, story NW-06): `ErrWatchlistNotFound` → 404 `WATCHLIST_NOT_FOUND` (also for another user's list), `ErrWatchlistItemNotFound` → 404 `WATCHLIST_ITEM_NOT_FOUND`, `ErrDuplicateItem` → 409 `CONFLICT`, `ErrWatchlistLimit` → 422 `WATCHLIST_LIMIT_REACHED`.
- Order errors: `ErrInsufficientFunds` → 422 `INSUFFICIENT_FUNDS`, `ErrInsufficientShares` → 422 `INSUFFICIENT_SHARES`, `ErrIdempotencyKeyReused` → 409 `CONFLICT`; invalid requests (`*ValidationError`) → 400 `VALIDATION_ERROR`, unknown instruments → 404 `INSTRUMENT_NOT_FOUND`.

### 3.7. GORM Models & Automatic Migrations
- Use explicit GORM tags (`primaryKey`, `uniqueIndex`, `type:numeric(18,2)`, `type:numeric(18,4)`).
- **Money is `decimal.Decimal`** (`github.com/shopspring/decimal`), never `float64`: cash and prices are `numeric(18,2)`, share quantities `numeric(18,4)`. All arithmetic stays in `decimal`; values are rounded half-up to 2 decimals only at the edges (pricing, average price, JSON via `domain.MoneyFloat` / `domain.Order.MarshalJSON`).
- Stocks are identified by `orderbook_id` (unique); `symbol` is only indexed for lookup, since symbols repeat across markets.
- `models.Order` is the order log. `models.Transaction` is the pre-NW-04 trade log: it is still migrated (old rows survive) and purged with guests, but **no longer written**.
- Use `gorm.DeletedAt` for soft-deletable entities.
- Mark sensitive fields with `json:"-"`.
- **Auto-migration:** Every new model **MUST** be added to `internal/integrations/database/migrate.go`. When a change cannot be expressed by AutoMigrate alone (e.g. backfilling a new NOT NULL column, dropping an old index), add idempotent SQL in `migrate.go` around the AutoMigrate call, as done for `stocks.orderbook_id`.
- `positions.avg_cost` (`numeric(20,8)`) and `users.realized_pnl` (`numeric(18,2)`) arrive with default 0 (story NW-05). `backfillAvgCost` then sets `avg_cost = avg_price` where `avg_cost = 0`: the buy commission of legacy lots was never recorded, so the average fill price is their best available cost basis. It is idempotent, since every NW-05 BUY writes a positive `avg_cost`.

### 3.8. Market Data Port
- `domain.MarketDataProvider` (`internal/domain/market.go`) is the port for market data: `Search`, `Quote`, `Details`, `Candles`. It speaks only domain DTOs (`InstrumentSummary`, `Quote`, `InstrumentDetails`, `Candle`) and errors (`ErrInstrumentNotFound` → 404, `ErrMarketDataUnavailable` → 502, `*ValidationError` → 400). No upstream JSON leaves an adapter.
- Adapters live in `internal/integrations/` and may import `internal/domain` to implement the port:
  - `internal/integrations/avanza` (`avanza.NewMarketProvider`): the Avanza public web API (`AVANZA_BASE_URL`, `AVANZA_TIMEOUT`).
  - `internal/integrations/simmarket` (`simmarket.New`): twelve fictional `SIM-*` instruments on a seeded, deterministic price path. No network. It also implements `domain.MarketSimControl` (pin/clear a price).
- `internal/app` picks the adapter from `MARKET_DATA_PROVIDER` (`avanza` by default, or `simulated`) and builds **one** instance, shared by `services.MarketService` and the handlers. The simulated market caches its daily aggregates per instance and is warmed in a background goroutine at startup. The active provider is logged at startup and reported as `market_data_provider` by `GET /health`.
- `handlers.MarketHandler` serves the public `/market/instruments…` routes (no session, rate-limited).
- **Dev/test only:** `handlers.SimHandler` (`PUT`/`DELETE /sim/instruments/{orderbook_id}/price`) is mounted only when `MARKET_DATA_PROVIDER=simulated` **and** `MARKET_SIM_CONTROL_ENABLED=true`; `GET /health` then reports `features.sim_control: true`. It has no auth and no rate limit, and must never be enabled in production. With the Avanza provider the flag is ignored (with a startup warning).

### 3.9. API Contract (story NW-07)
- `api/openapi.yaml` (OpenAPI 3.1, `info.title: TradeEdu API`) is the enforced source of truth. The `backend/api` package embeds it with `go:embed` (`api.OpenAPISpec`); `handlers.OpenAPIHandler` serves it at `GET /api/v1/openapi.yaml` (`application/yaml`) and renders it at `GET /api/docs` with a pinned Scalar build (`scalarVersion` + SRI hash in `openapi_handler.go`, strict CSP). Both are public.
- The router lives in `internal/app/router.go` (`App.Router`): `/api/v1` gets every non-nil handler, `/api` gets the docs page. `cmd/api` serves exactly this engine.
- Enforced by `go test ./...`:
  - `internal/handlers/openapi_parity_test.go` builds `App.Router` from `internal/testutil` fakes with guest login and sim control on, and fails when a mounted route is not in the spec, a documented operation has no route, or a route outside `/api/v1` is not allow-listed (`/api/docs` only). It also requires every documented error response (status ≥ 400 with a body) to use `#/components/schemas/Error`, and the `ErrorCode` enum to equal the `Code*` string constants in `internal/domain` (scanned with `go/parser`).
  - `api/spec_test.go`: the spec parses as OpenAPI 3.1 and `info.version` equals `VERSION` (bump both on a release).
- `make api-lint` runs Redocly (`redocly.yaml`, recommended rules) on the spec. It needs network for `npx`, so it runs in CI (job `api-contract`), not in the offline fast checks.
- Changing the API: edit `api/openapi.yaml` first (new path, schemas, error codes in `ErrorCode`), then the code; public operations say `security: []`. The frontend vendors the file and regenerates its types (`npm run api:sync && npm run api:gen`).

---

## 4. Security & API Hygiene

- **Session Security:** Cookies must be `HttpOnly`. Secure flag enabled via `AUTH_COOKIE_SECURE=true`.
- **Sensitive Data:** Never return internal session tokens or password hashes in API JSON responses.
- **ID Parsing:** Always validate URL path identifiers using `strconv.ParseUint`.
- **Avanza Upstream Scraping:** Rate limiting must always remain active on `/market` routes to prevent IP bans.
- **Sim Control:** The `/sim` routes are unauthenticated test hooks; never set `MARKET_SIM_CONTROL_ENABLED=true` in production.

