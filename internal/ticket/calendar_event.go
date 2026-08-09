package ticket

import (
	"strings"

	"github.com/tojiuni/goquest/internal/calendarsync"
)

// ticketCalendarPrincipal resolves the calendar backend's principal
// (owner identity) for a ticket: the assignee's email if AssigneeID is
// email-shaped, otherwise the requester's email — which this workspace's
// session-account convention already guarantees is email-shaped (see the
// calendar sync design documentation for the fallback rationale).
func ticketCalendarPrincipal(t *Ticket) string {
	if strings.Contains(t.AssigneeID, "@") {
		return t.AssigneeID
	}
	return t.RequesterID
}

// ticketCalendarEvent builds the calendarsync.Event for a ticket. Callers
// must check t.DueAt != nil before calling this — DTStart is meaningless
// otherwise (this function does not guard against a nil DueAt itself,
// since call sites already branch on DueAt presence to decide
// UpsertEvent-vs-DeleteEvent; see Task 3).
func ticketCalendarEvent(t *Ticket) calendarsync.Event {
	return calendarsync.Event{
		Principal: ticketCalendarPrincipal(t),
		ObjectID:  "goquest-" + t.ID.String() + ".ics",
		Summary:   t.Title,
		DTStart:   *t.DueAt,
	}
}

// ExportedTicketCalendarEvent is a test-only shim exposing
// ticketCalendarEvent to the external ticket_test package, matching how
// this package is tested everywhere else in the codebase.
func ExportedTicketCalendarEvent(t *Ticket) calendarsync.Event {
	return ticketCalendarEvent(t)
}
