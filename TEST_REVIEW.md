# Test
Testing claude-runner per-repo token.

## Refs feature (2026-05-31)

- **Store**: `internal/ticket/refs_store_test.go` (10 tests, real Postgres via `GOQUEST_TEST_DATABASE_URL` — CASCADE, append position, reorder, conflict, batch load).
- **Service**: `internal/ticket/refs_service_test.go` (10 tests — 50-cap, dup rejection, empty/long id rejection, Create/Update integration, visibility filter, replace semantics).
- **HTTP**: `internal/handler/refs_test.go` (12 tests — 4 ticket-scoped endpoints + reverse lookup + audit).
- **E2E**: `internal/handler/refs_e2e_test.go` (4 tests — full lifecycle, authz separation from gopedia, no cross-user leak, org-axis regression smoke).
- **Audit**: ref add/remove records via existing `audit.Logger` (in-memory fake in test, real PG in prod).

**Out of scope (other repos)**: bot fetch from gopedia, gopedia-side UI/route changes.
