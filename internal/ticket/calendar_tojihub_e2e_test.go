package ticket_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tojiuni/goquest/internal/calendarsync/tojihub"
	"github.com/tojiuni/goquest/internal/ticket"
)

// TestService_Create_WithRealTojihubClient_SendsUpsertEvent is the one
// end-to-end seam test covering svc.Create -> real *tojihub.Client -> actual
// HTTP request. Everything else in this package tests against a fake
// CalendarClient, and internal/calendarsync/tojihub tests the HTTP client in
// isolation via httptest — nothing previously exercised the full path, which
// is exactly what would have caught a payload-level signature drift between
// ticket.CalendarClient and *tojihub.Client (finding #2 in the final review
// was invisible to both halves individually).
func TestService_Create_WithRealTojihubClient_SendsUpsertEvent(t *testing.T) {
	var (
		mu       sync.Mutex
		gotPath  string
		gotBody  map[string]any
		received bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		received = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	realClient := tojihub.New(srv.URL, "test-token")

	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(realClient)

	due := time.Now().Add(24 * time.Hour).UTC()
	tk, err := svc.Create(t.Context(), ticket.CreateTicketReq{
		Title:      "e2e seam test ticket",
		AssigneeID: "assignee@example.com",
		DueAt:      &due,
	}, "requester@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := received
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !received {
		t.Fatal("expected httptest server to receive a request within 1s")
	}
	if gotPath != "/connect/v1/UpsertEvent" {
		t.Errorf("unexpected path: %q", gotPath)
	}
	wantObjectID := "goquest-" + tk.ID.String() + ".ics"
	if gotBody["object_id"] != wantObjectID {
		t.Errorf("unexpected object_id: got %v, want %q", gotBody["object_id"], wantObjectID)
	}
	if gotBody["principal"] != "assignee@example.com" {
		t.Errorf("unexpected principal: %v", gotBody["principal"])
	}
}
