package ticket

import (
	"strings"
	"time"

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

// ticketCalendarID picks which of the principal's calendars a ticket's event
// belongs to: one calendar per project, keyed by the project's ID (stable
// across a project rename, unlike its name), or "default" for tickets with
// no project.
func ticketCalendarID(t *Ticket) string {
	if t.ProjectID == nil {
		return "default"
	}
	return t.ProjectID.String()
}

// ticketCalendarColor derives the owning calendar's color from the ticket's
// project — deterministic (same project always gets the same color, see
// calendarsync.ProjectColor) and empty for tickets with no project (the
// shared "default" calendar has no single project to color it by).
func ticketCalendarColor(t *Ticket) string {
	if t.ProjectID == nil {
		return ""
	}
	return calendarsync.ProjectColor(*t.ProjectID)
}

// ticketCalendarEvent builds the calendarsync.Event for a ticket.
// calendarID/calendarDisplayName/calendarColor are passed in rather than
// derived here because calendarDisplayName needs an async project-name
// lookup the caller does on its own timeout (see syncCalendarEvent in
// service.go).
func ticketCalendarEvent(t *Ticket, calendarID, calendarDisplayName, calendarColor string) calendarsync.Event {
	return calendarsync.Event{
		Principal:           ticketCalendarPrincipal(t),
		ObjectID:            calendarObjectID(t),
		Summary:             t.Title,
		DTStart:             ticketCalendarDate(t),
		CalendarID:          calendarID,
		CalendarDisplayName: calendarDisplayName,
		CalendarColor:       calendarColor,
	}
}

// ticketCalendarDate resolves the date used for a ticket's calendar event
// DTSTART: due_at if set, else start_at, else created_at as a last resort.
// due_at-less tickets are included in calendar sync
// (goquest-sync-tickets-without-due-at) rather than excluded, so this
// always returns a usable date. created_at is only reached for tickets
// that predate Create's start_at/due_at defaulting (see Task 2) or that
// arrive through some other path that bypasses it.
func ticketCalendarDate(t *Ticket) time.Time {
	if t.DueAt != nil {
		return *t.DueAt
	}
	if t.StartAt != nil {
		return *t.StartAt
	}
	return t.CreatedAt
}
