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

## 3. Git & Agent Workflow Conventions

Automated agents (e.g. Jules, CI bots) and contributors must follow these Git standards:

### 3.1. Branch Naming
Branch directly from `dev` (`main` is the release branch):
- Bug fixes: `jules/fix/<issue-number>-<short-slug>` or `fix/<short-slug>`
- Features: `jules/feat/<issue-number>-<short-slug>` or `feat/<short-slug>`
- Refactors: `jules/refactor/<issue-number>-<short-slug>` or `refactor/<short-slug>`
- Chores & Docs: `jules/chore/<issue-number>-<short-slug>` or `docs/<short-slug>`

### 3.2. Commit Messages (Conventional Commits)
Use atomic commits with standard Conventional Commit prefixes:
- `feat(scope): ...`
- `fix(scope): ...`
- `refactor(scope): ...`
- `test(scope): ...`
- `docs(scope): ...`
- `chore(scope): ...`

Allowed scopes: `auth`, `order`, `portfolio`, `stock`, `user`, `watchlist`, `config`, `db`, `api`.

### 3.3. Pull Request Requirements
- **Title:** Conventional commit format matching the primary change.
- **Body:**
  - Brief summary of changes and design decisions.
  - Linked issue reference: `Closes #<issue-number>`.
  - Confirmation of passing checks (`gofmt`, `go test ./...`, `go build ./cmd/api`).


---

## Agent Lifecycle (Jira → GitHub → Jules)

1. **Jira ticket → GitHub issue.** A new or reassigned Jira ticket is mirrored as an issue titled `<KEY>: <summary>` with the `jira` label, assigned to the ticket owner (`.github/workflows/jira-sync.yml`).
2. **Breakdown.** The owner splits the issue into **sub-issues** with the **Agent task** form (goal, target paths, acceptance criteria, verification, out of scope).
3. **Delegation.** Adding the `agent-delegate` label to a sub-issue checks the Definition of Ready and hands the task to Jules (`.github/workflows/agent-implement.yml`). If the task is not ready, a bot comment lists what is missing and the label is removed.
4. **Pull request.** Jules opens one PR against `dev`, titled `<KEY> <type>(<scope>): …` and containing `Closes #<sub-issue>`. CI must pass, and a human approves every merge.
5. **Remediation.** Greptile reviews Jules PRs, and Jules may address the findings at most twice (`agent-fix:1`, `agent-fix:2`). After that the PR is labelled `needs-human`.
6. **Done.** When the parent issue is closed as completed, the Jira ticket moves to Done.
