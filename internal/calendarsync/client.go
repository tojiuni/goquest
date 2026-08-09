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
}
