# NivWest Backend — Development & Agent Workflow (`doc/workflow.md`)

## 1. Step-by-Step Recipe: Implementing a New Feature

When tasked with adding a new entity or endpoint, follow this sequence:

1. **Model Definition (`internal/models/models.go`):**
   - Define the struct with proper GORM and JSON tags.
2. **Migration Registration (`internal/integrations/database/migrate.go`):**
   - Add `&models.NewEntity{}` to `db.AutoMigrate(...)`.
3. **Domain Contracts & DTOs (`internal/domain/`):**
   - Add request/response DTOs and domain repository interface in `internal/domain/repo.go`.
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
   - Return clean JSON payloads with appropriate HTTP status codes.
7. **Application Wiring:**
   - In `internal/app/app.go`: Instantiate repository, service, handler, and assign to `App` struct.
   - In `cmd/api/main.go`: Pass `app.NewEntityHandler` into `mountHandlers(...)`.
8. **Verification:**
   - Format: `gofmt -w cmd internal`
   - Test: `go test ./...`
   - Compile: `go build ./cmd/api`

---

## 2. Testing Guidelines

- Write unit tests alongside packages using the naming convention `*_test.go`.
- Test services and handlers by using the in-memory fakes provided in `internal/testutil` (e.g., `testutil.NewUserRepo()`), rather than mocking frameworks or a live PostgreSQL database.
- Test handlers using `httptest.NewRecorder()` and Gin's test context (`gin.SetMode(gin.TestMode)`).
- Always verify that `go test ./...` passes before completing any task.

---

## 3. Git Workflow (GitFlow)

The full rules live in the workspace repo: [`NivWest/platform` → `docs/git-workflow.md`](https://github.com/NivWest/platform/blob/main/docs/git-workflow.md). In short:

### 3.1. Branches
| Branch | From | Into | Merge method |
|---|---|---|---|
| `feature/<id>-<slug>` | `dev` | `dev` | squash |
| `bugfix/<id>-<slug>` | `dev` | `dev` | squash |
| `release/X.Y.Z` | `dev` | `main` (+ back-merge into `dev`) | merge commit |
| `hotfix/X.Y.Z` | `main` | `main` (+ back-merge into `dev`) | merge commit |

`<id>` is the GitHub Project issue number when there is one. `main` is production; every merge into it is tagged `vX.Y.Z` by CI.

### 3.2. Commit Messages & PR Titles (Conventional Commits)
`type(scope): summary`, with `type` one of `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`, `ci`, `chore`. Breaking changes use `!` or a `BREAKING CHANGE:` footer.

Allowed scopes: `auth`, `order`, `portfolio`, `stock`, `user`, `watchlist`, `config`, `db`, `api`.

Feature PRs are squash-merged, so the **PR title becomes the commit on `dev`** and must be a Conventional Commit (CI checks it).

### 3.3. Pull Requests
- Opened and merged by humans. AI agents (the `/story` workflow in the platform workspace) only push `feature/*` branches.
- **Body:** summary and design decisions, the acceptance criteria as a checklist, and `Closes NivWest/platform#<id>` when there is an issue.
- CI must pass: `gofmt`, `go vet ./...`, `go test ./...`, `go build ./cmd/api`.
