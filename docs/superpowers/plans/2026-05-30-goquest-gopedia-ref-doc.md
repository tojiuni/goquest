# goquest ↔ gopedia Ref Doc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ticket에 gopedia L1 문서를 옵션 첨부(N개)로 ref하는 lean 기능 — id만 저장, live link, goquest의 gopedia 런타임 의존성 0, ticket 가시성과 gopedia ACL 완전 분리.

**Architecture:** 새 테이블 `ticket_refs(ticket_id, gopedia_l1_id, position, ...)` 추가. 기존 `tickets`/Service/Handler 패턴 그대로 확장. ref 노출은 `Ticket.Refs []string` 필드 추가로 REST 응답·Redis Stream 이벤트 양쪽에 자동 반영. 역참조는 SpiceDB `VisibleResourceIDs` 교집합으로 post-filter. bot/orchestrator는 ref_ids를 받아 자기 ZITADEL 토큰으로 gopedia를 직접 fetch.

**Tech Stack:** Go 1.25, chi v5, pgxpool, SpiceDB(authzed), Redis Streams, ZITADEL JWT, embedded SQL migrations (`internal/migrate`).

**Spec:** `docs/superpowers/specs/2026-05-30-goquest-gopedia-ref-doc-design.md`

---

## File Structure

**Create:**
- `internal/migrate/sql/015_ticket_refs.sql` — new table
- `internal/ticket/refs_store_test.go` — store integration tests
- `internal/ticket/refs_service_test.go` — service tests
- `internal/handler/refs_test.go` — handler HTTP tests
- `internal/handler/refs_e2e_test.go` — full-stack E2E

**Modify:**
- `internal/ticket/model.go` — add `TicketRef`, `Refs []string` on `Ticket`, `RefIDs []string` on Create/Update reqs, `ErrRefConflict`, `ErrRefLimitExceeded`
- `internal/ticket/store.go` — extend `Store` interface with refs methods
- `internal/ticket/store_pg.go` — implement refs methods; populate `Refs` in Get/List
- `internal/ticket/service.go` — add `AddRefs`, `ListRefs`, `RemoveRef`, `ReorderRefs`, `ListTicketsByRef`; integrate `ref_ids` into `Create`/`Update`
- `internal/handler/ticket.go` — new methods: `ListRefs`, `AddRefs`, `RemoveRef`, `ReorderRefs`, `ReverseLookup`
- `cmd/service/main.go` — wire new routes

**Out of scope (this plan):**
- gopedia 측 변경 (역참조 API 호출만 알려주면 됨)
- bot/orchestrator의 ref fetch 로직 (각자 레포)
- UI 변경

---

## Conventions Reminder

- All tests against real Postgres use the existing `setupTestDB(t)` helper (see `internal/ticket/store_transitions_test.go` for the pattern).
- Service tests can use the existing in-memory or stub patterns established in `internal/ticket/service_test.go`.
- Handler HTTP tests follow `internal/handler/authz_e2e_test.go` style — real chi router + real store + stub checker.
- Commit after each green test cycle. Each commit message should follow `feat(refs): ...` / `test(refs): ...` / `fix(refs): ...` style.

---

## Task 1: DB Migration

**Files:**
- Create: `internal/migrate/sql/015_ticket_refs.sql`

Migrations are auto-applied at startup by `internal/migrate.Run`. No code wiring needed.

- [ ] **Step 1: Write the SQL**

Create `internal/migrate/sql/015_ticket_refs.sql`:

```sql
-- ticket_refs: optional N-ary references from a ticket to gopedia L1 docs.
-- id-only (no title/snippet cache); live link policy; CASCADE on ticket delete.
CREATE TABLE IF NOT EXISTS ticket_refs (
    ticket_id       UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    gopedia_l1_id   TEXT NOT NULL,
    position        INT  NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      TEXT NOT NULL,
    PRIMARY KEY (ticket_id, gopedia_l1_id)
);

CREATE INDEX IF NOT EXISTS idx_ticket_refs_gopedia ON ticket_refs(gopedia_l1_id);
CREATE INDEX IF NOT EXISTS idx_ticket_refs_ticket  ON ticket_refs(ticket_id, position);
```

- [ ] **Step 2: Verify migration loads**

Run: `go build ./...`
Expected: success (the file is picked up via `//go:embed sql/*.sql`).

Then run the migrate package's existing test to confirm SQL applies cleanly:

Run: `go test ./internal/migrate/...`
Expected: PASS — existing tests apply all migrations including 015 against the test DB.

- [ ] **Step 3: Commit**

```bash
git add internal/migrate/sql/015_ticket_refs.sql
git commit -m "feat(refs): 015 ticket_refs table (id-only, CASCADE on ticket)"
```

---

## Task 2: Model — TicketRef struct + req field + errors

**Files:**
- Modify: `internal/ticket/model.go`

- [ ] **Step 1: Write the failing test for serialization**

Append to `internal/ticket/model_test.go`:

```go
func TestTicketRef_JSONShape(t *testing.T) {
    now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
    r := TicketRef{
        TicketID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
        GopediaL1ID: "l1-abc",
        Position:    2,
        CreatedAt:   now,
        CreatedBy:   "user@example.com",
    }
    b, err := json.Marshal(r)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    got := string(b)
    want := `{"ticket_id":"11111111-1111-1111-1111-111111111111","gopedia_l1_id":"l1-abc","position":2,"created_at":"2026-05-30T12:00:00Z","created_by":"user@example.com"}`
    if got != want {
        t.Errorf("json shape mismatch:\n got=%s\nwant=%s", got, want)
    }
}

func TestTicket_RefsOmittedWhenEmpty(t *testing.T) {
    var tk Ticket
    b, _ := json.Marshal(tk)
    if strings.Contains(string(b), `"ref_ids"`) {
        t.Errorf("empty Refs must be omitted: %s", string(b))
    }
}

func TestTicket_RefsPresentWhenSet(t *testing.T) {
    tk := Ticket{Refs: []string{"l1-a", "l1-b"}}
    b, _ := json.Marshal(tk)
    if !strings.Contains(string(b), `"ref_ids":["l1-a","l1-b"]`) {
        t.Errorf("Refs not serialized as ref_ids: %s", string(b))
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ticket/ -run 'TestTicketRef_JSONShape|TestTicket_Refs' -v`
Expected: FAIL — `TicketRef` undefined, `Refs` undefined.

- [ ] **Step 3: Add the types and errors**

In `internal/ticket/model.go`:

1. Add `Refs []string \`json:"ref_ids,omitempty"\`` to the `Ticket` struct, right after `Tags []Tag` line.
2. Add `RefIDs []string \`json:"ref_ids,omitempty"\`` to `CreateTicketReq` and `UpdateTicketReq` structs.
3. Append new types:

```go
type TicketRef struct {
    TicketID    uuid.UUID `json:"ticket_id"`
    GopediaL1ID string    `json:"gopedia_l1_id"`
    Position    int       `json:"position"`
    CreatedAt   time.Time `json:"created_at"`
    CreatedBy   string    `json:"created_by"`
}

const (
    MaxRefsPerTicket  = 50
    MaxRefIDLength    = 512
)
```

4. Append new errors (next to existing `ErrNotFound`):

```go
var (
    ErrRefConflict       = errors.New("ref already exists for this ticket")
    ErrRefLimitExceeded  = errors.New("ref count exceeds MaxRefsPerTicket")
    ErrRefIDInvalid      = errors.New("gopedia_l1_id must be 1..512 chars")
    ErrRefIDsDuplicate   = errors.New("duplicate gopedia_l1_id in request")
)
```

Add `"errors"` import if not present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ticket/ -run 'TestTicketRef_JSONShape|TestTicket_Refs' -v`
Expected: PASS (all 3).

- [ ] **Step 5: Verify no regression in existing model tests**

Run: `go test ./internal/ticket/ -run 'TestTicket' -v`
Expected: PASS — all existing tests still pass.

- [ ] **Step 6: Commit**

```bash
git add internal/ticket/model.go internal/ticket/model_test.go
git commit -m "feat(refs): TicketRef model + Refs field on Ticket + req errors"
```

---

## Task 3: Store interface — refs methods (signature only, no impl)

**Files:**
- Modify: `internal/ticket/store.go`

This task just extends the interface so subsequent tasks have a contract to implement. No tests yet (interfaces have no behavior).

- [ ] **Step 1: Extend the Store interface**

Append inside the `Store` interface in `internal/ticket/store.go`:

```go
    // Refs (id-only). All methods are transaction-internal where needed; callers
    // do not assume cross-call atomicity except where explicitly noted.
    AddRefs(ctx context.Context, ticketID uuid.UUID, gopediaL1IDs []string, createdBy string) ([]TicketRef, error)
    ListRefs(ctx context.Context, ticketID uuid.UUID) ([]TicketRef, error)
    RemoveRef(ctx context.Context, ticketID uuid.UUID, gopediaL1ID string) error
    ReorderRefs(ctx context.Context, ticketID uuid.UUID, orderedL1IDs []string) error
    CountRefs(ctx context.Context, ticketID uuid.UUID) (int, error)
    ListTicketIDsByRef(ctx context.Context, gopediaL1ID string) ([]uuid.UUID, error)
    ListRefIDsForTickets(ctx context.Context, ticketIDs []uuid.UUID) (map[uuid.UUID][]string, error)
