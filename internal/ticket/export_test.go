package ticket

import "github.com/tojiuni/goquest/internal/calendarsync"

// ExportedTicketCalendarEvent is a test-only shim exposing the unexported
// ticketCalendarEvent to the external ticket_test package. It lives in
// export_test.go (rather than a production .go file) so the Go compiler
// only includes it when building tests — it never ships in the real
// binary.
func ExportedTicketCalendarEvent(t *Ticket) calendarsync.Event {
	return ticketCalendarEvent(t, ticketCalendarID(t), "", ticketCalendarColor(t))
}
