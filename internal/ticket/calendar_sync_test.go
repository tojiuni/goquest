package ticket_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tojiuni/goquest/internal/calendarsync"
	"github.com/tojiuni/goquest/internal/ticket"
)

// syncFakeCalendarClient is concurrency-safe (calls arrive from the
// service's background goroutines) — mirrors mockWebhook's atomic-counter
// approach in service_test.go, but records full call payloads behind a
// mutex since assertions need the actual Event/objectID, not just a count.
type syncFakeCalendarClient struct {
	mu       sync.Mutex
	upserted []calendarsync.Event
	deleted  []string
}

func (f *syncFakeCalendarClient) UpsertEvent(_ context.Context, ev calendarsync.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, ev)
	return nil
}

func (f *syncFakeCalendarClient) DeleteEvent(_ context.Context, _, objectID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, objectID)
	return nil
}

func (f *syncFakeCalendarClient) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.upserted)
}

func (f *syncFakeCalendarClient) deleteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleted)
}

// waitFor polls until cond() is true or 1s elapses — background goroutines
// need a moment; service_test.go's TestService_Update_CompletedFiresWebhook
// uses a flat time.Sleep(100ms) for the same reason, but polling avoids an
// arbitrarily-sized flat sleep for the two calls this test makes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 1s")
}

func TestService_Create_WithDueAt_UpsertsCalendarEvent(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	tk, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "with due date",
		DueAt: &due,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
	if cal.upserted[0].ObjectID != "goquest-"+tk.ID.String()+".ics" {
		t.Errorf("unexpected object id: %q", cal.upserted[0].ObjectID)
	}
}

func TestService_Create_WithoutDueAt_NoCalendarCall(t *testing.T) {
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{Title: "no due date"}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.upsertCount() != 0 {
		t.Errorf("expected 0 upserts, got %d", cal.upsertCount())
	}
}

func TestService_Update_CompletedTicket_DeletesCalendarEvent(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	base := newTestTicket(ticket.StatusInProgress)
	base.DueAt = &due
	base.RequesterID = "requester@example.com"
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.deleteCount() == 1 })
	if cal.deleted[0] != "goquest-"+base.ID.String()+".ics" {
		t.Errorf("unexpected object id: %q", cal.deleted[0])
	}
}

func TestService_Update_NoDueAt_NotTerminal_NoCalendarCall(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress) // DueAt left nil
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	newTitle := "renamed, unrelated to due date"
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Title: &newTitle}, "actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.upsertCount() != 0 || cal.deleteCount() != 0 {
		t.Errorf("expected no calendar calls, got %d upserts / %d deletes", cal.upsertCount(), cal.deleteCount())
	}
}

// TestService_Create_NonEmailPrincipal_NoCalendarCall covers finding #2:
// when neither AssigneeID nor RequesterID is email-shaped,
// ticketCalendarPrincipal resolves to "" and syncCalendarEvent must no-op
// rather than sending tojihub a call with an empty/non-email principal.
func TestService_Create_NonEmailPrincipal_NoCalendarCall(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title:      "opaque requester id",
		AssigneeID: "bob",
		DueAt:      &due,
	}, "svc-account-42") // requesterID is not email-shaped either
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.upsertCount() != 0 {
		t.Errorf("expected 0 upserts for non-email principal, got %d", cal.upsertCount())
	}
}

// TestService_Update_CompletedTicket_NonEmailPrincipal_NoCalendarCall is the
// delete-path counterpart of TestService_Create_NonEmailPrincipal_NoCalendarCall.
func TestService_Update_CompletedTicket_NonEmailPrincipal_NoCalendarCall(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	base := newTestTicket(ticket.StatusInProgress)
	base.DueAt = &due
	base.AssigneeID = "bob"
	base.RequesterID = "svc-account-42"
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.deleteCount() != 0 {
		t.Errorf("expected 0 deletes for non-email principal, got %d", cal.deleteCount())
	}
}

func TestService_Reopen_WithDueAt_UpsertsCalendarEvent(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	base := newTestTicket(ticket.StatusCompleted)
	base.DueAt = &due
	base.RequesterID = "requester@example.com"
	// ReopenTerminal's mock (internal/ticket/service_test.go) returns a copy
	// of getTicket with Status forced to StatusInProgress — DueAt carries
	// through unchanged, so no extra mockStore wiring is needed here.
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Reopen(context.Background(), base.ID, "actor", "reopened by mistake")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
}