```

- [ ] **Step 2: Verify build (PGStore will not satisfy interface yet — expected)**

Run: `go build ./internal/ticket/`
Expected: FAIL — `*pgStore does not implement Store: missing AddRefs ...`. This is intentional; Task 4 implements them.

Do NOT commit yet — leave it broken; Task 4 fixes the build.

---

## Task 4: PGStore impl — refs methods + populate Refs on Get/List

**Files:**
- Modify: `internal/ticket/store_pg.go`
- Create: `internal/ticket/refs_store_test.go`

- [ ] **Step 1: Write failing store tests**

Create `internal/ticket/refs_store_test.go`:

```go
package ticket_test

import (
    "context"
    "errors"
    "testing"

    "github.com/google/uuid"
    "github.com/tojiuni/goquest/internal/ticket"
)

// setupTestDB is the helper used by other store tests; reuse it.
// It returns a *pgxpool.Pool against a freshly-migrated test DB.

func makeTicketForRefs(t *testing.T, store ticket.Store) uuid.UUID {
    t.Helper()
    tk := &ticket.Ticket{
        Title:        "refs-test",
        Description:  "",
        Type:         ticket.TypeTask,
        Status:       ticket.StatusPending,
        Priority:     ticket.PriorityNormal,
        AssigneeType: ticket.AssigneeHuman,
        RequesterID:  "test@example.com",
        Visibility:   ticket.VisibilityProject,
    }
    if err := store.Create(context.Background(), tk); err != nil {
        t.Fatalf("seed ticket: %v", err)
    }
    return tk.ID
}

func TestRefs_AddAndList(t *testing.T) {
    pool, store := setupTestDB(t)
    _ = pool
    tid := makeTicketForRefs(t, store)

    refs, err := store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x")
    if err != nil {
        t.Fatalf("AddRefs: %v", err)
    }
    if len(refs) != 2 {
        t.Fatalf("want 2 refs, got %d", len(refs))
    }
    if refs[0].Position != 0 || refs[1].Position != 1 {
        t.Errorf("positions: got %d,%d want 0,1", refs[0].Position, refs[1].Position)
    }

    listed, err := store.ListRefs(context.Background(), tid)
    if err != nil {
        t.Fatalf("ListRefs: %v", err)
    }
    if len(listed) != 2 || listed[0].GopediaL1ID != "l1-a" || listed[1].GopediaL1ID != "l1-b" {
        t.Errorf("ListRefs order/content wrong: %+v", listed)
    }
}

func TestRefs_AddConflict(t *testing.T) {
    _, store := setupTestDB(t)
    tid := makeTicketForRefs(t, store)
    if _, err := store.AddRefs(context.Background(), tid, []string{"l1-a"}, "u@x"); err != nil {
        t.Fatalf("first add: %v", err)
    }
    _, err := store.AddRefs(context.Background(), tid, []string{"l1-a"}, "u@x")
    if !errors.Is(err, ticket.ErrRefConflict) {
        t.Errorf("want ErrRefConflict, got %v", err)
    }
}

func TestRefs_RemoveIdempotent(t *testing.T) {
    _, store := setupTestDB(t)
    tid := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), tid, []string{"l1-a"}, "u@x")

    if err := store.RemoveRef(context.Background(), tid, "l1-a"); err != nil {
        t.Fatalf("first remove: %v", err)
    }
    if err := store.RemoveRef(context.Background(), tid, "l1-a"); err != nil {
        t.Errorf("second remove must be idempotent, got %v", err)
    }
    if err := store.RemoveRef(context.Background(), tid, "never-existed"); err != nil {
        t.Errorf("remove of non-existing must be idempotent, got %v", err)
    }
}

func TestRefs_AppendPositionMonotonic(t *testing.T) {
    _, store := setupTestDB(t)
    tid := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x")
    refs, err := store.AddRefs(context.Background(), tid, []string{"l1-c"}, "u@x")
    if err != nil {
        t.Fatalf("AddRefs: %v", err)
    }
    if refs[0].Position != 2 {
        t.Errorf("new ref position want 2, got %d", refs[0].Position)
    }
}

func TestRefs_Reorder(t *testing.T) {
    _, store := setupTestDB(t)
    tid := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b", "l1-c"}, "u@x")

    if err := store.ReorderRefs(context.Background(), tid, []string{"l1-c", "l1-a", "l1-b"}); err != nil {
        t.Fatalf("ReorderRefs: %v", err)
    }
    listed, _ := store.ListRefs(context.Background(), tid)
    want := []string{"l1-c", "l1-a", "l1-b"}
    for i, r := range listed {
        if r.GopediaL1ID != want[i] {
            t.Errorf("position %d: got %s want %s", i, r.GopediaL1ID, want[i])
        }
    }
}

func TestRefs_ReorderMismatchedSet(t *testing.T) {
    _, store := setupTestDB(t)
    tid := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x")
    err := store.ReorderRefs(context.Background(), tid, []string{"l1-a", "l1-c"})
    if err == nil {
        t.Errorf("want error when reorder ids don't match existing set")
    }
}

func TestRefs_Count(t *testing.T) {
    _, store := setupTestDB(t)
    tid := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x")
    n, err := store.CountRefs(context.Background(), tid)
    if err != nil || n != 2 {
        t.Errorf("Count want 2, got %d err=%v", n, err)
    }
}

func TestRefs_ListTicketIDsByRef(t *testing.T) {
    _, store := setupTestDB(t)
    t1 := makeTicketForRefs(t, store)
    t2 := makeTicketForRefs(t, store)
    t3 := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), t1, []string{"l1-shared"}, "u@x")
    _, _ = store.AddRefs(context.Background(), t2, []string{"l1-shared"}, "u@x")
    _, _ = store.AddRefs(context.Background(), t3, []string{"l1-other"}, "u@x")

    ids, err := store.ListTicketIDsByRef(context.Background(), "l1-shared")
    if err != nil {
        t.Fatalf("ListTicketIDsByRef: %v", err)
    }
    if len(ids) != 2 {
        t.Errorf("want 2 ticket ids, got %d", len(ids))
    }
}

func TestRefs_CascadeOnTicketDelete(t *testing.T) {
    pool, store := setupTestDB(t)
    tid := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), tid, []string{"l1-a"}, "u@x")
    if _, err := pool.Exec(context.Background(), `DELETE FROM tickets WHERE id=$1`, tid); err != nil {
        t.Fatalf("delete ticket: %v", err)
    }
    var n int
    if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ticket_refs WHERE ticket_id=$1`, tid).Scan(&n); err != nil {
        t.Fatalf("count refs: %v", err)
    }
    if n != 0 {
        t.Errorf("CASCADE failed: %d refs remain", n)
    }
}

func TestRefs_ListRefIDsForTickets(t *testing.T) {
    _, store := setupTestDB(t)
    t1 := makeTicketForRefs(t, store)
    t2 := makeTicketForRefs(t, store)
    _, _ = store.AddRefs(context.Background(), t1, []string{"l1-a", "l1-b"}, "u@x")

    m, err := store.ListRefIDsForTickets(context.Background(), []uuid.UUID{t1, t2})
    if err != nil {
        t.Fatalf("ListRefIDsForTickets: %v", err)
    }
    if len(m[t1]) != 2 || m[t1][0] != "l1-a" || m[t1][1] != "l1-b" {
        t.Errorf("t1 refs: %v", m[t1])
    }
    if _, ok := m[t2]; ok {
        t.Errorf("t2 should not be present in map when it has no refs (or be empty), got %v", m[t2])
    }
}
```

- [ ] **Step 2: Run tests to verify they fail (compile error)**

Run: `go test ./internal/ticket/ -run 'TestRefs_'`
Expected: FAIL — compile errors because `AddRefs`/etc. not implemented on `*pgStore`.

- [ ] **Step 3: Implement methods on PGStore**

Append to `internal/ticket/store_pg.go`:

