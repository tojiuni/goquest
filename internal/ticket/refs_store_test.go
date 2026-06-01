package ticket_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tojiuni/goquest/internal/migrate"
	"github.com/tojiuni/goquest/internal/ticket"
)

// setupTestDB returns a *pgxpool.Pool connected to the test database and a
// fresh *pgStore-backed ticket.Store. Migrations are applied (idempotently)
// and any prior test rows are wiped so each test starts from a clean slate
// for tickets/ticket_refs.
//
// The DSN is read from GOQUEST_TEST_DATABASE_URL. If unset (or the database is
// unreachable), the test is skipped — these are integration tests against real
// Postgres and CI/local-dev provisions the DB out of band. This matches the
// repo's hermetic-unit-tests-only invariant: unit suites must not require a DB.
func setupTestDB(t *testing.T) (*pgxpool.Pool, ticket.Store) {
	t.Helper()

	dsn := os.Getenv("GOQUEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("GOQUEST_TEST_DATABASE_URL not set; skipping PG integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("connect test DB (%s): %v", dsn, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("ping test DB: %v", err)
	}
	if err := migrate.Run(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate: %v", err)
	}
	// Best-effort wipe of the tables touched by these tests. ticket_refs is
	// CASCADE'd by tickets, so the tickets truncate also clears ticket_refs;
	// the explicit DELETE is a belt-and-braces guard if migration order shifts.
	if _, err := pool.Exec(ctx, `DELETE FROM ticket_refs`); err != nil {
		t.Fatalf("wipe ticket_refs: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tickets`); err != nil {
		t.Fatalf("wipe tickets: %v", err)
	}

	t.Cleanup(func() { pool.Close() })
	return pool, ticket.NewPGStore(pool)
}

func makeTicketForRefs(t *testing.T, store ticket.Store) uuid.UUID {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
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
	if _, err := store.AddRefs(context.Background(), tid, []string{"l1-a"}, "u@x"); err != nil {
		t.Fatalf("AddRefs: %v", err)
	}

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
	if _, err := store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x"); err != nil {
		t.Fatalf("seed: %v", err)
	}
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
	if _, err := store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b", "l1-c"}, "u@x"); err != nil {
		t.Fatalf("seed: %v", err)
	}

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
	if _, err := store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := store.ReorderRefs(context.Background(), tid, []string{"l1-a", "l1-c"})
	if err == nil {
		t.Errorf("want error when reorder ids don't match existing set")
	}
}

func TestRefs_Count(t *testing.T) {
	_, store := setupTestDB(t)
	tid := makeTicketForRefs(t, store)
	if _, err := store.AddRefs(context.Background(), tid, []string{"l1-a", "l1-b"}, "u@x"); err != nil {
		t.Fatalf("seed: %v", err)
	}
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
	if _, err := store.AddRefs(context.Background(), t1, []string{"l1-shared"}, "u@x"); err != nil {
		t.Fatalf("seed t1: %v", err)
	}
	if _, err := store.AddRefs(context.Background(), t2, []string{"l1-shared"}, "u@x"); err != nil {
		t.Fatalf("seed t2: %v", err)
	}
	if _, err := store.AddRefs(context.Background(), t3, []string{"l1-other"}, "u@x"); err != nil {
		t.Fatalf("seed t3: %v", err)
	}

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
	if _, err := store.AddRefs(context.Background(), tid, []string{"l1-a"}, "u@x"); err != nil {
		t.Fatalf("seed: %v", err)
	}
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
	if _, err := store.AddRefs(context.Background(), t1, []string{"l1-a", "l1-b"}, "u@x"); err != nil {
		t.Fatalf("seed t1: %v", err)
	}

	m, err := store.ListRefIDsForTickets(context.Background(), []uuid.UUID{t1, t2})
	if err != nil {
		t.Fatalf("ListRefIDsForTickets: %v", err)
	}
	if len(m[t1]) != 2 || m[t1][0] != "l1-a" || m[t1][1] != "l1-b" {
		t.Errorf("t1 refs: %v", m[t1])
	}
	if _, ok := m[t2]; ok && len(m[t2]) > 0 {
		t.Errorf("t2 should not be present (or be empty), got %v", m[t2])
	}
}

// seedTicketWithParent creates a ticket with an optional parent and returns the
// full *Ticket (including the assigned ID). Used by TestListReturnsChildCount.
// It extends the simpler seedTicket (refs_service_test.go) with title and
// parent support so child-count tests can build a real hierarchy.
func seedTicketWithParent(t *testing.T, store ticket.Store, title string, parentID *uuid.UUID) *ticket.Ticket {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        title,
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
		ParentID:     parentID,
	}
	if err := store.Create(context.Background(), tk); err != nil {
		t.Fatalf("seed ticket %q: %v", title, err)
	}
	return tk
}

func TestListReturnsChildCount(t *testing.T) {
	ctx := context.Background()
	_, store := setupTestDB(t)

	// Root with two direct children; one child also has one grandchild.
	root := seedTicketWithParent(t, store, "root", nil)
	c1 := seedTicketWithParent(t, store, "c1", &root.ID)
	_ = seedTicketWithParent(t, store, "c2", &root.ID)
	_ = seedTicketWithParent(t, store, "gc1", &c1.ID)

	// Leaf root: no children at all.
	leafRoot := seedTicketWithParent(t, store, "leafRoot", nil)

	tickets, _, err := store.List(ctx, ticket.ListTicketsFilter{TopLevelOnly: true, Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[uuid.UUID]int{}
	for _, tk := range tickets {
		got[tk.ID] = tk.ChildCount
	}
	if got[root.ID] != 2 {
		t.Errorf("root: want child_count=2, got %d", got[root.ID])
	}
	if got[leafRoot.ID] != 0 {
		t.Errorf("leafRoot: want child_count=0, got %d", got[leafRoot.ID])
	}

	// Intermediate node c1 — fetched via ListSubtasks should also carry child_count.
	sub, err := store.ListSubtasks(ctx, root.ID)
	if err != nil {
		t.Fatalf("subtasks: %v", err)
	}
	subGot := map[uuid.UUID]int{}
	for _, tk := range sub {
		subGot[tk.ID] = tk.ChildCount
	}
	if subGot[c1.ID] != 1 {
		t.Errorf("c1 (intermediate): want child_count=1, got %d", subGot[c1.ID])
	}

	// Also verify Get returns the same child_count (path uses ticketSelectSQL too).
	rootGot, err := store.Get(ctx, root.ID)
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	if rootGot.ChildCount != 2 {
		t.Errorf("Get(root): want child_count=2, got %d", rootGot.ChildCount)
	}
}
