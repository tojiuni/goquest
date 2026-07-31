package mention_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tojiuni/goquest/internal/mention"
	"github.com/tojiuni/goquest/internal/migrate"
	"github.com/tojiuni/goquest/internal/ticket"
)

// setupTestDB mirrors internal/ticket/refs_store_test.go's setupTestDB: a
// real-Postgres integration test, skipped when GOQUEST_TEST_DATABASE_URL is
// unset or the DB is unreachable, so the hermetic unit suite never depends
// on a live database.
func setupTestDB(t *testing.T) (mention.Store, ticket.Store) {
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
	// ticket_mentions is CASCADE'd by tickets; the explicit DELETE is a
	// belt-and-braces guard if migration order shifts.
	if _, err := pool.Exec(ctx, `DELETE FROM ticket_mentions`); err != nil {
		t.Fatalf("wipe ticket_mentions: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tickets`); err != nil {
		t.Fatalf("wipe tickets: %v", err)
	}

	t.Cleanup(func() { pool.Close() })
	return mention.NewPGStore(pool), ticket.NewPGStore(pool)
}

func makeTicketForMentions(t *testing.T, store ticket.Store) uuid.UUID {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "mentions-test",
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

func TestPGStore_CreateAndListByActor(t *testing.T) {
	store, tickets := setupTestDB(t)
	ctx := context.Background()
	tid := makeTicketForMentions(t, tickets)

	m := &mention.Mention{
		ID:               uuid.New(),
		TicketID:         tid,
		MentionedActorID: "bob",
		Source:           mention.SourceComment,
		CreatedAt:        time.Now().UTC(),
		CreatedBy:        "alice",
	}
	if err := store.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	list, err := store.ListByActor(ctx, "bob", 10, 0)
	if err != nil {
		t.Fatalf("ListByActor: %v", err)
	}
	if len(list) != 1 || list[0].ID != m.ID {
		t.Fatalf("want 1 mention with id %s, got %+v", m.ID, list)
	}
	if list[0].SeenAt != nil {
		t.Fatalf("new mention should be unseen, got SeenAt=%v", list[0].SeenAt)
	}
}

func TestPGStore_CountUnseenAndMarkSeen(t *testing.T) {
	store, tickets := setupTestDB(t)
	ctx := context.Background()
	tid := makeTicketForMentions(t, tickets)

	m := &mention.Mention{
		ID: uuid.New(), TicketID: tid, MentionedActorID: "carol",
		Source: mention.SourceDescription, CreatedAt: time.Now().UTC(), CreatedBy: "dave",
	}
	if err := store.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	n, err := store.CountUnseenByActor(ctx, "carol")
	if err != nil {
		t.Fatalf("CountUnseenByActor: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 unseen, got %d", n)
	}

	if err := store.MarkSeen(ctx, m.ID, "carol"); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	n, err = store.CountUnseenByActor(ctx, "carol")
	if err != nil {
		t.Fatalf("CountUnseenByActor after MarkSeen: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 unseen after MarkSeen, got %d", n)
	}

	// Marking again (already seen) or as the wrong actor must not succeed silently.
	if err := store.MarkSeen(ctx, m.ID, "carol"); err != mention.ErrNotFound {
		t.Fatalf("re-MarkSeen: want ErrNotFound, got %v", err)
	}
}

func TestPGStore_MarkSeen_WrongActorIsNotFound(t *testing.T) {
	store, tickets := setupTestDB(t)
	ctx := context.Background()
	tid := makeTicketForMentions(t, tickets)

	m := &mention.Mention{
		ID: uuid.New(), TicketID: tid, MentionedActorID: "erin",
		Source: mention.SourceComment, CreatedAt: time.Now().UTC(), CreatedBy: "frank",
	}
	if err := store.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.MarkSeen(ctx, m.ID, "not-erin"); err != mention.ErrNotFound {
		t.Fatalf("MarkSeen as wrong actor: want ErrNotFound, got %v", err)
	}
}