```go
// --- Refs ---

func (s *pgStore) AddRefs(ctx context.Context, ticketID uuid.UUID, gopediaL1IDs []string, createdBy string) ([]ticket.TicketRef, error) {
    if len(gopediaL1IDs) == 0 {
        return nil, nil
    }

    tx, err := s.db.Begin(ctx)
    if err != nil {
        return nil, fmt.Errorf("begin: %w", err)
    }
    defer tx.Rollback(ctx)

    // Lock the ticket row to serialize concurrent ref appends and read a stable max(position).
    var exists bool
    if err := tx.QueryRow(ctx,
        `SELECT EXISTS(SELECT 1 FROM tickets WHERE id=$1 FOR UPDATE)`, ticketID,
    ).Scan(&exists); err != nil {
        return nil, fmt.Errorf("lock ticket: %w", err)
    }
    if !exists {
        return nil, ticket.ErrNotFound
    }

    var maxPos *int
    if err := tx.QueryRow(ctx,
        `SELECT MAX(position) FROM ticket_refs WHERE ticket_id=$1`, ticketID,
    ).Scan(&maxPos); err != nil {
        return nil, fmt.Errorf("max pos: %w", err)
    }
    nextPos := 0
    if maxPos != nil {
        nextPos = *maxPos + 1
    }

    out := make([]ticket.TicketRef, 0, len(gopediaL1IDs))
    for i, id := range gopediaL1IDs {
        var r ticket.TicketRef
        err := tx.QueryRow(ctx, `
            INSERT INTO ticket_refs (ticket_id, gopedia_l1_id, position, created_by)
            VALUES ($1, $2, $3, $4)
            RETURNING ticket_id, gopedia_l1_id, position, created_at, created_by`,
            ticketID, id, nextPos+i, createdBy,
        ).Scan(&r.TicketID, &r.GopediaL1ID, &r.Position, &r.CreatedAt, &r.CreatedBy)
        if err != nil {
            if isUniqueViolation(err) {
                return nil, ticket.ErrRefConflict
            }
            return nil, fmt.Errorf("insert ref %s: %w", id, err)
        }
        out = append(out, r)
    }
    if err := tx.Commit(ctx); err != nil {
        return nil, fmt.Errorf("commit: %w", err)
    }
    return out, nil
}

func (s *pgStore) ListRefs(ctx context.Context, ticketID uuid.UUID) ([]ticket.TicketRef, error) {
    rows, err := s.db.Query(ctx, `
        SELECT ticket_id, gopedia_l1_id, position, created_at, created_by
          FROM ticket_refs
         WHERE ticket_id=$1
         ORDER BY position ASC`, ticketID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []ticket.TicketRef
    for rows.Next() {
        var r ticket.TicketRef
        if err := rows.Scan(&r.TicketID, &r.GopediaL1ID, &r.Position, &r.CreatedAt, &r.CreatedBy); err != nil {
            return nil, err
        }
        out = append(out, r)
    }
    return out, rows.Err()
}

func (s *pgStore) RemoveRef(ctx context.Context, ticketID uuid.UUID, gopediaL1ID string) error {
    _, err := s.db.Exec(ctx,
        `DELETE FROM ticket_refs WHERE ticket_id=$1 AND gopedia_l1_id=$2`,
        ticketID, gopediaL1ID)
    return err
}

func (s *pgStore) ReorderRefs(ctx context.Context, ticketID uuid.UUID, orderedL1IDs []string) error {
    tx, err := s.db.Begin(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx)

    // Verify the proposed set matches the existing set exactly.
    rows, err := tx.Query(ctx,
        `SELECT gopedia_l1_id FROM ticket_refs WHERE ticket_id=$1 FOR UPDATE`, ticketID)
    if err != nil {
        return err
    }
    existing := map[string]bool{}
    for rows.Next() {
        var id string
        if err := rows.Scan(&id); err != nil {
            rows.Close()
            return err
        }
        existing[id] = true
    }
    rows.Close()

    if len(existing) != len(orderedL1IDs) {
        return fmt.Errorf("reorder: id count mismatch (existing=%d, given=%d)", len(existing), len(orderedL1IDs))
    }
    seen := map[string]bool{}
    for _, id := range orderedL1IDs {
        if !existing[id] {
            return fmt.Errorf("reorder: %s not in existing set", id)
        }
        if seen[id] {
            return fmt.Errorf("reorder: %s duplicated", id)
        }
        seen[id] = true
    }

    for i, id := range orderedL1IDs {
        if _, err := tx.Exec(ctx,
            `UPDATE ticket_refs SET position=$1 WHERE ticket_id=$2 AND gopedia_l1_id=$3`,
            i, ticketID, id); err != nil {
            return err
        }
    }
    return tx.Commit(ctx)
}

func (s *pgStore) CountRefs(ctx context.Context, ticketID uuid.UUID) (int, error) {
    var n int
    err := s.db.QueryRow(ctx,
        `SELECT COUNT(*) FROM ticket_refs WHERE ticket_id=$1`, ticketID).Scan(&n)
    return n, err
}

func (s *pgStore) ListTicketIDsByRef(ctx context.Context, gopediaL1ID string) ([]uuid.UUID, error) {
    rows, err := s.db.Query(ctx,
        `SELECT ticket_id FROM ticket_refs WHERE gopedia_l1_id=$1`, gopediaL1ID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []uuid.UUID
    for rows.Next() {
        var id uuid.UUID
        if err := rows.Scan(&id); err != nil {
            return nil, err
        }
        out = append(out, id)
    }
    return out, rows.Err()
}

func (s *pgStore) ListRefIDsForTickets(ctx context.Context, ticketIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
    if len(ticketIDs) == 0 {
        return map[uuid.UUID][]string{}, nil
    }
    rows, err := s.db.Query(ctx, `
        SELECT ticket_id, gopedia_l1_id
          FROM ticket_refs
         WHERE ticket_id = ANY($1)
         ORDER BY ticket_id, position ASC`, ticketIDs)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    out := map[uuid.UUID][]string{}
    for rows.Next() {
        var tid uuid.UUID
        var rid string
        if err := rows.Scan(&tid, &rid); err != nil {
            return nil, err
        }
        out[tid] = append(out[tid], rid)
    }
    return out, rows.Err()
}

// isUniqueViolation: helper for pgx unique-constraint detection.
// Pattern is already used elsewhere in the file; if a helper already exists, reuse it
// (e.g. via pgxpool.PgError SQLSTATE "23505"). If not, add:
//
// func isUniqueViolation(err error) bool {
//     var pgErr *pgconn.PgError
//     return errors.As(err, &pgErr) && pgErr.Code == "23505"
// }
```

**Note for the implementer:** check whether `isUniqueViolation` (or equivalent) already exists in `store_pg.go`. If yes, use it. If not, add the helper at the bottom of the file with the appropriate import (`github.com/jackc/pgx/v5/pgconn`, `errors`).

- [ ] **Step 4: Modify Get/List to populate Refs**

In `store_pg.go`, find `Get` and `List` methods and after constructing `*Ticket`, populate `Refs`:

For `Get(ctx, id)`:
```go
// After successful scan of *Ticket t:
ids, err := s.ListRefIDsForTickets(ctx, []uuid.UUID{t.ID})
if err != nil {
    return nil, fmt.Errorf("load refs: %w", err)
}
t.Refs = ids[t.ID]
return t, nil
```

For `List(ctx, f)` (after assembling the `tickets []*Ticket` slice):
```go
if len(tickets) > 0 {
    ids := make([]uuid.UUID, 0, len(tickets))
    for _, t := range tickets {
        ids = append(ids, t.ID)
    }
    refs, err := s.ListRefIDsForTickets(ctx, ids)
    if err != nil {
        return nil, 0, fmt.Errorf("load refs: %w", err)
    }
    for _, t := range tickets {
        t.Refs = refs[t.ID]
    }
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ticket/ -run 'TestRefs_' -v`
Expected: PASS for all 10 tests.

- [ ] **Step 6: Verify no regression in existing ticket store tests**

Run: `go test ./internal/ticket/...`
Expected: PASS (all existing).

- [ ] **Step 7: Commit**

```bash
git add internal/ticket/store.go internal/ticket/store_pg.go internal/ticket/refs_store_test.go
git commit -m "feat(refs): PGStore impl + Get/List populate Refs"
```

---

## Task 5: Service — refs methods + validation

**Files:**
- Modify: `internal/ticket/service.go`
- Create: `internal/ticket/refs_service_test.go`

The service layer enforces validation rules from the spec: max 50 refs, length 1..512, no duplicates in a single request, no empty ids.

- [ ] **Step 1: Write failing service tests**

Create `internal/ticket/refs_service_test.go`:

