# goquest REST Agent-Friendly Enhancements — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make goquest's REST API friendly for AI agents — name-based tags (get-or-create), idempotent ticket creation by `title_name`, name-based query/filter + a `by-name` lookup route, and a hand-written ticket-focused OpenAPI spec served over HTTP.

**Architecture:** Additive, backward-compatible changes layered onto the existing `internal/ticket`, `internal/tag`, `internal/resolve`, and `internal/handler` packages. New request fields and query params are optional; existing `tag_ids`/UUID callers are unaffected. Name→ID resolution follows the established `resolve.Resolver`/`ProjectResolver` pattern. Phase 2 (goquest-mcp, separate repo) consumes the OpenAPI contract produced here.

**Tech Stack:** Go, chi router (`github.com/go-chi/chi/v5`), pgx v5 (`github.com/jackc/pgx/v5`), Postgres. Tests are standard `testing` with in-package mocks (no external test DB required for unit tests; pgx integration tests run only when a harness is present).

## Global Constraints

- All new API inputs are **optional** — existing `project_id` / `tag_ids` (UUID) callers must keep working unchanged.
- `title_name` format: lowercase, hyphen-separated, validated by the existing `titleNameRe` regex (e.g. `goq-vault-portforward`). Do not change this regex.
- Tag names resolve **case-insensitively** within a workspace (`LOWER(name)`).
- `title_name` uniqueness is **per project** and only applies when `project_id IS NOT NULL` (existing partial index `tickets_project_title_name_key`).
- Auth model is unchanged: `X-API-Key` (project-scoped). Do not add or alter middleware.
- Migrations are plain `.sql` files in `internal/migrate/sql/`, applied in lexical order by a runner that **logs but does not abort** on error — so any new unique index needs the pre-check documented in Task 1.
- Follow existing code idioms: `writeJSON(w, status, v)` / `writeError(w, status, msg)` helpers in `internal/handler/ticket.go`; sentinel errors via `errors.New`; unique-violation detection via `isUniqueViolation(err, constraintName)`.

---

## File Structure

**Created:**
- `internal/migrate/sql/017_tag_name_unique.sql` — workspace-scoped unique index on tag name.
- `internal/resolve/tags.go` — `TagResolver`: project_id → workspace_id → tag get-or-create.
- `internal/resolve/tags_test.go` — unit tests for `TagResolver`.
- `api/openapi.yaml` — hand-written ticket-focused OpenAPI 3.1 spec.
- `internal/handler/openapi.go` — serves the embedded spec at `/api/v1/openapi.json` and `/api/v1/openapi.yaml`.
- `internal/handler/openapi_test.go` — tests the serving endpoints.

**Modified:**
- `internal/tag/store.go` — add `GetByName` to the `Store` interface.
- `internal/tag/store_pg.go` — implement `GetByName`.
- `internal/tag/service.go` — add `GetOrCreate`.
- `internal/tag/service_test.go` (create if absent) — `GetOrCreate` unit tests with a mock store.
- `internal/ticket/model.go` — add `TagNames []string` to `CreateTicketReq`; add `TitleName *string` and `TagNames []string` to `ListTicketsFilter`.
- `internal/ticket/store.go` — add `GetByProjectAndTitleName` to the `Store` interface.
- `internal/ticket/store_pg.go` — implement `GetByProjectAndTitleName`; add `title_name` + tag filtering to `List`.
- `internal/ticket/service.go` — add `GetByProjectAndTitleName` passthrough.
- `internal/handler/ticket.go` — add `TagResolver` field + interface; resolve `tag_names` in `Create`; idempotent-replay on duplicate `title_name`; new query params in `List`; new `GetByName` handler.
- `internal/handler/ticket_test.go`, `internal/handler/authz_e2e_test.go` — update `NewTicketHandler(...)` call sites (pass `nil` for the new resolver).
- `cmd/service/main.go` — construct `resolve.TagResolver`, pass to `NewTicketHandler`; register `GET /tickets/by-name/{title_name}` and the openapi routes.

---

## Task 1: Migration — workspace-scoped unique tag name

**Files:**
- Create: `internal/migrate/sql/017_tag_name_unique.sql`

**Interfaces:**
- Consumes: existing `tags` table (`id, workspace_id, name, color, created_by, created_at`).
- Produces: unique index `tags_workspace_lower_name_key ON tags (workspace_id, LOWER(name))` relied on by Task 2's get-or-create race handling.

- [ ] **Step 1: Write the migration file**

Create `internal/migrate/sql/017_tag_name_unique.sql`:

```sql
-- Workspace-scoped, case-insensitive uniqueness for tag names.
-- Enables get-or-create by name (see internal/tag GetOrCreate) and guards
-- against duplicate tags differing only by case within one workspace.
--
-- PRE-CHECK (run manually against the cluster DB before deploying — the
-- migration runner only logs failures, it does not abort):
--   SELECT workspace_id, LOWER(name), COUNT(*)
--   FROM tags GROUP BY 1,2 HAVING COUNT(*) > 1;
-- Resolve any duplicates (rename/merge) first, otherwise this index is
-- silently not created. After deploy verify with: \d tags
CREATE UNIQUE INDEX IF NOT EXISTS tags_workspace_lower_name_key
    ON tags (workspace_id, LOWER(name));
```

- [ ] **Step 2: Verify the file is picked up in lexical order**

Run: `ls internal/migrate/sql/ | sort | tail -3`
Expected: `016_cancellation_note.sql`, then `017_tag_name_unique.sql` last.

