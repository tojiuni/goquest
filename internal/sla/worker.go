// Package sla provides a background worker that expires overdue tickets.
package sla

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

const defaultInterval = time.Minute

// OverdueScanner finds tickets past their due_at deadline.
type OverdueScanner interface {
	ListOverdue(ctx context.Context) ([]*ticket.Ticket, error)
}

// TicketUpdater transitions a ticket to a new status.
type TicketUpdater interface {
	Update(ctx context.Context, id uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error)
}

// Worker scans for overdue tickets and marks them as failed.
type Worker struct {
	scanner  OverdueScanner
	updater  TicketUpdater
	interval time.Duration
}

func New(scanner OverdueScanner, updater TicketUpdater) *Worker {
	return &Worker{scanner: scanner, updater: updater, interval: defaultInterval}
}

// Run blocks until ctx is cancelled, ticking every interval.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.ExpireOnce(ctx)
		}
	}
}

// ExpireOnce runs a single expiry sweep — exported for testing.
func (w *Worker) ExpireOnce(ctx context.Context) {
	overdue, err := w.scanner.ListOverdue(ctx)
	if err != nil {
		log.Printf("sla: list overdue: %v", err)
		return
	}

	failed := ticket.StatusFailed
	for _, t := range overdue {
		if _, err := w.updater.Update(ctx, t.ID, ticket.UpdateTicketReq{Status: &failed}); err != nil {
			log.Printf("sla: expire ticket %s: %v", t.ID, err)
		} else {
			log.Printf("sla: expired ticket %s (due_at=%s)", t.ID, t.DueAt)
		}
	}
}