```go
package ticket_test

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/google/uuid"
    "github.com/tojiuni/goquest/internal/ticket"
)

// newSvcWithStore returns a *ticket.Service backed by the real PG store via setupTestDB.
// Stub publisher/nuclex/xp/webhook/magic (nil-safe wrappers already exist in service_test.go).
func newSvcWithStore(t *testing.T) (*ticket.Service, ticket.Store) {
    t.Helper()
    _, store := setupTestDB(t)
    svc := ticket.NewService(store, nil, nil, nil, nil, nil)
    return svc, store
}

func seedTicket(t *testing.T, store ticket.Store) uuid.UUID {
    t.Helper()
    tk := &ticket.Ticket{
        Title:        "svc-refs",
        Type:         ticket.TypeTask,
        Status:       ticket.StatusPending,
        Priority:     ticket.PriorityNormal,
        AssigneeType: ticket.AssigneeHuman,
        RequesterID:  "test@example.com",
        Visibility:   ticket.VisibilityProject,
    }
    if err := store.Create(context.Background(), tk); err != nil {
        t.Fatalf("seed: %v", err)
    }
    return tk.ID
}

func TestSvcRefs_AddEmptyNoOp(t *testing.T) {
    svc, store := newSvcWithStore(t)
    tid := seedTicket(t, store)
    refs, err := svc.AddRefs(context.Background(), tid, nil, "u@x")
    if err != nil {
        t.Fatalf("AddRefs nil: %v", err)
    }
    if len(refs) != 0 {
        t.Errorf("want 0, got %d", len(refs))
    }
}

func TestSvcRefs_RejectDuplicateInRequest(t *testing.T) {
    svc, store := newSvcWithStore(t)
    tid := seedTicket(t, store)
    _, err := svc.AddRefs(context.Background(), tid, []string{"l1-a", "l1-a"}, "u@x")
    if !errors.Is(err, ticket.ErrRefIDsDuplicate) {
        t.Errorf("want ErrRefIDsDuplicate, got %v", err)
    }
}

func TestSvcRefs_RejectEmptyOrTooLong(t *testing.T) {
    svc, store := newSvcWithStore(t)
    tid := seedTicket(t, store)
    _, err := svc.AddRefs(context.Background(), tid, []string{""}, "u@x")
    if !errors.Is(err, ticket.ErrRefIDInvalid) {
        t.Errorf("empty id: want ErrRefIDInvalid, got %v", err)
    }
    long := strings.Repeat("a", 513)
    _, err = svc.AddRefs(context.Background(), tid, []string{long}, "u@x")
    if !errors.Is(err, ticket.ErrRefIDInvalid) {
        t.Errorf("long id: want ErrRefIDInvalid, got %v", err)
    }
}

func TestSvcRefs_EnforceLimit(t *testing.T) {
    svc, store := newSvcWithStore(t)
    tid := seedTicket(t, store)
    existing := make([]string, ticket.MaxRefsPerTicket)
    for i := range existing {
        existing[i] = strings.Repeat("a", 1) + uuid.NewString()
    }
    if _, err := svc.AddRefs(context.Background(), tid, existing, "u@x"); err != nil {
        t.Fatalf("seed 50: %v", err)
    }
    _, err := svc.AddRefs(context.Background(), tid, []string{"one-more"}, "u@x")
    if !errors.Is(err, ticket.ErrRefLimitExceeded) {
        t.Errorf("want ErrRefLimitExceeded, got %v", err)
    }
}

func TestSvcRefs_AddListRemove(t *testing.T) {
    svc, store := newSvcWithStore(t)
    tid := seedTicket(t, store)

    if _, err := svc.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x"); err != nil {
        t.Fatalf("Add: %v", err)
    }
    refs, err := svc.ListRefs(context.Background(), tid)
    if err != nil || len(refs) != 2 {
        t.Fatalf("List: %v len=%d", err, len(refs))
    }
    if err := svc.RemoveRef(context.Background(), tid, "l1-a"); err != nil {
        t.Fatalf("Remove: %v", err)
    }
    refs2, _ := svc.ListRefs(context.Background(), tid)
    if len(refs2) != 1 || refs2[0].GopediaL1ID != "l1-b" {
        t.Errorf("after remove: %+v", refs2)
    }
}

func TestSvcRefs_VisibleTicketsByRef_NoFilter(t *testing.T) {
    svc, store := newSvcWithStore(t)
    t1 := seedTicket(t, store)
    t2 := seedTicket(t, store)
    _, _ = svc.AddRefs(context.Background(), t1, []string{"l1-shared"}, "u@x")
    _, _ = svc.AddRefs(context.Background(), t2, []string{"l1-shared"}, "u@x")

    // Caller authorized to see both — pass visible=nil meaning "no SpiceDB filter, treat as unrestricted".
    ids, err := svc.ListTicketsByRef(context.Background(), "l1-shared", nil)
    if err != nil {
        t.Fatalf("ListTicketsByRef: %v", err)
    }
    if len(ids) != 2 {
        t.Errorf("want 2 ids unrestricted, got %d", len(ids))
    }
}

func TestSvcRefs_VisibleTicketsByRef_WithFilter(t *testing.T) {
    svc, store := newSvcWithStore(t)
    t1 := seedTicket(t, store)
    t2 := seedTicket(t, store)
    _, _ = svc.AddRefs(context.Background(), t1, []string{"l1-shared"}, "u@x")
    _, _ = svc.AddRefs(context.Background(), t2, []string{"l1-shared"}, "u@x")

    // Caller can only see t1.
    visible := map[uuid.UUID]bool{t1: true}
    ids, err := svc.ListTicketsByRef(context.Background(), "l1-shared", visible)
    if err != nil {
        t.Fatalf("ListTicketsByRef: %v", err)
    }
    if len(ids) != 1 || ids[0] != t1 {
        t.Errorf("want only t1, got %v", ids)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ticket/ -run 'TestSvcRefs_'`
Expected: FAIL — `Service.AddRefs` / `ListRefs` / etc. undefined.

- [ ] **Step 3: Implement Service methods**

Append to `internal/ticket/service.go`:

```go
// --- Refs ---

func (s *Service) AddRefs(ctx context.Context, ticketID uuid.UUID, gopediaL1IDs []string, createdBy string) ([]TicketRef, error) {
    if len(gopediaL1IDs) == 0 {
        return nil, nil
    }
    seen := map[string]bool{}
    for _, id := range gopediaL1IDs {
        if id == "" || len(id) > MaxRefIDLength {
            return nil, ErrRefIDInvalid
        }
        if seen[id] {
            return nil, ErrRefIDsDuplicate
        }
        seen[id] = true
    }
    existing, err := s.store.CountRefs(ctx, ticketID)
    if err != nil {
        return nil, err
    }
    if existing+len(gopediaL1IDs) > MaxRefsPerTicket {
        return nil, ErrRefLimitExceeded
    }
    return s.store.AddRefs(ctx, ticketID, gopediaL1IDs, createdBy)
}

func (s *Service) ListRefs(ctx context.Context, ticketID uuid.UUID) ([]TicketRef, error) {
    return s.store.ListRefs(ctx, ticketID)
}

func (s *Service) RemoveRef(ctx context.Context, ticketID uuid.UUID, gopediaL1ID string) error {
    return s.store.RemoveRef(ctx, ticketID, gopediaL1ID)
}

func (s *Service) ReorderRefs(ctx context.Context, ticketID uuid.UUID, orderedL1IDs []string) error {
    seen := map[string]bool{}
    for _, id := range orderedL1IDs {
        if id == "" || len(id) > MaxRefIDLength {
            return ErrRefIDInvalid
        }
        if seen[id] {
            return ErrRefIDsDuplicate
        }
        seen[id] = true
    }
    return s.store.ReorderRefs(ctx, ticketID, orderedL1IDs)
}

// ListTicketsByRef returns ticket ids that reference the given gopedia L1 doc.
// If `visible` is nil, no visibility filter is applied (caller is unrestricted —
// e.g., shadow mode with NoopChecker). If `visible` is non-nil, only ids whose
// UUID appears as a key with value true are returned.
func (s *Service) ListTicketsByRef(ctx context.Context, gopediaL1ID string, visible map[uuid.UUID]bool) ([]uuid.UUID, error) {
    if gopediaL1ID == "" || len(gopediaL1ID) > MaxRefIDLength {
        return nil, ErrRefIDInvalid
    }
    ids, err := s.store.ListTicketIDsByRef(ctx, gopediaL1ID)
    if err != nil {
        return nil, err
    }
    if visible == nil {
        return ids, nil
    }
    out := make([]uuid.UUID, 0, len(ids))
    for _, id := range ids {
        if visible[id] {
            out = append(out, id)
        }
    }
    return out, nil
}
```

Add `"github.com/google/uuid"` import if not already present.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ticket/ -run 'TestSvcRefs_' -v`
Expected: PASS for all 7 tests.

- [ ] **Step 5: Verify no regression**

Run: `go test ./internal/ticket/...`
Expected: PASS (all).

- [ ] **Step 6: Commit**

```bash
git add internal/ticket/service.go internal/ticket/refs_service_test.go
git commit -m "feat(refs): Service.AddRefs/List/Remove/Reorder/ListTicketsByRef + validation"
```

---

## Task 6: Wire ref_ids into ticket Create/Update flow

**Files:**
- Modify: `internal/ticket/service.go`

When a caller submits `POST /tickets` with `ref_ids: [...]`, the service should attach them as part of the same logical operation. Same for `PATCH /tickets/{id}` when `ref_ids` is present (replace semantics: the given list becomes the new full set).

- [ ] **Step 1: Write the failing test**

Append to `internal/ticket/refs_service_test.go`:

```go
func TestSvcRefs_CreateWithRefs(t *testing.T) {
    svc, _ := newSvcWithStore(t)
    req := ticket.CreateTicketReq{
        Title:        "with refs",
        Type:         ticket.TypeTask,
        Priority:     ticket.PriorityNormal,
        AssigneeType: ticket.AssigneeHuman,
        Visibility:   ticket.VisibilityProject,
        RefIDs:       []string{"l1-a", "l1-b"},
    }
    tk, err := svc.Create(context.Background(), req, "user@example.com")
    if err != nil {
        t.Fatalf("Create: %v", err)
    }
    if len(tk.Refs) != 2 || tk.Refs[0] != "l1-a" || tk.Refs[1] != "l1-b" {
        t.Errorf("Refs on returned ticket: %v", tk.Refs)
    }
}