- [ ] **Step 3: Build to confirm nothing else references migration numbering**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/migrate/sql/017_tag_name_unique.sql
git commit -m "feat(migrate): workspace-scoped unique tag name index (017)"
```

---

## Task 2: Tag get-or-create (store + service)

**Files:**
- Modify: `internal/tag/store.go`
- Modify: `internal/tag/store_pg.go`
- Modify: `internal/tag/service.go`
- Test: `internal/tag/service_test.go` (create if absent)

**Interfaces:**
- Consumes: `tag.Store` (existing `Create`, `Get`, `ListByWorkspace`), `tag.ErrNotFound`, the unique index from Task 1.
- Produces:
  - `Store.GetByName(ctx context.Context, workspaceID uuid.UUID, name string) (*Tag, error)` — returns `ErrNotFound` when absent.
  - `Service.GetOrCreate(ctx context.Context, workspaceID uuid.UUID, creatorID, name string) (*Tag, error)` — used by `resolve.TagResolver` in Task 3.

- [ ] **Step 1: Write the failing test**

Create `internal/tag/service_test.go`:

```go
package tag

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// mockStore implements Store for unit tests.
type mockStore struct {
	tags        map[uuid.UUID]*Tag
	createCalls int
	// failCreateOnce: simulate a concurrent insert losing the race (unique
	// violation) the first time Create is called, then succeed-by-existing.
	failCreateOnce bool
}

func newMockStore() *mockStore { return &mockStore{tags: map[uuid.UUID]*Tag{}} }

