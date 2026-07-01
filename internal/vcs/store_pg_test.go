package vcs

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tojiuni/goquest/internal/migrate"
	"github.com/tojiuni/goquest/internal/ticket"
)

// testPool connects to the integration database named by GOQUEST_TEST_DATABASE_URL
// (the same env var the other PG integration tests use). It runs migrations so
// the vcs_* and tickets tables exist, then returns a ready pool.
func testPool(t *testing.T) *pgxpool.Pool {
	dsn := os.Getenv("GOQUEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("GOQUEST_TEST_DATABASE_URL not set; skipping PG integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("connect test DB (%s): %v", dsn, err)
	}
	if err := migrate.Run(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// makeFixtureTicket seeds a real tickets row so vcs_links.ticket_id → tickets(id)
// is satisfied, then registers cleanup (CASCADE also removes any linked vcs_links).
func makeFixtureTicket(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "vcs-link-test",
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
	}
	if err := ticket.NewPGStore(pool).Create(context.Background(), tk); err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tickets WHERE id=$1`, tk.ID)
	})
	return tk.ID
}

func TestUpsertLinkIdempotent(t *testing.T) {
	pool := testPool(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	tid := makeFixtureTicket(t, pool)

	l := &VcsLink{TicketID: tid, Provider: "github", RepoFullName: "o/r", Kind: KindPR, ExternalRef: "12", State: StateOpen,
		SourceBranch: "feature/goq-foo", TargetBranch: "main"}
	first, err := s.UpsertLink(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceBranch != "feature/goq-foo" || first.TargetBranch != "main" {
		t.Fatalf("branches not persisted: %+v", first)
	}
	l2 := &VcsLink{TicketID: tid, Provider: "github", RepoFullName: "o/r", Kind: KindPR, ExternalRef: "12", State: StateMerged,
		SourceBranch: "feature/goq-foo", TargetBranch: "develop"}
	second, err := s.UpsertLink(ctx, l2)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("upsert created a new row: %s != %s", first.ID, second.ID)
	}
	if second.State != StateMerged {
		t.Fatalf("state not updated: %s", second.State)
	}
	if second.TargetBranch != "develop" {
		t.Fatalf("target_branch not updated on redelivery: %+v", second)
	}
}