func TestSvcRefs_UpdateReplacesRefs(t *testing.T) {
    svc, store := newSvcWithStore(t)
    tid := seedTicket(t, store)
    _, _ = svc.AddRefs(context.Background(), tid, []string{"l1-old"}, "u@x")

    newRefs := []string{"l1-x", "l1-y"}
    req := ticket.UpdateTicketReq{RefIDs: &newRefs}
    tk, err := svc.Update(context.Background(), tid, req)
    if err != nil {
        t.Fatalf("Update: %v", err)
    }
    if len(tk.Refs) != 2 || tk.Refs[0] != "l1-x" || tk.Refs[1] != "l1-y" {
        t.Errorf("Refs after update: %v", tk.Refs)
    }
}

func TestSvcRefs_UpdateNilLeavesRefsUntouched(t *testing.T) {
    svc, store := newSvcWithStore(t)
    tid := seedTicket(t, store)
    _, _ = svc.AddRefs(context.Background(), tid, []string{"l1-keep"}, "u@x")

    // RefIDs == nil → no change. Use some other field to drive the Update.
    pri := ticket.PriorityHigh
    req := ticket.UpdateTicketReq{Priority: &pri}
    tk, err := svc.Update(context.Background(), tid, req)
    if err != nil {
        t.Fatalf("Update: %v", err)
    }
    if len(tk.Refs) != 1 || tk.Refs[0] != "l1-keep" {
        t.Errorf("Refs must be untouched: %v", tk.Refs)
    }
}
```

**Important:** the test uses `UpdateTicketReq{RefIDs: &newRefs}`. That requires `RefIDs` on `UpdateTicketReq` to be `*[]string` (pointer to slice), so nil means "no change" and empty `[]string{}` means "clear all". In Task 2 we wrote `RefIDs []string` — adjust to `RefIDs *[]string \`json:"ref_ids,omitempty"\`` on `UpdateTicketReq` only (Create keeps plain `[]string`).

- [ ] **Step 2: Adjust UpdateTicketReq.RefIDs to *[]string**

Edit `internal/ticket/model.go` on the `UpdateTicketReq` struct: change `RefIDs []string` to `RefIDs *[]string \`json:"ref_ids,omitempty"\``.

`CreateTicketReq.RefIDs` stays as `[]string`.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/ticket/ -run 'TestSvcRefs_CreateWithRefs|TestSvcRefs_Update' -v`
Expected: FAIL — `Create`/`Update` don't yet attach refs.

- [ ] **Step 4: Implement in Service.Create**

In `internal/ticket/service.go`, find `func (s *Service) Create(ctx, req, requesterID)`. After the existing logic that creates the ticket and before returning, add:

```go
// Attach optional ref docs.
if len(req.RefIDs) > 0 {
    if _, err := s.AddRefs(ctx, created.ID, req.RefIDs, requesterID); err != nil {
        // Best-effort rollback: delete the freshly created ticket so we don't
        // leave a half-attached ticket. If deletion fails, we surface the
        // original ref error — the ticket remains and operator can clean up.
        _ = s.store.Cancel(ctx, created.ID) // or whichever delete primitive exists
        return nil, err
    }
    // Re-fetch to get Refs populated by Get's join.
    refreshed, gerr := s.store.Get(ctx, created.ID)
    if gerr == nil {
        created = refreshed
    }
}
return created, nil
```

**Note for implementer:** check what cleanup primitive exists for a freshly-created ticket. If `store.Cancel` is not appropriate (it transitions to cancelled, not deletes), use a direct `DELETE FROM tickets WHERE id=$1` via a new store method `DeleteForRollback` or accept that the ticket stays and just bubble the ref error. Simpler choice: **don't roll back** — the validation in Step 1 of `Service.AddRefs` (length, dup, count) catches most user errors before any DB write. A late failure (DB down) results in a ticket without refs, which is acceptable (ref is optional).

**Simpler implementation choice (preferred):** validate first, then write the ticket only after validation succeeds; if `Add` then fails due to DB issues, surface the error and let the ticket stay (refs are optional, no inconsistency).

```go
// Pre-validate refs before creating the ticket.
if len(req.RefIDs) > 0 {
    if err := validateRefIDs(req.RefIDs); err != nil {
        return nil, err
    }
}
// ... existing ticket creation logic produces `created` ...
if len(req.RefIDs) > 0 {
    if _, err := s.store.AddRefs(ctx, created.ID, req.RefIDs, requesterID); err != nil {
        return nil, fmt.Errorf("attach refs: %w", err)
    }
    created.Refs = append([]string(nil), req.RefIDs...)
}
return created, nil
```

Add the helper at the bottom of `service.go`:

```go
func validateRefIDs(ids []string) error {
    seen := map[string]bool{}
    for _, id := range ids {
        if id == "" || len(id) > MaxRefIDLength {
            return ErrRefIDInvalid
        }
        if seen[id] {
            return ErrRefIDsDuplicate
        }
        seen[id] = true
    }
    if len(ids) > MaxRefsPerTicket {
        return ErrRefLimitExceeded
    }
    return nil
}
```

Refactor `Service.AddRefs` to use `validateRefIDs` (replacing the inline dedup loop).

- [ ] **Step 5: Implement in Service.Update**

In `Service.Update`, after the existing update path produces an updated `*Ticket`, add:

```go
if req.RefIDs != nil {
    newIDs := *req.RefIDs
    if err := validateRefIDs(newIDs); err != nil {
        return nil, err
    }
    // Replace semantics: clear existing then add. Done in a single transaction
    // at the store level via a new method `ReplaceRefs`. If that method is not
    // present, the simplest correct implementation is: delete-all + add-all.
    if err := s.replaceRefs(ctx, id, newIDs, /* createdBy */ ""); err != nil {
        return nil, fmt.Errorf("replace refs: %w", err)
    }
    refreshed, gerr := s.store.Get(ctx, id)
    if gerr == nil {
        updated = refreshed
    }
}
return updated, nil
```

Add `replaceRefs` helper on Service:

```go
func (s *Service) replaceRefs(ctx context.Context, ticketID uuid.UUID, newIDs []string, createdBy string) error {
    existing, err := s.store.ListRefs(ctx, ticketID)
    if err != nil {
        return err
    }
    for _, r := range existing {
        if err := s.store.RemoveRef(ctx, ticketID, r.GopediaL1ID); err != nil {
            return err
        }
    }
    if len(newIDs) > 0 {
        if _, err := s.store.AddRefs(ctx, ticketID, newIDs, createdBy); err != nil {
            return err
        }
    }
    return nil
}
```

**Note:** `createdBy` for update-time refs ideally comes from the request actor. For now we accept `""` (treated as system) — Task 11 will plumb the actor through. Don't block on it here.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/ticket/ -run 'TestSvcRefs_CreateWithRefs|TestSvcRefs_Update' -v`
Expected: PASS for all 3.

- [ ] **Step 7: Verify no regression**

Run: `go test ./internal/ticket/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/ticket/model.go internal/ticket/service.go internal/ticket/refs_service_test.go
git commit -m "feat(refs): Create/Update accept ref_ids (replace semantics on Update)"
```

---

## Task 7: HTTP handlers — ticket-scoped refs endpoints

**Files:**
- Modify: `internal/handler/ticket.go` (add 4 methods on `TicketHandler`)
- Create: `internal/handler/refs_test.go`

All 4 endpoints sit under `/api/v1/tickets/{id}/refs*` and are gated by the existing `RequireTicketView` middleware (per spec §5).

- [ ] **Step 1: Write failing handler tests**

Create `internal/handler/refs_test.go`. Follow the pattern in `authz_e2e_test.go` (real chi router + real store + stub spicedb checker).

