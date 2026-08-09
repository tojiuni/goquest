package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/tojiuni/goquest/internal/calendarsync"
	"github.com/tojiuni/goquest/internal/ticket"
)

// noopCalendarClient is Task 1's minimal fake — it only needs to satisfy
// ticket.CalendarClient so SetCalendarClient compiles and can be called.
// Task 3 introduces a fuller, concurrency-safe fake (syncFakeCalendarClient)
// once there's real dispatch behavior to assert on.
type noopCalendarClient struct{}

func (noopCalendarClient) UpsertEvent(_ context.Context, _ calendarsync.Event) error { return nil }
func (noopCalendarClient) DeleteEvent(_ context.Context, _, _, _ string) error       { return nil }

func TestService_SetCalendarClient_DefaultsToNil(t *testing.T) {
	// NewService's existing 6-arg call sites must keep compiling and
	// behaving exactly as before — SetCalendarClient is opt-in. Exercise a
	// real code path (Create with DueAt set, so the calendar branch would
	// fire if a client were wired) to confirm calendar sync is a true
	// no-op — not merely a nil-check assumption — when SetCalendarClient is
	// never called.
	due := time.Now().Add(24 * time.Hour).UTC()
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "no calendar client wired",
		DueAt: &due,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestService_SetCalendarClient_Wires(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(noopCalendarClient{}) // must compile and not panic
}
