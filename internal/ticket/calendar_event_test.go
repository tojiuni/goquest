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

// TestTicketCalendarEvent_NoDueAt_FallsBackToStartAt covers
// goquest-sync-tickets-without-due-at: a ticket with no due_at but a
// start_at must use start_at for DTSTART, not fail or zero out.
func TestTicketCalendarEvent_NoDueAt_FallsBackToStartAt(t *testing.T) {
	start := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	tk := &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "no due date, has a start date",
		AssigneeID:  "assignee@example.com",
		RequesterID: "requester@example.com",
		StartAt:     &start,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if !ev.DTStart.Equal(start) {
		t.Errorf("expected DTStart to fall back to start_at %v, got %v", start, ev.DTStart)
	}
}

// TestTicketCalendarEvent_PopulatesGoquestMetadataFields asserts the
// TicketID/Status/ProjectID/AssigneeID/AssigneeType/RequesterID/StartAt/
// DueAt fields on the resulting Event mirror the source ticket exactly.
func TestTicketCalendarEvent_PopulatesGoquestMetadataFields(t *testing.T) {
	projectID := uuid.New()
	start := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	due := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	tk := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "with metadata",
		Status:       ticket.StatusInProgress,
		ProjectID:    &projectID,
		AssigneeID:   "assignee@example.com",
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "requester@example.com",
		StartAt:      &start,
		DueAt:        &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.TicketID != tk.ID {
		t.Errorf("expected TicketID %v, got %v", tk.ID, ev.TicketID)
	}
	if ev.Status != string(tk.Status) {
		t.Errorf("expected Status %q, got %q", tk.Status, ev.Status)
	}
	if ev.ProjectID == nil || *ev.ProjectID != *tk.ProjectID {
		t.Errorf("expected ProjectID %v, got %v", tk.ProjectID, ev.ProjectID)
	}
	if ev.AssigneeID != tk.AssigneeID {
		t.Errorf("expected AssigneeID %q, got %q", tk.AssigneeID, ev.AssigneeID)
	}
	if ev.AssigneeType != string(tk.AssigneeType) {
		t.Errorf("expected AssigneeType %q, got %q", tk.AssigneeType, ev.AssigneeType)
	}
	if ev.RequesterID != tk.RequesterID {
		t.Errorf("expected RequesterID %q, got %q", tk.RequesterID, ev.RequesterID)
	}
	if ev.StartAt == nil || !ev.StartAt.Equal(*tk.StartAt) {
		t.Errorf("expected StartAt %v, got %v", tk.StartAt, ev.StartAt)
	}
	if ev.DueAt == nil || !ev.DueAt.Equal(*tk.DueAt) {
		t.Errorf("expected DueAt %v, got %v", tk.DueAt, ev.DueAt)
	}
}

// TestTicketCalendarEvent_NoDueAtNoStartAt_FallsBackToCreatedAt covers the
// last-resort fallback for a ticket with neither due_at nor start_at set —
// reachable for tickets that predate Create's start_at/due_at defaulting,
// or that arrive through a path that bypasses it.
func TestTicketCalendarEvent_NoDueAtNoStartAt_FallsBackToCreatedAt(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tk := &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "neither due nor start date",
		AssigneeID:  "assignee@example.com",
		RequesterID: "requester@example.com",
		CreatedAt:   created,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if !ev.DTStart.Equal(created) {
		t.Errorf("expected DTStart to fall back to created_at %v, got %v", created, ev.DTStart)
	}
}
