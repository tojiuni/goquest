package ticket

import (
	"strings"
	"testing"
	"time"
)

// TestTicketWhereDateRange covers goq-ticket-list-date-range-filter:
// goquest-ui's contribution-graph ("잔디") view filters the ticket list to a
// single day by created_at/completed_at. From is inclusive, To is exclusive,
// each pair is independent, and an unset filter must add no predicate.
func TestTicketWhereDateRange(t *testing.T) {
	day := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	nextDay := day.AddDate(0, 0, 1)

	t.Run("created_from adds an inclusive lower bound", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{CreatedFrom: &day})
		if !strings.Contains(where, "t.created_at>=$1") {
			t.Fatalf("created_from clause missing: %s", where)
		}
		if len(args) != 1 || args[0] != day {
			t.Fatalf("want args=[day], got %v", args)
		}
	})

	t.Run("created_to adds an exclusive upper bound", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{CreatedTo: &nextDay})
		if !strings.Contains(where, "t.created_at<$1") {
			t.Fatalf("created_to clause missing: %s", where)
		}
		if len(args) != 1 || args[0] != nextDay {
			t.Fatalf("want args=[nextDay], got %v", args)
		}
	})

	t.Run("created_from + created_to together map a single day, positional args increment", func(t *testing.T) {
		where, args, next := ticketWhere(ListTicketsFilter{CreatedFrom: &day, CreatedTo: &nextDay})
		if !strings.Contains(where, "t.created_at>=$1") || !strings.Contains(where, "t.created_at<$2") {
			t.Fatalf("expected both bounds with incrementing placeholders: %s", where)
		}
		if len(args) != 2 || args[0] != day || args[1] != nextDay {
			t.Fatalf("want args=[day, nextDay], got %v", args)
		}
		if next != 3 {
			t.Fatalf("want next placeholder index 3, got %d", next)
		}
	})

	t.Run("completed_from/completed_to are independent of created_from/created_to", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{CompletedFrom: &day, CompletedTo: &nextDay})
		if !strings.Contains(where, "t.completed_at>=$1") || !strings.Contains(where, "t.completed_at<$2") {
			t.Fatalf("completed range clause missing: %s", where)
		}
		if strings.Contains(where, "created_at") {
			t.Fatalf("completed-only filter must not reference created_at: %s", where)
		}
		if len(args) != 2 {
			t.Fatalf("want 2 args, got %v", args)
		}
	})

	t.Run("all four bounds together compose in declaration order", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{
			CreatedFrom: &day, CreatedTo: &nextDay,
			CompletedFrom: &day, CompletedTo: &nextDay,
		})
		wantOrder := []string{"t.created_at>=$1", "t.created_at<$2", "t.completed_at>=$3", "t.completed_at<$4"}
		for _, frag := range wantOrder {
			if !strings.Contains(where, frag) {
				t.Fatalf("missing %q in %s", frag, where)
			}
		}
		if len(args) != 4 {
			t.Fatalf("want 4 args, got %v", args)
		}
	})

	t.Run("unset (default) adds no date-range predicate", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{})
		for _, frag := range []string{"created_at", "completed_at"} {
			if strings.Contains(where, frag) {
				t.Fatalf("unscoped filter must not reference %s: %s", frag, where)
			}
		}
		if len(args) != 0 {
			t.Fatalf("want no args, got %v", args)
		}
	})
}
