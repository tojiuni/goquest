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
// Stub publisher/nuclex/xp/webhook/magic — Service constructor accepts nil for all
// optional dependencies, and the ref methods do not invoke them.
func newSvcWithStore(t *testing.T) (*ticket.Service, ticket.Store) {
	t.Helper()
	_, store := setupTestDB(t)
	svc := ticket.NewService(store, nil, nil, nil, nil, nil)
	return svc, store
}

func seedTicket(t *testing.T, store ticket.Store) uuid.UUID {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
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
