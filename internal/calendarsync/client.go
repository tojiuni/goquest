// Package calendarsync defines the backend-agnostic Event data type used
// to sync a goquest ticket to an external calendar. It has no knowledge of
// any specific backend (tojihub, or anything else) — see the tojihub
// subpackage for the concrete implementation used today. The
// CalendarClient interface that consumes Event lives in package ticket.
package calendarsync

import "time"

// Event is a calendar event derived from a goquest ticket, backend-agnostic.
// Concrete CalendarClient implementations translate this into whatever wire
// format their backend needs (e.g. iCal for a CalDAV-compatible backend).
type Event struct {
	// Principal identifies the calendar owner (e.g. an email address).
	Principal string
	// ObjectID is a stable identifier for this event — repeated calls with
	// the same ObjectID must update the same event, not create a new one.
	ObjectID string
	// Summary is the event title.
	Summary string
	// DTStart is the event's start time.
	DTStart time.Time
	// CalendarID identifies which of the principal's calendars this event
	// belongs to — distinct per project so each project's events land in
	// their own calendar rather than one shared "default" one. "default" for
	// tickets with no project.
	CalendarID string
	// CalendarDisplayName and CalendarColor are the owning calendar's own
	// metadata (project name / a color derived from the project), applied
	// idempotently by the backend on every event sync — not just when the
	// calendar is first created — so a project rename/recolor propagates on
	// the next ticket sync with no separate call needed.
	CalendarDisplayName string
	CalendarColor       string
}
