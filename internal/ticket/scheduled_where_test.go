package ticket

import (
	"strings"
	"testing"
)

// TestTicketWhereScheduled covers goq-roadmap-backend-support: the roadmap
// viewer's server-side "has a schedule" filter must match tickets with
// either due_at or start_at set, and must be a no-op when not requested.
func TestTicketWhereScheduled(t *testing.T) {
	t.Run("scheduled=true adds the due_at/start_at predicate", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{Scheduled: true})
		if !strings.Contains(where, "t.due_at IS NOT NULL OR t.start_at IS NOT NULL") {
			t.Fatalf("scheduled clause missing: %s", where)
		}
		if len(args) != 0 {
			t.Fatalf("scheduled filter must not add positional args, got %v", args)
		}
	})

	t.Run("scheduled=false (default) adds no predicate", func(t *testing.T) {
		where, _, _ := ticketWhere(ListTicketsFilter{})
		if strings.Contains(where, "start_at") {
			t.Fatalf("unscoped filter must not reference start_at: %s", where)
		}
	})
}