```go
package handler_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/tojiuni/goquest/internal/auth"
    "github.com/tojiuni/goquest/internal/handler"
    "github.com/tojiuni/goquest/internal/spicedb"
    "github.com/tojiuni/goquest/internal/ticket"
)

// helpers (newTestRouter, newTestTicket, withOIDCSubject) are reused from
// authz_e2e_test.go — pull them out into a shared *_helpers_test.go if needed,
// or duplicate the small bits here. Do not redefine if they already exist
// in the package's _test files.

func TestRefsHandler_AddAndList(t *testing.T) {
    svc, _, srv := newTestRouter(t) // returns service, checker, *httptest.Server
    defer srv.Close()

    tk := newTestTicket(t, svc, "alice@example.com")

    // POST /api/v1/tickets/{id}/refs
    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-a", "l1-b"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    res, err := srv.Client().Do(req)
    if err != nil {
        t.Fatalf("POST: %v", err)
    }
    if res.StatusCode != http.StatusOK {
        t.Fatalf("POST status: want 200, got %d", res.StatusCode)
    }

    // GET /api/v1/tickets/{id}/refs
    getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", nil)
    withOIDCSubject(getReq, "alice@example.com")
    getRes, _ := srv.Client().Do(getReq)
    if getRes.StatusCode != http.StatusOK {
        t.Fatalf("GET status: %d", getRes.StatusCode)
    }
    var got struct {
        Refs []ticket.TicketRef `json:"refs"`
    }
    _ = json.NewDecoder(getRes.Body).Decode(&got)
    if len(got.Refs) != 2 {
        t.Errorf("want 2 refs, got %d", len(got.Refs))
    }
}

func TestRefsHandler_AddRequiresAuth(t *testing.T) {
    _, _, srv := newTestRouter(t)
    defer srv.Close()
    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-a"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+uuid.New().String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusUnauthorized {
        t.Errorf("want 401, got %d", res.StatusCode)
    }
}

func TestRefsHandler_AddDeniedWithoutViewPermission(t *testing.T) {
    svc, checker, srv := newTestRouter(t)
    defer srv.Close()
    checker.DenyAll() // stub helper that makes CanViewTicket return false

    tk := newTestTicket(t, svc, "alice@example.com")
    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-a"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "bob@example.com") // not the requester
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusForbidden {
        t.Errorf("want 403, got %d", res.StatusCode)
    }
}

func TestRefsHandler_DeleteIdempotent(t *testing.T) {
    svc, _, srv := newTestRouter(t)
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com")

    // First DELETE on non-existent ref → 204.
    req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs/l1-never", nil)
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusNoContent {
        t.Errorf("want 204, got %d", res.StatusCode)
    }
}

func TestRefsHandler_Reorder(t *testing.T) {
    svc, _, srv := newTestRouter(t)
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), tk.ID, []string{"l1-a", "l1-b", "l1-c"}, "alice@example.com")

    body, _ := json.Marshal(map[string]any{"ordered_l1_ids": []string{"l1-c", "l1-a", "l1-b"}})
    req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs/reorder", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Errorf("want 200, got %d", res.StatusCode)
    }
}

func TestRefsHandler_AddRejectsDuplicates(t *testing.T) {
    svc, _, srv := newTestRouter(t)
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com")

    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-a", "l1-a"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusBadRequest {
        t.Errorf("want 400, got %d", res.StatusCode)
    }
}

func TestRefsHandler_AddRejectsConflict(t *testing.T) {
    svc, _, srv := newTestRouter(t)
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), tk.ID, []string{"l1-x"}, "alice@example.com")

    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-x"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusConflict {
        t.Errorf("want 409, got %d", res.StatusCode)
    }
}

// Shared imports used implicitly — ensure they resolve.
var _ = chi.NewRouter
var _ = httptest.NewServer
var _ auth.PermissionChecker = (*spicedb.NoopChecker)(nil)
```

**Note for implementer:** the test file refers to helper functions `newTestRouter`, `newTestTicket`, `withOIDCSubject`, `reqCtx`, and a `checker.DenyAll()` API. If these exist already in `authz_e2e_test.go`, reuse them (move to a shared `helpers_test.go` if needed). If `checker.DenyAll()` doesn't exist, add a small toggle to the stub used in `authz_e2e_test.go`. Don't invent a new helpers framework — match the established pattern exactly.

- [ ] **Step 2: Run tests to verify they fail (compile errors expected)**

Run: `go test ./internal/handler/ -run 'TestRefsHandler_' -v`
Expected: FAIL — `(*TicketHandler).ListRefs/AddRefs/RemoveRef/ReorderRefs` undefined.

- [ ] **Step 3: Implement handler methods**

In `internal/handler/ticket.go`, append 4 methods on `*TicketHandler`:

```go
// ListRefs handles GET /api/v1/tickets/{id}/refs.
func (h *TicketHandler) ListRefs(w http.ResponseWriter, r *http.Request) {
    tk := auth.TicketFrom(r)
    if tk == nil {
        writeError(w, http.StatusInternalServerError, "ticket context missing")
        return
    }
    refs, err := h.svc.ListRefs(r.Context(), tk.ID)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "list refs: "+err.Error())
        return
    }
    writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
}

// AddRefs handles POST /api/v1/tickets/{id}/refs.
// Body: {"ref_ids": ["l1-a", ...]}
func (h *TicketHandler) AddRefs(w http.ResponseWriter, r *http.Request) {
    tk := auth.TicketFrom(r)
    if tk == nil {
        writeError(w, http.StatusInternalServerError, "ticket context missing")
        return
    }
    var body struct {
        RefIDs []string `json:"ref_ids"`
    }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeError(w, http.StatusBadRequest, "invalid body")
        return
    }
    if len(body.RefIDs) == 0 {
        writeJSON(w, http.StatusOK, map[string]any{"refs": []ticket.TicketRef{}})
        return
    }
    actor := auth.RequesterID(r)
    refs, err := h.svc.AddRefs(r.Context(), tk.ID, body.RefIDs, actor)
    if err != nil {
        switch {
        case errors.Is(err, ticket.ErrRefIDInvalid):
            writeError(w, http.StatusBadRequest, "invalid ref id (empty or >512 chars)")
        case errors.Is(err, ticket.ErrRefIDsDuplicate):
            writeError(w, http.StatusBadRequest, "duplicate ref id in request")
        case errors.Is(err, ticket.ErrRefLimitExceeded):
            writeError(w, http.StatusUnprocessableEntity, "max 50 refs per ticket")
        case errors.Is(err, ticket.ErrRefConflict):
            writeError(w, http.StatusConflict, "ref already exists for this ticket")
        default:
            writeError(w, http.StatusInternalServerError, "add refs: "+err.Error())
        }
        return
    }
    writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
}

// RemoveRef handles DELETE /api/v1/tickets/{id}/refs/{l1_id}.
func (h *TicketHandler) RemoveRef(w http.ResponseWriter, r *http.Request) {
    tk := auth.TicketFrom(r)
    if tk == nil {
        writeError(w, http.StatusInternalServerError, "ticket context missing")
        return
    }
    l1 := chi.URLParam(r, "l1_id")
    if l1 == "" {
        writeError(w, http.StatusBadRequest, "missing l1_id")
        return
    }
    if err := h.svc.RemoveRef(r.Context(), tk.ID, l1); err != nil {
        writeError(w, http.StatusInternalServerError, "remove ref: "+err.Error())
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

// ReorderRefs handles PATCH /api/v1/tickets/{id}/refs/reorder.
// Body: {"ordered_l1_ids": ["l1-c", "l1-a", "l1-b"]}
func (h *TicketHandler) ReorderRefs(w http.ResponseWriter, r *http.Request) {
    tk := auth.TicketFrom(r)
    if tk == nil {
        writeError(w, http.StatusInternalServerError, "ticket context missing")
        return
    }
    var body struct {
        OrderedL1IDs []string `json:"ordered_l1_ids"`
    }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeError(w, http.StatusBadRequest, "invalid body")
        return
    }
    if err := h.svc.ReorderRefs(r.Context(), tk.ID, body.OrderedL1IDs); err != nil {
        switch {
        case errors.Is(err, ticket.ErrRefIDInvalid):
            writeError(w, http.StatusBadRequest, "invalid ref id")
        case errors.Is(err, ticket.ErrRefIDsDuplicate):
            writeError(w, http.StatusBadRequest, "duplicate ref id")
        default:
            writeError(w, http.StatusBadRequest, "reorder: "+err.Error())
        }
        return
    }
    refs, _ := h.svc.ListRefs(r.Context(), tk.ID)
    writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
}
```