func (m *mockStore) Create(ctx context.Context, t *Tag) error {
	m.createCalls++
	if m.failCreateOnce {
		m.failCreateOnce = false
		// Simulate the row already existing (racer committed first).
		existing := &Tag{ID: uuid.New(), WorkspaceID: t.WorkspaceID, Name: t.Name}
		m.tags[existing.ID] = existing
		return errors.New("ERROR: duplicate key value violates unique constraint \"tags_workspace_lower_name_key\" (SQLSTATE 23505)")
	}
	m.tags[t.ID] = t
	return nil
}
func (m *mockStore) Get(ctx context.Context, id uuid.UUID) (*Tag, error) {
	if t, ok := m.tags[id]; ok {
		return t, nil
	}
	return nil, ErrNotFound
}
func (m *mockStore) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Tag, error) {
	return nil, errors.New("not used")
}
func (m *mockStore) Delete(ctx context.Context, id uuid.UUID) error { return nil }
func (m *mockStore) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Tag, error) {
	var out []*Tag
	for _, t := range m.tags {
		if t.WorkspaceID == workspaceID {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *mockStore) GetByName(ctx context.Context, workspaceID uuid.UUID, name string) (*Tag, error) {
	for _, t := range m.tags {
		if t.WorkspaceID == workspaceID && equalFold(t.Name, name) {
			return t, nil
		}
	}
	return nil, ErrNotFound
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func TestGetOrCreate_CreatesWhenMissing(t *testing.T) {
	st := newMockStore()
	svc := NewService(st)
	ws := uuid.New()

	got, err := svc.GetOrCreate(context.Background(), ws, "creator", "infra")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if got.Name != "infra" || got.WorkspaceID != ws {
		t.Fatalf("unexpected tag: %+v", got)
	}
	if st.createCalls != 1 {
		t.Fatalf("expected 1 create call, got %d", st.createCalls)
	}
}

func TestGetOrCreate_ReturnsExistingCaseInsensitive(t *testing.T) {
	st := newMockStore()
	svc := NewService(st)
	ws := uuid.New()
	if _, err := svc.GetOrCreate(context.Background(), ws, "creator", "Infra"); err != nil {
		t.Fatal(err)
	}
	st.createCalls = 0

	got, err := svc.GetOrCreate(context.Background(), ws, "creator", "infra")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if got.Name != "Infra" {
		t.Fatalf("expected existing tag 'Infra', got %q", got.Name)
	}
	if st.createCalls != 0 {
		t.Fatalf("expected no create call, got %d", st.createCalls)
	}
}

func TestGetOrCreate_AbsorbsRace(t *testing.T) {
	st := newMockStore()
	st.failCreateOnce = true
	svc := NewService(st)
	ws := uuid.New()

	got, err := svc.GetOrCreate(context.Background(), ws, "creator", "data")
	if err != nil {
		t.Fatalf("expected race to be absorbed, got error: %v", err)
	}
	if got.Name != "data" {
		t.Fatalf("expected re-read tag 'data', got %q", got.Name)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/tag/ -run TestGetOrCreate -v`
Expected: FAIL — `svc.GetOrCreate undefined` and `GetByName` not in `Store` interface (compile error).

- [ ] **Step 3: Add `GetByName` to the Store interface**

In `internal/tag/store.go`, add to the `Store` interface:

```go
	GetByName(ctx context.Context, workspaceID uuid.UUID, name string) (*Tag, error)
```

- [ ] **Step 4: Implement `GetByName` in the pg store**

In `internal/tag/store_pg.go`, add:

```go
func (s *pgStore) GetByName(ctx context.Context, workspaceID uuid.UUID, name string) (*Tag, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, name, color, created_by, created_at
		FROM tags WHERE workspace_id=$1 AND LOWER(name)=LOWER($2)`, workspaceID, name)
	return scanTag(row)
}
```

- [ ] **Step 5: Implement `GetOrCreate` in the service**

In `internal/tag/service.go`, add (the `errors` package is already imported):

```go
// GetOrCreate returns the workspace tag whose name matches (case-insensitive),
// creating it when absent. A concurrent creator losing the unique-index race
// (tags_workspace_lower_name_key) is absorbed by re-reading the now-present row.
func (svc *Service) GetOrCreate(ctx context.Context, workspaceID uuid.UUID, creatorID, name string) (*Tag, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("tag name is required")
	}
	existing, err := svc.store.GetByName(ctx, workspaceID, name)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	created, cerr := svc.Create(ctx, workspaceID, creatorID, CreateReq{Name: name})
	if cerr == nil {
		return created, nil
	}
	// Lost the race: another creator inserted the same name. Re-read.
	if reread, rerr := svc.store.GetByName(ctx, workspaceID, name); rerr == nil {
		return reread, nil
	}
	return nil, cerr
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/tag/ -run TestGetOrCreate -v`
Expected: PASS (all three tests).

- [ ] **Step 7: Build the whole module**

Run: `go build ./... && go vet ./internal/tag/`
Expected: no output (success).

- [ ] **Step 8: Commit**

```bash
git add internal/tag/store.go internal/tag/store_pg.go internal/tag/service.go internal/tag/service_test.go
git commit -m "feat(tag): GetByName store method + GetOrCreate service"
```

---

## Task 3: Name-based tags on ticket create (`tag_names`)

**Files:**
- Create: `internal/resolve/tags.go`
- Test: `internal/resolve/tags_test.go`
- Modify: `internal/ticket/model.go` (add `TagNames` to `CreateTicketReq`)
- Modify: `internal/handler/ticket.go` (add `TagResolver` interface + field; resolve in `Create`)
- Modify: `cmd/service/main.go` (construct + inject resolver)
- Modify: `internal/handler/ticket_test.go`, `internal/handler/authz_e2e_test.go` (call-site updates)

**Interfaces:**
- Consumes: `tag.Service.GetOrCreate` (Task 2), `project.Store.Get(ctx, id) (*project.Project, error)` (returns `.WorkspaceID`).
- Produces:
  - `resolve.TagResolver` with `ResolveTagIDs(ctx context.Context, projectID uuid.UUID, creatorID string, names []string) ([]uuid.UUID, error)`.
  - `handler.TagResolver` interface (same signature) — a `nil` field means `tag_names` is ignored (mirrors the nil-able `audit` field).
  - `ticket.CreateTicketReq.TagNames []string`.

- [ ] **Step 1: Write the failing test for the resolver**

Create `internal/resolve/tags_test.go`:

```go
package resolve

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/tag"
)

type fakeProjectGetter struct {
	wsID uuid.UUID
	err  error
}

func (f fakeProjectGetter) Get(ctx context.Context, id uuid.UUID) (*project.Project, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &project.Project{ID: id, WorkspaceID: f.wsID}, nil
}

type fakeTagGetOrCreator struct {
	created map[string]uuid.UUID
}

func (f *fakeTagGetOrCreator) GetOrCreate(ctx context.Context, workspaceID uuid.UUID, creatorID, name string) (*tag.Tag, error) {
	if f.created == nil {
		f.created = map[string]uuid.UUID{}
	}
	id, ok := f.created[name]
	if !ok {
		id = uuid.New()
		f.created[name] = id
	}
	return &tag.Tag{ID: id, WorkspaceID: workspaceID, Name: name}, nil
}

func TestResolveTagIDs_DedupAndResolve(t *testing.T) {
	ws := uuid.New()
	r := NewTagResolver(fakeProjectGetter{wsID: ws}, &fakeTagGetOrCreator{})
	ids, err := r.ResolveTagIDs(context.Background(), uuid.New(), "creator", []string{"infra", "infra", "goquest"})
	if err != nil {
		t.Fatalf("ResolveTagIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 deduped ids, got %d", len(ids))
	}
}

func TestResolveTagIDs_Empty(t *testing.T) {
	r := NewTagResolver(fakeProjectGetter{wsID: uuid.New()}, &fakeTagGetOrCreator{})
	ids, err := r.ResolveTagIDs(context.Background(), uuid.New(), "creator", nil)
	if err != nil {
		t.Fatalf("ResolveTagIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected 0 ids, got %d", len(ids))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/resolve/ -run TestResolveTagIDs -v`
Expected: FAIL — `NewTagResolver undefined`.

- [ ] **Step 3: Implement the resolver**

Create `internal/resolve/tags.go`:

```go
package resolve

import (
	"context"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/tag"
)

type projectGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*project.Project, error)
}

type tagGetOrCreator interface {
	GetOrCreate(ctx context.Context, workspaceID uuid.UUID, creatorID, name string) (*tag.Tag, error)
}

// TagResolver turns tag names into tag UUIDs within the workspace that owns a
// project, creating missing tags on demand.
type TagResolver struct {
	pj  projectGetter
	tag tagGetOrCreator
}

func NewTagResolver(pj projectGetter, t tagGetOrCreator) *TagResolver {
	return &TagResolver{pj: pj, tag: t}
}

// ResolveTagIDs resolves names to tag IDs in the project's workspace. Names are
// get-or-created; duplicate names (case-insensitively producing the same tag)
// collapse to a single ID. An empty/nil names slice returns an empty result.
func (r *TagResolver) ResolveTagIDs(ctx context.Context, projectID uuid.UUID, creatorID string, names []string) ([]uuid.UUID, error) {
	if len(names) == 0 {
		return nil, nil
	}
	proj, err := r.pj.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0, len(names))
	for _, name := range names {
		t, terr := r.tag.GetOrCreate(ctx, proj.WorkspaceID, creatorID, name)
		if terr != nil {
			return nil, terr
		}
		if _, dup := seen[t.ID]; dup {
			continue
		}
		seen[t.ID] = struct{}{}
		out = append(out, t.ID)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the resolver test to verify it passes**

Run: `go test ./internal/resolve/ -run TestResolveTagIDs -v`
Expected: PASS.

- [ ] **Step 5: Add `TagNames` to `CreateTicketReq`**

In `internal/ticket/model.go`, in the `CreateTicketReq` struct, directly below the `TagIDs` field add:

```go
	TagNames       []string       `json:"tag_names,omitempty"`
```

- [ ] **Step 6: Add the `TagResolver` interface + field to the handler**

In `internal/handler/ticket.go`, below the `ProjectResolver` interface add:

```go
// TagResolver resolves tag names to tag UUIDs within a project's workspace,
// creating missing tags. A nil TagResolver on the handler means tag_names is
// ignored (parity with the nil-able audit logger).
type TagResolver interface {
	ResolveTagIDs(ctx context.Context, projectID uuid.UUID, creatorID string, names []string) ([]uuid.UUID, error)
}
```

Add a field to the `TicketHandler` struct (below `resolver ProjectResolver`):

```go
	tags     TagResolver
```

Change `NewTicketHandler` to accept and store it. New signature and body:

```go
func NewTicketHandler(svc *ticket.Service, checker spicedb.PermissionChecker, writer spicedb.RelationshipWriter, resolver ProjectResolver, tags TagResolver, auditLogger audit.Logger) *TicketHandler {
	return &TicketHandler{svc: svc, checker: checker, writer: writer, resolver: resolver, tags: tags, audit: auditLogger}
}
```

- [ ] **Step 7: Resolve `tag_names` inside `Create`**

In `internal/handler/ticket.go`, in `Create`, immediately **after** the project-resolution block (after the closing `}` of `if req.ProjectID == nil { ... }`, before `requesterID := auth.RequesterID(r)`), insert:

```go
	requesterID := auth.RequesterID(r)

	if len(req.TagNames) > 0 {
		if h.tags == nil || req.ProjectID == nil {
			writeError(w, http.StatusBadRequest, "tag_names requires a resolvable project")
			return
		}
		tagIDs, terr := h.tags.ResolveTagIDs(r.Context(), *req.ProjectID, requesterID, req.TagNames)
		if terr != nil {
			writeError(w, http.StatusBadRequest, "resolve tag_names: "+terr.Error())
			return
		}
		req.TagIDs = dedupeTagIDs(append(req.TagIDs, tagIDs...))
	}
```

Then **delete** the now-duplicate `requesterID := auth.RequesterID(r)` line that currently sits just before `t, err := h.svc.Create(...)`.

Add this helper near the bottom of `internal/handler/ticket.go` (next to `writeJSON`):

```go
func dedupeTagIDs(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
```

- [ ] **Step 8: Update the existing `NewTicketHandler` call sites in tests to pass `nil`**

In `internal/handler/ticket_test.go` and `internal/handler/authz_e2e_test.go`, find each `NewTicketHandler(...)` call and insert `nil,` for the new `tags TagResolver` parameter — it goes **between** the `resolver` argument and the `auditLogger` argument. For example:

```go
// before
h := handler.NewTicketHandler(svc, checker, writer, resolver, auditLogger)
// after
h := handler.NewTicketHandler(svc, checker, writer, resolver, nil, auditLogger)
```

Run `grep -rn "NewTicketHandler(" internal/handler/*_test.go` to find every call (do not miss any).

- [ ] **Step 9: Wire the real resolver in main**

In `cmd/service/main.go`, after the existing `projectResolver := resolve.New(wsStore, pjStore)` line add:

```go
	tagResolver := resolve.NewTagResolver(pjStore, tagSvc)
```

Update the ticket handler construction to pass it:

```go
	ticketH := handler.NewTicketHandler(svc, sdbChecker, sdbWriter, projectResolver, tagResolver, auditLogger)
```

- [ ] **Step 10: Write a handler test for `tag_names`**

Add to `internal/handler/ticket_test.go` a test that POSTs a ticket with `tag_names` and a resolvable `project_id`, using a stub `TagResolver`. Match the file's existing handler-test setup (router, fake services). Minimal stub + assertion:

```go
type stubTagResolver struct{ ids []uuid.UUID }

func (s stubTagResolver) ResolveTagIDs(ctx context.Context, projectID uuid.UUID, creatorID string, names []string) ([]uuid.UUID, error) {
	return s.ids, nil
}

func TestCreate_TagNamesRequiresProject(t *testing.T) {
	// Build the handler the same way the other tests in this file do, but pass
	// stubTagResolver{} for the tags parameter. POST a body with tag_names and
	// NO project_id / workspace_name. Expect HTTP 400 and a body mentioning
	// "tag_names requires a resolvable project".
	t.Skip("wire to this file's existing handler test harness")
}
```

> The implementer should replace the `t.Skip` with a real test using the harness already present in `ticket_test.go`. Keep the 400-without-project assertion — it is the cheapest meaningful guard.

- [ ] **Step 11: Run the full handler + resolve + ticket test suites**

Run: `go test ./internal/handler/ ./internal/resolve/ ./internal/ticket/ -v`
Expected: PASS (and the suite compiles with the new constructor arg).

- [ ] **Step 12: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 13: Commit**

```bash
git add internal/resolve/tags.go internal/resolve/tags_test.go internal/ticket/model.go internal/handler/ticket.go internal/handler/ticket_test.go internal/handler/authz_e2e_test.go cmd/service/main.go
git commit -m "feat(ticket): name-based tags (tag_names) with get-or-create on create"
```

---

## Task 4: Idempotent create by `title_name`

**Files:**
- Modify: `internal/ticket/store.go` (interface)
- Modify: `internal/ticket/store_pg.go` (implement `GetByProjectAndTitleName`)
- Modify: `internal/ticket/service.go` (passthrough)
- Modify: `internal/handler/ticket.go` (`Create`: 200 + replay header on duplicate)

**Interfaces:**
- Consumes: existing `ErrDuplicateTitleName` and `isUniqueViolation(err, "tickets_project_title_name_key")`.
- Produces:
  - `Store.GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error)`.
  - `Service.GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error)`.
  - Behavior: `POST /tickets` with a `title_name` that already exists in the project returns **200** with header `X-Idempotent-Replay: true`; a fresh create still returns **201**.

- [ ] **Step 1: Write the failing store test**

Add to `internal/ticket/store_pg_test.go` (only runs when the pg harness is present — guard like the other tests in that file). If the file gates on an env/DSN helper, follow the same gate:

```go
func TestGetByProjectAndTitleName(t *testing.T) {
	// Using this file's existing pg test harness:
	//  1. create a project P
	//  2. create a ticket in P with title_name "goq-dedup-x"
	//  3. store.GetByProjectAndTitleName(P, "goq-dedup-x") returns that ticket
	//  4. GetByProjectAndTitleName(P, "nope") returns ErrTicketNotFound (or the
	//     package's not-found sentinel used by Get)
	t.Skip("wire to this file's existing pg harness")
}
```

> Replace `t.Skip` with the real test if the harness is available; otherwise the unit coverage in Step 8 (handler-level, with a mock service) is the safety net.

- [ ] **Step 2: Add the method to the Store interface**

In `internal/ticket/store.go`, add to the `Store` interface (next to `Get`):

```go
	GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error)
```

- [ ] **Step 3: Implement it in the pg store**

In `internal/ticket/store_pg.go`, add a method that reuses the same SELECT/scan shape as `Get`. Find the existing `func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Ticket, error)` to copy its column list and scan helper, then add:

```go
func (s *pgStore) GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error) {
	// Mirror Get's projection (same JOINs/columns/scan). Filter on the
	// partial-unique pair instead of id.
	row := s.db.QueryRow(ctx, ticketSelectSQL+
		" WHERE t.project_id=$1 AND t.title_name=$2"+ticketGroupBySQL,
		projectID, titleName)
	return scanTicketRow(row) // use the same scan helper Get uses
}
```

> Match `Get`'s exact projection and scan function name — open `Get` in this file and copy its `ticketSelectSQL`/`WHERE`/`GROUP BY`/scan usage. If `Get` does not use `ticketSelectSQL`/`ticketGroupBySQL` directly, replicate whatever query+scan `Get` uses, changing only the WHERE clause. Return the same not-found sentinel `Get` returns.

- [ ] **Step 4: Add the service passthrough**

In `internal/ticket/service.go`, add:

```go
func (s *Service) GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error) {
	return s.store.GetByProjectAndTitleName(ctx, projectID, titleName)
}
```

- [ ] **Step 5: Change the handler to replay on duplicate**

In `internal/handler/ticket.go`, in `Create`, find the existing duplicate branch:

```go
	t, err := h.svc.Create(r.Context(), req, requesterID)
	if err != nil {
		if errors.Is(err, ticket.ErrDuplicateTitleName) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
```

Replace the `ErrDuplicateTitleName` branch body so it returns the existing ticket idempotently (a duplicate `title_name` can only occur when `project_id` is set, per the partial index):

```go
	t, err := h.svc.Create(r.Context(), req, requesterID)
	if err != nil {
		if errors.Is(err, ticket.ErrDuplicateTitleName) {
			if req.ProjectID != nil && req.TitleName != "" {
				if existing, gerr := h.svc.GetByProjectAndTitleName(r.Context(), *req.ProjectID, req.TitleName); gerr == nil {
					w.Header().Set("X-Idempotent-Replay", "true")
					writeJSON(w, http.StatusOK, existing)
					return
				}
			}
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
```

- [ ] **Step 6: Write a handler-level idempotency test**

Add to `internal/handler/ticket_test.go`. Use a fake ticket service whose `Create` returns `ticket.ErrDuplicateTitleName` and whose `GetByProjectAndTitleName` returns a known ticket. Assert: status 200, header `X-Idempotent-Replay: true`, body is the existing ticket. (If the file mocks the `*ticket.Service` via an interface, follow that pattern; if it uses a real service over a mock store, set the mock store's `Create` to return the duplicate sentinel.)

```go
func TestCreate_DuplicateTitleNameReplays(t *testing.T) {
	// Arrange a handler whose Create path yields ErrDuplicateTitleName and whose
	// GetByProjectAndTitleName returns an existing ticket. POST with project_id
	// + title_name. Expect: 200, X-Idempotent-Replay=true, body.id == existing.id.
	t.Skip("wire to this file's existing handler test harness")
}
```

> Replace `t.Skip` with a real test using the harness in this file.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/ticket/ ./internal/handler/ -v`
Expected: PASS.

- [ ] **Step 8: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add internal/ticket/store.go internal/ticket/store_pg.go internal/ticket/service.go internal/handler/ticket.go internal/handler/ticket_test.go internal/ticket/store_pg_test.go
git commit -m "feat(ticket): idempotent create by title_name (200 + X-Idempotent-Replay)"
```

---

## Task 5: Query/filter by `title_name` and tag name

**Files:**
- Modify: `internal/ticket/model.go` (`ListTicketsFilter`)
- Modify: `internal/ticket/store_pg.go` (`List` WHERE conditions)
- Modify: `internal/handler/ticket.go` (`List`: new query params; resolve tag names)

**Interfaces:**
- Consumes: existing `List` query builder (`where`/`args`/`i` pattern), the `ticket_tags` table, `h.tags`/`h.resolver` for tag-name → id, the `LEFT JOIN ticket_tags tt` already in `ticketSelectSQL`.
- Produces: `ListTicketsFilter.TitleName *string` and `ListTicketsFilter.TagIDs` honored by the store; `GET /tickets?title_name=&tag=` query params.

- [ ] **Step 1: Add filter fields**

In `internal/ticket/model.go`, in `ListTicketsFilter`, below the existing `TagIDs []uuid.UUID` line add:

```go
	TitleName    *string
```

(`TagIDs` already exists; we will now actually apply it in the store.)

- [ ] **Step 2: Write the failing store test for filtering**

Add to `internal/ticket/store_pg_test.go` (pg-harness-gated):

```go
func TestList_FilterByTitleNameAndTag(t *testing.T) {
	// With the pg harness: create two tickets in a project, one with
	// title_name "goq-a" tagged "infra", one with title_name "goq-b".
	//  - List{TitleName: ptr("goq-a")} returns exactly the first.
	//  - List{TagIDs: []uuid.UUID{infraTagID}} returns exactly the first.
	t.Skip("wire to this file's existing pg harness")
}
```

- [ ] **Step 3: Apply `TitleName` and `TagIDs` in the store `List`**

In `internal/ticket/store_pg.go`, inside `func (s *pgStore) List`, in the section where `where`/`args` conditions are appended (after the existing `f.ParentID` / before the `VisibleFilter` block), add:

```go
	if f.TitleName != nil {
		i := len(args) + 1
		where += fmt.Sprintf(" AND t.title_name=$%d", i)
		args = append(args, *f.TitleName)
	}
	if len(f.TagIDs) > 0 {
		i := len(args) + 1
		where += fmt.Sprintf(" AND t.id IN (SELECT ticket_id FROM ticket_tags WHERE tag_id = ANY($%d))", i)
		args = append(args, f.TagIDs)
	}
```

> Use the same `i := len(args) + 1` placeholder-indexing convention already used by the surrounding conditions in this function. The `IN (SELECT ...)` subquery (rather than a join predicate) keeps the existing `GROUP BY t.id` intact and matches **any** of the requested tags.

- [ ] **Step 4: Wire query params in the handler `List`**

In `internal/handler/ticket.go`, in `List`, after the existing `requester_id` param handling add:

```go
	if tn := q.Get("title_name"); tn != "" {
		f.TitleName = &tn
	}
```

Then add tag-name resolution. Tag filtering needs a workspace; resolve it from `project_id` when present (the agent dedup/list flow always scopes by project). After the `project_id` param block add:

```go
	if tagNames := q["tag"]; len(tagNames) > 0 {
		if h.tags == nil || f.ProjectID == nil {
			writeError(w, http.StatusBadRequest, "tag filter requires project_id")
			return
		}
		tagIDs, terr := h.tags.ResolveTagIDs(r.Context(), *f.ProjectID, auth.RequesterID(r), tagNames)
		if terr != nil {
			writeError(w, http.StatusBadRequest, "resolve tag filter: "+terr.Error())
			return
		}
		f.TagIDs = tagIDs
	}
```

> `q["tag"]` returns all repeated `?tag=` values. Using `ResolveTagIDs` (get-or-create) for a filter is acceptable here: a filter naming a not-yet-existing tag creates an empty tag and yields zero matching tickets — consistent with "no tickets have that tag yet." This avoids a second read-only resolver. (If a strictly read-only filter is later desired, swap to a `GetByName`-only path; out of scope now.)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/ticket/ ./internal/handler/ -v`
Expected: PASS.

- [ ] **Step 6: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/ticket/model.go internal/ticket/store_pg.go internal/handler/ticket.go internal/ticket/store_pg_test.go
git commit -m "feat(ticket): list filter by title_name and tag name"
```

---

## Task 6: `GET /tickets/by-name/{title_name}` lookup route

**Files:**
- Modify: `internal/handler/ticket.go` (new `GetByName` handler)
- Modify: `cmd/service/main.go` (register route)
- Modify: `internal/handler/ticket_test.go` (route test)

**Interfaces:**
- Consumes: `h.resolver.ResolveProjectID` (existing), `h.svc.GetByProjectAndTitleName` (Task 4), chi `URLParam`.
- Produces: `GET /api/v1/tickets/by-name/{title_name}?workspace=<name|slug>&project=<name>` → 200 ticket | 404 | 400.

- [ ] **Step 1: Implement the handler**

In `internal/handler/ticket.go`, add:

```go
// GetByName resolves a ticket by its human-readable title_name within a
// project identified by workspace + project names (no UUIDs required).
func (h *TicketHandler) GetByName(w http.ResponseWriter, r *http.Request) {
	titleName := chi.URLParam(r, "title_name")
	if titleName == "" {
		writeError(w, http.StatusBadRequest, "title_name is required")
		return
	}
	q := r.URL.Query()
	ref := resolve.Ref{
		WorkspaceSlug: q.Get("workspace_slug"),
		WorkspaceName: q.Get("workspace"),
		ProjectName:   q.Get("project"),
	}
	pid, rerr := h.resolver.ResolveProjectID(r.Context(), ref)
	if rerr != nil {
		writeError(w, http.StatusBadRequest, rerr.Error())
		return
	}
	if pid == nil {
		writeError(w, http.StatusBadRequest, "workspace and project are required")
		return
	}
	t, err := h.svc.GetByProjectAndTitleName(r.Context(), *pid, titleName)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	writeJSON(w, http.StatusOK, t)
}
```

> `resolve.Ref` accepts both `workspace` (name) and `workspace_slug`; slug wins when both are present (existing resolver behavior). `project` maps to `ProjectName`.

- [ ] **Step 2: Register the route**

In `cmd/service/main.go`, next to the existing `r.Get("/tickets/doc/{doc_id}", ticketH.GetDoc)` line (a sibling fixed-prefix route, so no conflict with `/tickets/{id}`), add:

```go
			r.Get("/tickets/by-name/{title_name}", ticketH.GetByName)
```

- [ ] **Step 3: Write the route test**

Add to `internal/handler/ticket_test.go` a test that mounts `GET /tickets/by-name/{title_name}`, with a stub resolver returning a project id and a fake service returning a ticket; assert 200 + body. Add a second case: resolver returns `nil` → expect 400. Follow this file's existing router/harness setup.

```go
func TestGetByName_OKAndMissingProject(t *testing.T) {
	t.Skip("wire to this file's existing handler test harness")
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/handler/ -v`
Expected: PASS.

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/ticket.go cmd/service/main.go internal/handler/ticket_test.go
git commit -m "feat(ticket): GET /tickets/by-name/{title_name} lookup route"
```

---

## Task 7: OpenAPI spec (hand-written, ticket-focused) + serving

**Files:**
- Create: `api/openapi.yaml`
- Create: `internal/handler/openapi.go`
- Create: `internal/handler/openapi_test.go`
- Modify: `cmd/service/main.go` (register routes)

**Interfaces:**
- Consumes: Go `embed` of `api/openapi.yaml`; a YAML→JSON conversion (use `sigs.k8s.io/yaml` if already in `go.mod`, else `gopkg.in/yaml.v3` + `encoding/json`).
- Produces: `GET /api/v1/openapi.yaml` (raw) and `GET /api/v1/openapi.json` (converted) — both unauthenticated (placed outside the API-key group, beside `/health`).

- [ ] **Step 1: Check which YAML lib is available**

Run: `grep -E "yaml|sigs.k8s.io/yaml" go.mod`
Expected: note whether `gopkg.in/yaml.v3` and/or `sigs.k8s.io/yaml` is present. Use whichever exists; if neither, run `go get sigs.k8s.io/yaml` (it converts YAML↔JSON cleanly) and record it in the commit.

- [ ] **Step 2: Write the spec file**

Create `api/openapi.yaml` covering the agent-facing ticket surface. Minimum viable content (expand fields to match `Ticket`/`CreateTicketReq` in `internal/ticket/model.go`):

```yaml
openapi: 3.1.0
info:
  title: goquest tickets API
  version: "1.0.0"
  description: >
    Agent-facing ticket operations. Authenticate with the X-API-Key header
    (project-scoped). title_name is a human handle (e.g. goq-vault-portforward),
    unique per project.
servers:
  - url: https://goquest.toji.homes/api/v1
components:
  securitySchemes:
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key
  schemas:
    CreateTicketReq:
      type: object
      required: [title]
      properties:
        title: { type: string }
        title_name:
          type: string
          description: lowercase hyphen-separated handle, unique per project
        description: { type: string }
        type: { type: string, enum: [task, bug, feature, incident, request] }
        priority: { type: string, enum: [urgent, high, normal, low] }
        domain: { type: string, enum: [infra, data, code, monitor, knowledge, pm] }
        project_id: { type: string, format: uuid }
        workspace_name: { type: string }
        workspace_slug: { type: string }
        project_name: { type: string }
        parent_id: { type: string, format: uuid }
        tag_ids: { type: array, items: { type: string, format: uuid } }
        tag_names:
          type: array
          items: { type: string }
          description: tag names; missing tags are created in the project's workspace
    Ticket:
      type: object
      properties:
        id: { type: string, format: uuid }
        title_name: { type: string }
        title: { type: string }
        status: { type: string }
        priority: { type: string }
        domain: { type: string }
        project_id: { type: string, format: uuid }
security:
  - ApiKeyAuth: []
paths:
  /tickets:
    post:
      summary: Create a ticket (idempotent on title_name within a project)
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/CreateTicketReq' }
      responses:
        '201': { description: created, content: { application/json: { schema: { $ref: '#/components/schemas/Ticket' } } } }
        '200':
          description: existing ticket replayed (header X-Idempotent-Replay=true)
          content: { application/json: { schema: { $ref: '#/components/schemas/Ticket' } } }
        '400': { description: bad request }
    get:
      summary: List tickets
      parameters:
        - { name: project_id, in: query, schema: { type: string, format: uuid } }
        - { name: title_name, in: query, schema: { type: string } }
        - { name: tag, in: query, schema: { type: string }, description: repeatable tag-name filter (requires project_id) }
        - { name: status, in: query, schema: { type: string } }
        - { name: domain, in: query, schema: { type: string } }
      responses:
        '200': { description: ok }
  /tickets/by-name/{title_name}:
    get:
      summary: Look up a ticket by title_name within a project
      parameters:
        - { name: title_name, in: path, required: true, schema: { type: string } }
        - { name: workspace, in: query, schema: { type: string } }
        - { name: workspace_slug, in: query, schema: { type: string } }
        - { name: project, in: query, schema: { type: string } }
      responses:
        '200': { description: ok, content: { application/json: { schema: { $ref: '#/components/schemas/Ticket' } } } }
        '400': { description: missing/unresolvable identifiers }
        '404': { description: not found }
  /tickets/{id}:
    get:
      summary: Get a ticket by UUID
      parameters:
        - { name: id, in: path, required: true, schema: { type: string, format: uuid } }
      responses:
        '200': { description: ok }
        '404': { description: not found }
    patch:
      summary: Update a ticket
      parameters:
        - { name: id, in: path, required: true, schema: { type: string, format: uuid } }
      responses:
        '200': { description: ok }
```

- [ ] **Step 3: Write the serving handler**

Create `internal/handler/openapi.go` (adjust the import to the YAML lib chosen in Step 1; example uses `sigs.k8s.io/yaml`):

```go
package handler

import (
	_ "embed"
	"net/http"

	"sigs.k8s.io/yaml"
)

//go:embed openapi_spec.yaml
var openapiYAML []byte

// OpenAPIHandler serves the hand-written ticket OpenAPI spec.
type OpenAPIHandler struct {
	json []byte
}

func NewOpenAPIHandler() (*OpenAPIHandler, error) {
	j, err := yaml.YAMLToJSON(openapiYAML)
	if err != nil {
		return nil, err
	}
	return &OpenAPIHandler{json: j}, nil
}

func (h *OpenAPIHandler) JSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(h.json)
}

func (h *OpenAPIHandler) YAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openapiYAML)
}
```

> `go:embed` cannot reach a sibling parent dir (`../../api/`), so place the embeddable copy **next to the handler**. Create `internal/handler/openapi_spec.yaml` as a copy of `api/openapi.yaml` (the `api/` copy is the human-edited source of truth; document in the file header that they must be kept in sync, or — simpler — keep ONLY `internal/handler/openapi_spec.yaml` and drop `api/openapi.yaml`). Choose one location to avoid drift; recommended: keep only `internal/handler/openapi_spec.yaml` and skip creating `api/openapi.yaml`.

- [ ] **Step 4: Write the serving test**

Create `internal/handler/openapi_test.go`:

```go
package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAPI_JSONServes(t *testing.T) {
	h, err := NewOpenAPIHandler()
	if err != nil {
		t.Fatalf("NewOpenAPIHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.JSON(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if doc["openapi"] == nil {
		t.Fatalf("missing openapi field")
	}
}
```

- [ ] **Step 5: Run the test to verify it fails, then passes**

Run: `go test ./internal/handler/ -run TestOpenAPI -v`
Expected: first FAIL (handler/embed not present), then PASS after Steps 2-3 are in place.

- [ ] **Step 6: Register the routes (unauthenticated, beside /health)**

In `cmd/service/main.go`, construct the handler near the other handler constructors:

```go
	openapiH, err := handler.NewOpenAPIHandler()
	if err != nil {
		log.Fatalf("openapi: %v", err)
	}
```

Register inside the `r.Route("/api/v1", ...)` block but **outside** the API-key `r.Group(...)` (next to `r.Get("/health", healthH.Health)`):

```go
		r.Get("/openapi.json", openapiH.JSON)
		r.Get("/openapi.yaml", openapiH.YAML)
```

- [ ] **Step 7: Build + vet + full test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build/vet clean; tests pass (pg-harness-gated tests may skip).

- [ ] **Step 8: Commit**

```bash
git add internal/handler/openapi.go internal/handler/openapi_spec.yaml internal/handler/openapi_test.go cmd/service/main.go go.mod go.sum
git commit -m "feat(api): serve hand-written ticket OpenAPI spec at /api/v1/openapi.{json,yaml}"
```

---

## Final verification

- [ ] **Run the complete suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build clean, vet clean, all unit tests pass (integration tests skip without a DB harness).

- [ ] **Manual smoke (optional, requires running service + DB)**

```bash
# create with tag_names + title_name (201)
curl -s -X POST "$BASE/api/v1/tickets" -H "X-API-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"title":"[GOQ] smoke","title_name":"goq-smoke-1","workspace_name":"toji","project_name":"ops-troubleshooting","domain":"code","tag_names":["goquest"]}'
# repeat identical request → 200 with X-Idempotent-Replay: true
curl -si -X POST "$BASE/api/v1/tickets" -H "X-API-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"title":"[GOQ] smoke","title_name":"goq-smoke-1","workspace_name":"toji","project_name":"ops-troubleshooting"}' | grep -i x-idempotent-replay
# look up by name
curl -s "$BASE/api/v1/tickets/by-name/goq-smoke-1?workspace=toji&project=ops-troubleshooting" -H "X-API-Key: $KEY"
# filter by tag
curl -s "$BASE/api/v1/tickets?project_id=<PID>&tag=goquest" -H "X-API-Key: $KEY"
# fetch the contract
curl -s "$BASE/api/v1/openapi.json" | head
```

---

## Notes for the executor

- **Test harness reuse:** several test steps end in `t.Skip("wire to ...")`. These are placeholders for tests that must plug into the *existing* harness in that file (router construction, fake services, pg gating). Replace each `t.Skip` with a real test modeled on the neighbouring tests in the same file — do not leave skips in the final commits.
- **Constructor churn:** Task 3 changes `NewTicketHandler`'s signature (adds the `tags TagResolver` param before `auditLogger`). Every call site must be updated in the same task or the package won't compile — `grep -rn "NewTicketHandler(" --include=*.go`.
- **Order matters:** Task 4 depends on `GetByProjectAndTitleName` (also used by Task 6). Task 5 and 6 depend on Task 3's `h.tags`. Execute in numbered order.
