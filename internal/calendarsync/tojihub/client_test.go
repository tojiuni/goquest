package tojihub_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tojiuni/goquest/internal/calendarsync"
	"github.com/tojiuni/goquest/internal/calendarsync/tojihub"
)

func TestClient_UpsertEvent_SendsExpectedRequest(t *testing.T) {
	var (
		gotPath  string
		gotToken string
		gotBody  map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Tojihub-Service-Token")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"etag":"abc"}`))
	}))
	defer srv.Close()

	c := tojihub.New(srv.URL, "test-token")
	err := c.UpsertEvent(context.Background(), calendarsync.Event{
		Principal:             "user@example.com",
		ObjectID:              "goquest-t1.ics",
		Summary:               "do the thing",
		DTStart:               time.Now().UTC(),
		CalendarID:            "project-abc",
		CalendarDisplayName:   "toji-Project Foo",
		CalendarColor:         "#4A90D9FF",
		CalendarWorkspaceName: "toji",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/connect/v1/UpsertEvent" {
		t.Errorf("unexpected path: %q", gotPath)
	}
	if gotToken != "test-token" {
		t.Errorf("unexpected token header: %q", gotToken)
	}
	if gotBody["principal"] != "user@example.com" {
		t.Errorf("unexpected principal: %v", gotBody["principal"])
	}
	if gotBody["calendar_id"] != "project-abc" {
		t.Errorf("unexpected calendar_id: %v", gotBody["calendar_id"])
	}
	if gotBody["object_id"] != "goquest-t1.ics" {
		t.Errorf("unexpected object_id: %v", gotBody["object_id"])
	}
	if gotBody["calendar_display_name"] != "toji-Project Foo" {
		t.Errorf("unexpected calendar_display_name: %v", gotBody["calendar_display_name"])
	}
	if gotBody["calendar_color"] != "#4A90D9FF" {
		t.Errorf("unexpected calendar_color: %v", gotBody["calendar_color"])
	}
	if gotBody["calendar_workspace_name"] != "toji" {
		t.Errorf("unexpected calendar_workspace_name: %v", gotBody["calendar_workspace_name"])
	}
	icalData, _ := gotBody["ical_data"].(string)
	if icalData == "" {
		t.Error("expected non-empty ical_data")
	}
}

func TestClient_UpsertEvent_NonSuccessStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := tojihub.New(srv.URL, "wrong-token")
	err := c.UpsertEvent(context.Background(), calendarsync.Event{ObjectID: "goquest-t1.ics"})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestClient_DeleteEvent_SendsExpectedRequest(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := tojihub.New(srv.URL, "test-token")
	err := c.DeleteEvent(context.Background(), "user@example.com", "project-abc", "goquest-t1.ics")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/connect/v1/DeleteEvent" {
		t.Errorf("unexpected path: %q", gotPath)
	}
	if gotBody["calendar_id"] != "project-abc" {
		t.Errorf("unexpected calendar_id: %v", gotBody["calendar_id"])
	}
	if gotBody["object_id"] != "goquest-t1.ics" {
		t.Errorf("unexpected object_id: %v", gotBody["object_id"])
	}
}

// Compile-time assertion that *Client implements calendarsync.CalendarClient's
// shape (the interface itself lives in package ticket, following the usual
// Go idiom of consumer-defined interfaces — the interface lives next to the
// code that uses it, not the code that implements it — so this checks the
// two methods' signatures directly rather than referencing
// ticket.CalendarClient).
var _ interface {
	UpsertEvent(ctx context.Context, ev calendarsync.Event) error
	DeleteEvent(ctx context.Context, principal, calendarID, objectID string) error
} = (*tojihub.Client)(nil)
