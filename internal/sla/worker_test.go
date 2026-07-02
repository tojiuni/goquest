package sla_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/sla"
	"github.com/tojiuni/goquest/internal/ticket"
)

// --- mocks ---

type mockScanner struct {
	overdue []*ticket.Ticket
	err     error
}

func (m *mockScanner) ListOverdue(_ context.Context) ([]*ticket.Ticket, error) {
	return m.overdue, m.err
}

type mockUpdater struct {
	updated  []uuid.UUID
	statuses []ticket.Status
}

func (m *mockUpdater) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq, _ string) (*ticket.Ticket, error) {
	m.updated = append(m.updated, id)
	if req.Status != nil {
		m.statuses = append(m.statuses, *req.Status)
	}
	return &ticket.Ticket{ID: id}, nil
}

func newOverdueTicket() *ticket.Ticket {
	past := time.Now().Add(-time.Hour)
	return &ticket.Ticket{
		ID:     uuid.New(),
		Status: ticket.StatusInProgress,
		DueAt:  &past,
	}
}

// --- tests ---

func TestWorker_ExpiresSingleTicket(t *testing.T) {
	tk := newOverdueTicket()
	scanner := &mockScanner{overdue: []*ticket.Ticket{tk}}
	updater := &mockUpdater{}

	w := sla.New(scanner, updater)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Run the expire cycle directly via exported method.
	w.ExpireOnce(ctx)

	if len(updater.updated) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updater.updated))
	}
	if updater.updated[0] != tk.ID {
		t.Error("wrong ticket updated")
	}
	if updater.statuses[0] != ticket.StatusFailed {
		t.Errorf("expected StatusFailed, got %s", updater.statuses[0])
	}
}

func TestWorker_ExpiresMultipleTickets(t *testing.T) {
	overdue := []*ticket.Ticket{newOverdueTicket(), newOverdueTicket(), newOverdueTicket()}
	scanner := &mockScanner{overdue: overdue}
	updater := &mockUpdater{}

	w := sla.New(scanner, updater)
	w.ExpireOnce(context.Background())

	if len(updater.updated) != 3 {
		t.Errorf("expected 3 updates, got %d", len(updater.updated))
	}
}

func TestWorker_NothingExpiredWhenNoOverdue(t *testing.T) {
	scanner := &mockScanner{overdue: nil}
	updater := &mockUpdater{}

	w := sla.New(scanner, updater)
	w.ExpireOnce(context.Background())

	if len(updater.updated) != 0 {
		t.Errorf("expected no updates, got %d", len(updater.updated))
	}
}

func TestWorker_ScanErrorDoesNotPanic(t *testing.T) {
	scanner := &mockScanner{err: context.DeadlineExceeded}
	updater := &mockUpdater{}

	w := sla.New(scanner, updater)
	// Should not panic even on scan error.
	w.ExpireOnce(context.Background())

	if len(updater.updated) != 0 {
		t.Error("expected no updates on scanner error")
	}
}