Add `"errors"` import if missing. `writeJSON`/`writeError` are existing helpers in `internal/handler/`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/handler/ -run 'TestRefsHandler_' -v`
Expected: PASS for all 6 tests.

(Note: until Task 9 wires the routes, the test router needs to register these handlers explicitly. Adjust `newTestRouter` in the test to register the new routes under `/tickets/{id}/refs*` with `RequireTicketView`, mirroring what Task 9 will do in `main.go`.)

- [ ] **Step 5: Commit**

```bash
git add internal/handler/ticket.go internal/handler/refs_test.go
git commit -m "feat(refs): ticket-scoped HTTP handlers (List/Add/Remove/Reorder)"
```

---

## Task 8: Reverse-lookup handler — GET /api/v1/refs

**Files:**
- Modify: `internal/handler/ticket.go`
- Modify: `internal/handler/refs_test.go`

The reverse-lookup endpoint is NOT under `/tickets/{id}`; it's a top-level resource. Authorization: the caller must be authenticated (any OIDC user); we then filter results by their SpiceDB-visible ticket set.

- [ ] **Step 1: Write failing tests**

Append to `internal/handler/refs_test.go`:

```go
func TestRefsHandler_ReverseLookup_UnrestrictedShadow(t *testing.T) {
    svc, _, srv := newTestRouter(t) // shadow uses NoopChecker → unrestricted
    defer srv.Close()
    t1 := newTestTicket(t, svc, "alice@example.com")
    t2 := newTestTicket(t, svc, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), t1.ID, []string{"l1-shared"}, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), t2.ID, []string{"l1-shared"}, "alice@example.com")

    req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/refs?gopedia_l1_id=l1-shared", nil)
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Fatalf("status: %d", res.StatusCode)
    }
    var got struct {
        TicketIDs []string `json:"ticket_ids"`
    }
    _ = json.NewDecoder(res.Body).Decode(&got)
    if len(got.TicketIDs) != 2 {
        t.Errorf("want 2, got %d", len(got.TicketIDs))
    }
}

func TestRefsHandler_ReverseLookup_FilteredByVisibility(t *testing.T) {
    svc, checker, srv := newTestRouter(t)
    defer srv.Close()
    t1 := newTestTicket(t, svc, "alice@example.com")
    t2 := newTestTicket(t, svc, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), t1.ID, []string{"l1-shared"}, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), t2.ID, []string{"l1-shared"}, "alice@example.com")

    // Configure checker so bob sees t1 only.
    checker.SetVisibleResources(spicedb.TypeTicket, "bob@example.com", []string{t1.ID.String()})

    req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/refs?gopedia_l1_id=l1-shared", nil)
    withOIDCSubject(req, "bob@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Fatalf("status: %d", res.StatusCode)
    }
    var got struct {
        TicketIDs []string `json:"ticket_ids"`
    }
    _ = json.NewDecoder(res.Body).Decode(&got)
    if len(got.TicketIDs) != 1 || got.TicketIDs[0] != t1.ID.String() {
        t.Errorf("want only t1, got %v", got.TicketIDs)
    }
}

func TestRefsHandler_ReverseLookup_RequiresGopediaL1ID(t *testing.T) {
    _, _, srv := newTestRouter(t)
    defer srv.Close()
    req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/refs", nil)
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusBadRequest {
        t.Errorf("want 400, got %d", res.StatusCode)
    }
}

func TestRefsHandler_ReverseLookup_NoLeak(t *testing.T) {
    svc, checker, srv := newTestRouter(t)
    defer srv.Close()
    t1 := newTestTicket(t, svc, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), t1.ID, []string{"l1-secret"}, "alice@example.com")
    checker.SetVisibleResources(spicedb.TypeTicket, "bob@example.com", []string{})

    req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/refs?gopedia_l1_id=l1-secret", nil)
    withOIDCSubject(req, "bob@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Fatalf("status: %d", res.StatusCode)
    }
    var got struct {
        TicketIDs []string `json:"ticket_ids"`
    }
    _ = json.NewDecoder(res.Body).Decode(&got)
    if len(got.TicketIDs) != 0 {
        t.Errorf("must not leak ticket ids, got %v", got.TicketIDs)
    }
}
```

`checker.SetVisibleResources(...)` is a new stub helper — extend the existing test checker in `authz_e2e_test.go` (or wherever it lives) to support per-user visible ID overrides.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/handler/ -run 'TestRefsHandler_ReverseLookup' -v`
Expected: FAIL — handler method undefined.

- [ ] **Step 3: Implement the reverse-lookup handler**

Decision: where does the reverse-lookup handler live? Options:
1. On `*TicketHandler` as `ReverseLookupRefs`.
2. On a new dedicated `RefsHandler` type.

Pick (1) — `TicketHandler` already owns `Service` and `Checker`. Append to `internal/handler/ticket.go`:

```go
// ReverseLookupRefs handles GET /api/v1/refs?gopedia_l1_id=X.
// Returns ticket ids that reference the doc, filtered by the caller's SpiceDB-visible set.
func (h *TicketHandler) ReverseLookupRefs(w http.ResponseWriter, r *http.Request) {
    docID := r.URL.Query().Get("gopedia_l1_id")
    if docID == "" {
        writeError(w, http.StatusBadRequest, "gopedia_l1_id is required")
        return
    }
    if len(docID) > ticket.MaxRefIDLength {
        writeError(w, http.StatusBadRequest, "gopedia_l1_id too long")
        return
    }

    userID := auth.RequesterID(r)
    visibleIDs, unrestricted, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_project", userID)
    if err != nil {
        writeError(w, http.StatusInternalServerError, "visibility check")
        return
    }

    var visible map[uuid.UUID]bool
    if !unrestricted {
        visible = make(map[uuid.UUID]bool, len(visibleIDs))
        for _, s := range visibleIDs {
            if id, perr := uuid.Parse(s); perr == nil {
                visible[id] = true
            }
        }
    }
    ids, err := h.svc.ListTicketsByRef(r.Context(), docID, visible)
    if err != nil {
        if errors.Is(err, ticket.ErrRefIDInvalid) {
            writeError(w, http.StatusBadRequest, "invalid gopedia_l1_id")
            return
        }
        writeError(w, http.StatusInternalServerError, "lookup: "+err.Error())
        return
    }
    strIDs := make([]string, 0, len(ids))
    for _, id := range ids {
        strIDs = append(strIDs, id.String())
    }
    writeJSON(w, http.StatusOK, map[string]any{"ticket_ids": strIDs})
}
```

Imports may need: `"github.com/tojiuni/goquest/internal/spicedb"`, `"github.com/google/uuid"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/handler/ -run 'TestRefsHandler_ReverseLookup' -v`
Expected: PASS for all 4.

- [ ] **Step 5: Commit**

```bash
git add internal/handler/ticket.go internal/handler/refs_test.go
git commit -m "feat(refs): GET /api/v1/refs reverse-lookup with SpiceDB visibility filter"
```

---

## Task 9: Wire routes in main.go

**Files:**
- Modify: `cmd/service/main.go`

- [ ] **Step 1: Add the routes**

In `cmd/service/main.go`, find the block (around line 237–254):

```go
r.Route("/tickets/{id}", func(r chi.Router) {
    r.Use(requireTicketView)
    r.Get("/", ticketH.Get)
    r.Patch("/", ticketH.Update)
    r.Delete("/", ticketH.Cancel)
    r.Post("/approve", ticketH.Approve)
    r.Get("/subtasks", ticketH.ListSubtasks)
    r.Post("/attempts", ticketH.SubmitAttempt)
    r.Get("/attempts", ticketH.ListAttempts)
    // comments ...
})
```

Append inside the `r.Route("/tickets/{id}", ...)` block, right after `r.Get("/attempts", ticketH.ListAttempts)`:

```go
    r.Get("/refs", ticketH.ListRefs)
    r.Post("/refs", ticketH.AddRefs)
    r.Delete("/refs/{l1_id}", ticketH.RemoveRef)
    r.Patch("/refs/reorder", ticketH.ReorderRefs)
```

Then, immediately after the `r.Route("/tickets/{id}", ...)` block closes (still inside the authenticated `/api/v1` group), add:

```go
r.Get("/refs", ticketH.ReverseLookupRefs)
```

- [ ] **Step 2: Build and run service smoke test**

Run: `go build ./...`
Expected: success.

Run: `go test ./internal/handler/ -run 'TestRefsHandler_' -v`
Expected: PASS — including any test that relies on the full router from `main.go` style wiring.

- [ ] **Step 3: Commit**

```bash
git add cmd/service/main.go
git commit -m "feat(refs): wire /tickets/{id}/refs* and /refs routes"
```

---

## Task 10: E2E test — full lifecycle, visibility, CASCADE, regressions

**Files:**
- Create: `internal/handler/refs_e2e_test.go`

The E2E test replicates the full chi stack with real Postgres and a configurable SpiceDB checker stub (same one used in `authz_e2e_test.go`). It covers spec §7 scenarios 1, 2, 3, 7, 8.

(Scenarios 4–6 — bot payload / fetch failures / gopedia-down — are bot-side concerns and live in the goquest-bot repo; we don't replicate them here. The spec's scenario 4 about "bot payload" reduces, in this repo, to verifying that GET /tickets/{id} returns `ref_ids` correctly, which is already covered. We add an explicit assertion below.)

- [ ] **Step 1: Write the E2E test**

Create `internal/handler/refs_e2e_test.go`:

```go
package handler_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "testing"

    "github.com/tojiuni/goquest/internal/ticket"
)

func TestRefsE2E_Lifecycle(t *testing.T) {
    svc, _, srv := newTestRouter(t)
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com")

    // 1. POST refs
    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-a", "l1-b"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Fatalf("POST refs: %d", res.StatusCode)
    }

    // 2. GET /tickets/{id} → assert ref_ids in body (this is what bot/orchestrator sees).
    getReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/tickets/"+tk.ID.String(), nil)
    withOIDCSubject(getReq, "alice@example.com")
    getRes, _ := srv.Client().Do(getReq)
    var fetched ticket.Ticket
    _ = json.NewDecoder(getRes.Body).Decode(&fetched)
    if len(fetched.Refs) != 2 || fetched.Refs[0] != "l1-a" || fetched.Refs[1] != "l1-b" {
        t.Errorf("GET /tickets/{id} must return Refs: %v", fetched.Refs)
    }

    // 3. GET /refs?gopedia_l1_id=l1-a → contains this ticket.
    revReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/refs?gopedia_l1_id=l1-a", nil)
    withOIDCSubject(revReq, "alice@example.com")
    revRes, _ := srv.Client().Do(revReq)
    var rev struct {
        TicketIDs []string `json:"ticket_ids"`
    }
    _ = json.NewDecoder(revRes.Body).Decode(&rev)
    found := false
    for _, id := range rev.TicketIDs {
        if id == tk.ID.String() {
            found = true
            break
        }
    }
    if !found {
        t.Errorf("reverse lookup missing ticket: %v", rev.TicketIDs)
    }

    // 4. DELETE one ref.
    delReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs/l1-a", nil)
    withOIDCSubject(delReq, "alice@example.com")
    delRes, _ := srv.Client().Do(delReq)
    if delRes.StatusCode != http.StatusNoContent {
        t.Errorf("DELETE: %d", delRes.StatusCode)
    }

    // 5. GET refs → only l1-b remains.
    listReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", nil)
    withOIDCSubject(listReq, "alice@example.com")
    listRes, _ := srv.Client().Do(listReq)
    var listed struct {
        Refs []ticket.TicketRef `json:"refs"`
    }
    _ = json.NewDecoder(listRes.Body).Decode(&listed)
    if len(listed.Refs) != 1 || listed.Refs[0].GopediaL1ID != "l1-b" {
        t.Errorf("after delete: %v", listed.Refs)
    }
}

func TestRefsE2E_AuthzSeparation_GopediaACLNotChecked(t *testing.T) {
    // The point: goquest does NOT check whether the caller can read the doc in gopedia.
    // ref registration succeeds even for arbitrary, possibly-private, possibly-nonexistent IDs.
    svc, _, srv := newTestRouter(t)
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com")

    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-private-secret", "l1-does-not-exist"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Errorf("authz separation: should succeed regardless of gopedia status, got %d", res.StatusCode)
    }
}

func TestRefsE2E_ReverseLookupNoLeakAcrossUsers(t *testing.T) {
    svc, checker, srv := newTestRouter(t)
    defer srv.Close()
    aliceTicket := newTestTicket(t, svc, "alice@example.com")
    _, _ = svc.AddRefs(reqCtx(), aliceTicket.ID, []string{"l1-shared"}, "alice@example.com")

    // Bob can see no tickets.
    checker.SetVisibleResources("ticket", "bob@example.com", []string{})

    req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/refs?gopedia_l1_id=l1-shared", nil)
    withOIDCSubject(req, "bob@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Fatalf("status: %d", res.StatusCode)
    }
    var got struct {
        TicketIDs []string `json:"ticket_ids"`
    }
    _ = json.NewDecoder(res.Body).Decode(&got)
    if len(got.TicketIDs) != 0 {
        t.Errorf("LEAK: bob sees %v", got.TicketIDs)
    }
}

// NOTE: SQL-level CASCADE is covered by TestRefs_CascadeOnTicketDelete in refs_store_test.go.
// Hard delete of tickets is not part of the public HTTP API (Cancel transitions status,
// it does not DELETE the row), so there is no separate E2E test here.

func TestRefsE2E_OrgAxisCompat_NoRegression(t *testing.T) {
    // Sanity: ref add/list/lookup works in a ticket that has an org_id workspace
    // (Plan1 dual-write). Boundary test — the new feature doesn't reach into
    // workspace org_id but we want to be sure existing flows are unaffected.
    svc, _, srv := newTestRouter(t)
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com") // assume helper creates with workspace+project as needed
    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-x"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    res, _ := srv.Client().Do(req)
    if res.StatusCode != http.StatusOK {
        t.Errorf("ref add must succeed on org-axis ticket, got %d", res.StatusCode)
    }
}
```

- [ ] **Step 2: Run E2E test**

Run: `go test ./internal/handler/ -run 'TestRefsE2E_' -v`
Expected: PASS for all (the CASCADE one is skipped per its own logic — store-level test in Task 4 covers it).

- [ ] **Step 3: Commit**

```bash
git add internal/handler/refs_e2e_test.go
git commit -m "test(refs): E2E lifecycle + authz separation + no-leak + regression"
```

---

## Task 11: Audit logging for ref add/remove

**Files:**
- Modify: `internal/handler/ticket.go` (call into audit)
- (Optionally) `internal/handler/ticket.go` constructor wiring if audit logger isn't already passed in

Per spec §6, ref add/remove are recorded via the existing audit logger.

- [ ] **Step 1: Check whether TicketHandler already receives an audit.Logger**

Run: `grep -n "audit\." internal/handler/ticket.go | head -10`

If the TicketHandler already has an `audit` field, use it. If not, the simplest path is to extend the constructor and the `cmd/service/main.go` wiring to pass it in.

- [ ] **Step 2: Add audit calls in AddRefs and RemoveRef**

Inside `AddRefs`, after the successful `s.svc.AddRefs(...)` call:

```go
if h.audit != nil {
    for _, r := range refs {
        h.audit.Record(r.Context(), actor, "ref.add", "ticket", tk.ID.String(), map[string]any{
            "gopedia_l1_id": r.GopediaL1ID,
            "position":      r.Position,
        })
    }
}
```

Inside `RemoveRef`, after successful `s.svc.RemoveRef(...)`:

```go
if h.audit != nil {
    h.audit.Record(r.Context(), auth.RequesterID(r), "ref.remove", "ticket", tk.ID.String(), map[string]any{
        "gopedia_l1_id": l1,
    })
}
```

- [ ] **Step 3: Add a handler test that verifies audit entries (optional but cheap)**

Append to `refs_test.go`:

```go
func TestRefsHandler_AuditOnAdd(t *testing.T) {
    svc, _, srv := newTestRouter(t) // make sure audit logger is wired in newTestRouter
    defer srv.Close()
    tk := newTestTicket(t, svc, "alice@example.com")

    body, _ := json.Marshal(map[string]any{"ref_ids": []string{"l1-a"}})
    req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/tickets/"+tk.ID.String()+"/refs", bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    withOIDCSubject(req, "alice@example.com")
    _, _ = srv.Client().Do(req)

    entries, _ := getAuditLog(t).List(reqCtx(), audit.ListFilter{Action: "ref.add", TargetID: tk.ID.String()})
    if len(entries) != 1 {
        t.Errorf("audit entries: want 1, got %d", len(entries))
    }
}
```

`getAuditLog(t)` returns whichever audit Logger the test router wired. If the existing test harness doesn't expose it, plumb a small accessor — keep it test-internal.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/handler/ -run 'TestRefsHandler_Audit' -v`
Expected: PASS.

- [ ] **Step 5: Final full suite run**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/ticket.go internal/handler/refs_test.go
git commit -m "feat(refs): audit log for ref add/remove"
```

---

## Task 12: Update TEST_REVIEW.md and finalize

**Files:**
- Modify: `TEST_REVIEW.md`

- [ ] **Step 1: Add a refs entry to TEST_REVIEW.md**

Append (or merge into the existing structure) a section noting:

> **Refs feature (2026-05-30):**
> - Store: `internal/ticket/refs_store_test.go` (real PG, CASCADE, append position, reorder, conflict)
> - Service: `internal/ticket/refs_service_test.go` (limit 50, dup rejection, empty/long id rejection, Create/Update integration, visibility filter)
> - HTTP: `internal/handler/refs_test.go` (auth required, view-gate, duplicate body 400, conflict 409, idempotent delete)
> - E2E: `internal/handler/refs_e2e_test.go` (full lifecycle, authz separation from gopedia, no cross-user leak, org-axis regression)
> - Out of scope (other repos): bot fetch from gopedia, gopedia-side UI/route changes.

- [ ] **Step 2: Verify the full suite one more time**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add TEST_REVIEW.md
git commit -m "docs(test-review): record refs coverage"
```

---

## Done

After Task 12 completes, the feature is end-to-end implemented with:
- DB migration applied
- Store / Service / Handler layers covered by tests
- HTTP routes wired in `main.go`
- Reverse lookup with SpiceDB visibility filter
- Audit log records actor/action
- TEST_REVIEW.md updated

The corresponding downstream work (gopedia UI for backlinks, bot fetch loop) lives in those repos and is explicitly out of scope.
