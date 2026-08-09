package ticket

import (
	"strings"

	"github.com/tojiuni/goquest/internal/calendarsync"
)

// ticketCalendarPrincipal resolves the calendar backend's principal
// (owner identity) for a ticket: the assignee's email if AssigneeID is
// email-shaped, otherwise the requester's email if RequesterID is
// email-shaped. Some auth paths in this codebase populate identifiers with
// opaque non-email IDs rather than verified emails, so neither field is
// guaranteed to be email-shaped — when neither is, this returns "" and
// callers must not send a calendar call with it (see syncCalendarEvent /
// deleteCalendarEvent in service.go).
func ticketCalendarPrincipal(t *Ticket) string {
	if strings.Contains(t.AssigneeID, "@") {
		return t.AssigneeID
	}
	if strings.Contains(t.RequesterID, "@") {
		return t.RequesterID
	}
	return ""
}

// calendarObjectID is the single source of truth for the tojihub object_id
// derived from a ticket — used by both ticketCalendarEvent (upsert) and
// deleteCalendarEvent (service.go) so the two never drift.
func calendarObjectID(t *Ticket) string {
	return "goquest-" + t.ID.String() + ".ics"
}

// ticketCalendarEvent builds the calendarsync.Event for a ticket. Callers
// must check t.DueAt != nil before calling this — DTStart is meaningless
// otherwise (this function does not guard against a nil DueAt itself,
// since call sites already branch on DueAt presence to decide
// UpsertEvent-vs-DeleteEvent; see Task 3).
func ticketCalendarEvent(t *Ticket) calendarsync.Event {
	return calendarsync.Event{
		Principal: ticketCalendarPrincipal(t),
		ObjectID:  calendarObjectID(t),
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
