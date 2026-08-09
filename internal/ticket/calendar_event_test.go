package ticket_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestTicketCalendarEvent_AssigneeEmailWins(t *testing.T) {
	due := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	id := uuid.New()
	tk := &ticket.Ticket{
		ID:          id,
		Title:       "ship the thing",
		AssigneeID:  "assignee@example.com",
		RequesterID: "requester@example.com",
		DueAt:       &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.Principal != "assignee@example.com" {
		t.Errorf("expected assignee email as principal, got %q", ev.Principal)
	}
	if ev.ObjectID != "goquest-"+id.String()+".ics" {
		t.Errorf("unexpected object id: %q", ev.ObjectID)
	}
	if ev.Summary != "ship the thing" {
		t.Errorf("unexpected summary: %q", ev.Summary)
	}
	if !ev.DTStart.Equal(due) {
		t.Errorf("expected DTStart %v, got %v", due, ev.DTStart)
	}
}

func TestTicketCalendarEvent_FallsBackToRequester(t *testing.T) {
	due := time.Now().UTC()
	tk := &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "no assignee yet",
		AssigneeID:  "bob", // not email-shaped
		RequesterID: "requester@example.com",
		DueAt:       &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.Principal != "requester@example.com" {
		t.Errorf("expected requester fallback, got %q", ev.Principal)
	}
}

func TestTicketCalendarEvent_EmptyAssigneeFallsBackToRequester(t *testing.T) {
	due := time.Now().UTC()
	tk := &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "unassigned",
		AssigneeID:  "",
		RequesterID: "requester@example.com",
		DueAt:       &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.Principal != "requester@example.com" {
		t.Errorf("expected requester fallback, got %q", ev.Principal)
	}
}

// TestTicketCalendarEvent_NeitherIDEmailShaped_EmptyPrincipal covers the
// case where neither AssigneeID nor RequesterID is email-shaped (some auth
// paths populate opaque non-email IDs) — the principal must resolve to ""
// rather than falling back to a non-email RequesterID, so callers can
// detect "no usable principal" and skip the calendar call entirely.
func TestTicketCalendarEvent_NeitherIDEmailShaped_EmptyPrincipal(t *testing.T) {
	due := time.Now().UTC()
	tk := &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "opaque ids only",
		AssigneeID:  "bob",
		RequesterID: "svc-account-42",
		DueAt:       &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.Principal != "" {
		t.Errorf("expected empty principal when neither id is email-shaped, got %q", ev.Principal)
	}
}
