package ticket_test

import (
	"context"
	"testing"

	"github.com/tojiuni/goquest/internal/calendarsync"
	"github.com/tojiuni/goquest/internal/ticket"
)

// noopCalendarClient is Task 1's minimal fake — it only needs to satisfy
// ticket.CalendarClient so SetCalendarClient compiles and can be called.
// Task 3 introduces a fuller, concurrency-safe fake (syncFakeCalendarClient)
// once there's real dispatch behavior to assert on.
type noopCalendarClient struct{}

func (noopCalendarClient) UpsertEvent(_ context.Context, _ calendarsync.Event) error { return nil }
func (noopCalendarClient) DeleteEvent(_ context.Context, _, _ string) error          { return nil }

func TestService_SetCalendarClient_DefaultsToNil(t *testing.T) {
	// NewService's existing 6-arg call sites must keep compiling and
	// behaving exactly as before — SetCalendarClient is opt-in.
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestService_SetCalendarClient_Wires(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(noopCalendarClient{}) // must compile and not panic
}
