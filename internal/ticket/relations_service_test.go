package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestSvcRelations_AddInvalidType(t *testing.T) {
	svc, store := newSvcWithStore(t)
	a := seedTicket(t, store)
	b := seedTicket(t, store)

	_, _, err := svc.AddRelation(context.Background(), a, b, "invalid_type", "tester")
	if err != ticket.ErrRelationTypeInvalid {
		t.Fatalf("want ErrRelationTypeInvalid, got %v", err)
	}
}

func TestSvcRelations_AddTargetNotFound(t *testing.T) {
	svc, store := newSvcWithStore(t)
	a := seedTicket(t, store)

	_, _, err := svc.AddRelation(context.Background(), a, uuid.New(), "blocks", "tester")
	if err != ticket.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
