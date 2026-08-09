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

// ticketCalendarEvent builds the calendarsync.Event for a ticket. Callers
// must check t.DueAt != nil before calling this — DTStart is meaningless
// otherwise (this function does not guard against a nil DueAt itself,
// since call sites already branch on DueAt presence to decide
// UpsertEvent-vs-DeleteEvent; see Task 3). calendarID/calendarDisplayName/
// calendarColor are passed in rather than derived here because
// calendarDisplayName needs an async project-name lookup the caller does on
// its own timeout (see syncCalendarEvent in service.go).
func ticketCalendarEvent(t *Ticket, calendarID, calendarDisplayName, calendarColor string) calendarsync.Event {
	return calendarsync.Event{
		Principal:           ticketCalendarPrincipal(t),
		ObjectID:            calendarObjectID(t),
		Summary:             t.Title,
		DTStart:             *t.DueAt,
		CalendarID:          calendarID,
		CalendarDisplayName: calendarDisplayName,
		CalendarColor:       calendarColor,
	}
}
