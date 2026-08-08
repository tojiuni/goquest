package ticket_test

import (
	"context"
	"testing"

	"github.com/tojiuni/goquest/internal/ticket"
)

func TestRelations_AddIdempotentReplay(t *testing.T) {
	_, store := setupTestDB(t)
	a := makeTicketForRefs(t, store)
	b := makeTicketForRefs(t, store)

	r1, created1, err := store.AddRelation(context.Background(), a, b, "blocks", "tester")
	if err != nil {
		t.Fatalf("first AddRelation: %v", err)
	}
	if !created1 {
		t.Fatalf("want created=true on first insert")
	}

	r2, created2, err := store.AddRelation(context.Background(), a, b, "blocks", "tester")
	if err != nil {
		t.Fatalf("replay AddRelation: %v", err)
	}
	if created2 {
		t.Fatalf("want created=false on replay")
	}
	if r1.ID != r2.ID {
		t.Fatalf("replay returned different id: %v vs %v", r1.ID, r2.ID)
	}
}

func TestRelations_AddSelfReference(t *testing.T) {
	_, store := setupTestDB(t)
	a := makeTicketForRefs(t, store)

	_, _, err := store.AddRelation(context.Background(), a, a, "blocks", "tester")
	if err != ticket.ErrRelationSelfReference {
		t.Fatalf("want ErrRelationSelfReference, got %v", err)
	}
}

func TestRelations_ListForTicketBothDirections(t *testing.T) {
	_, store := setupTestDB(t)
	a := makeTicketForRefs(t, store)
	b := makeTicketForRefs(t, store)

	if _, _, err := store.AddRelation(context.Background(), a, b, "blocks", "tester"); err != nil {
		t.Fatalf("AddRelation: %v", err)
	}

	relsA, err := store.ListRelationsForTicket(context.Background(), a)
	if err != nil {
		t.Fatalf("ListRelationsForTicket(a): %v", err)
	}
	if len(relsA) != 1 {
		t.Fatalf("want 1 relation for a, got %d", len(relsA))
	}

	relsB, err := store.ListRelationsForTicket(context.Background(), b)
	if err != nil {
		t.Fatalf("ListRelationsForTicket(b): %v", err)
	}
	if len(relsB) != 1 {
		t.Fatalf("want 1 relation for b, got %d", len(relsB))
	}
}
