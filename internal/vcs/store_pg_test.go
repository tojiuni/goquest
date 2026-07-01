package vcs

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestUpsertLinkIdempotent(t *testing.T) {
	pool := testPool(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	tid := uuid.New()
	// (test assumes a tickets row exists or FK deferred; if FK blocks, insert a fixture ticket first.)
	l := &VcsLink{TicketID: tid, Provider: "github", RepoFullName: "o/r", Kind: KindPR, ExternalRef: "12", State: StateOpen}
	first, err := s.UpsertLink(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	l2 := &VcsLink{TicketID: tid, Provider: "github", RepoFullName: "o/r", Kind: KindPR, ExternalRef: "12", State: StateMerged}
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
}
