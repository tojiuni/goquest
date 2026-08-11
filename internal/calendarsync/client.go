// Package calendarsync defines the backend-agnostic Event data type used
// to sync a goquest ticket to an external calendar. It has no knowledge of
// any specific backend (tojihub, or anything else) — see the tojihub
// subpackage for the concrete implementation used today. The
// CalendarClient interface that consumes Event lives in package ticket.
package calendarsync

import (
	"time"

	"github.com/google/uuid"
)

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
	// CalendarWorkspaceName is the workspace (org) name, sent alongside
	// CalendarDisplayName rather than folded into it — CalendarDisplayName
	// stays "{workspace}-{project}" for backward compatibility, and this
	// field lets a client (e.g. unicalendar iOS's org>project tree) get the
	// workspace structurally instead of parsing it back out of the combined
	// display name. Empty when the workspace name couldn't be resolved (see
	// ProjectNameResolver in package ticket) or the ticket has no project.
	CalendarWorkspaceName string
	// CalendarProjectName is the project name alone, without the workspace
	// prefix CalendarDisplayName still carries (goquest-calendar-send-project-name).
	// Sent alongside CalendarDisplayName/CalendarWorkspaceName rather than
	// replacing either yet, so the composing responsibility can move to the
	// backend (tojihub-calendar-display-name-compose) without a coordinated
	// cutover. Empty under the same conditions as CalendarWorkspaceName.
	CalendarProjectName string
	// TicketID is the source ticket's ID (goq-ical-ticket-metadata), sent as
	// X-GOQUEST-TICKET-ID so downstream consumers (e.g. unicalendar's
	// uni-rdb-goquest-ext) can link the calendar event back to its ticket.
	TicketID uuid.UUID
	// Status is the ticket's status (string(ticket.Status) — kept as a plain
	// string here rather than ticket.Status to avoid an import cycle, since
	// package ticket already imports this package), sent as X-GOQUEST-STATUS.
	Status string
	// ProjectID is the ticket's project, sent as X-GOQUEST-PROJECT-ID. Nil
	// when the ticket has no project — distinct from CalendarID above, which
	// is a derived value ("default" for no-project tickets) rather than the
	// raw project ID.
	ProjectID *uuid.UUID
	// AssigneeID is the ticket's assignee identifier, sent as
	// X-GOQUEST-ASSIGNEE-ID. Empty when the ticket is unassigned.
	AssigneeID string
	// AssigneeType is the ticket's assignee type (string(ticket.AssigneeType)
	// — plain string for the same import-cycle reason as Status), sent as
	// X-GOQUEST-ASSIGNEE-TYPE.
	AssigneeType string
	// RequesterID is the ticket's requester identifier, sent as
	// X-GOQUEST-REQUESTER-ID.
	RequesterID string
	// StartAt and DueAt are the ticket's start_at/due_at, sent as
	// X-GOQUEST-START-AT/X-GOQUEST-DUE-AT. Nil when unset on the ticket.
	StartAt *time.Time
	DueAt   *time.Time
}
