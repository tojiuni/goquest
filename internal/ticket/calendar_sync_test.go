package ticket_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/calendarsync"
	"github.com/tojiuni/goquest/internal/ticket"
)

// syncFakeCalendarClient is concurrency-safe (calls arrive from the
// service's background goroutines) — mirrors mockWebhook's atomic-counter
// approach in service_test.go, but records full call payloads behind a
// mutex since assertions need the actual Event/objectID, not just a count.
type syncFakeCalendarClient struct {
	mu       sync.Mutex
	upserted []calendarsync.Event
	deleted  []string
	err      error
}

func (f *syncFakeCalendarClient) UpsertEvent(_ context.Context, ev calendarsync.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.upserted = append(f.upserted, ev)
	return nil
}

func (f *syncFakeCalendarClient) DeleteEvent(_ context.Context, _, _, objectID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, objectID)
	return nil
}

func (f *syncFakeCalendarClient) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.upserted)
}

func (f *syncFakeCalendarClient) deleteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleted)
}

// waitFor polls until cond() is true or 1s elapses — background goroutines
// need a moment; service_test.go's TestService_Update_CompletedFiresWebhook
// uses a flat time.Sleep(100ms) for the same reason, but polling avoids an
// arbitrarily-sized flat sleep for the two calls this test makes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 1s")
}

func TestService_Create_WithDueAt_UpsertsCalendarEvent(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	tk, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "with due date",
		DueAt: &due,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
	if cal.upserted[0].ObjectID != "goquest-"+tk.ID.String()+".ics" {
		t.Errorf("unexpected object id: %q", cal.upserted[0].ObjectID)
	}
}

func TestService_Create_WithoutDueAt_NoCalendarCall(t *testing.T) {
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{Title: "no due date"}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.upsertCount() != 0 {
		t.Errorf("expected 0 upserts, got %d", cal.upsertCount())
	}
}

func TestService_Update_CompletedTicket_DeletesCalendarEvent(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	base := newTestTicket(ticket.StatusInProgress)
	base.DueAt = &due
	base.RequesterID = "requester@example.com"
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.deleteCount() == 1 })
	if cal.deleted[0] != "goquest-"+base.ID.String()+".ics" {
		t.Errorf("unexpected object id: %q", cal.deleted[0])
	}
}

func TestService_Update_NoDueAt_NotTerminal_NoCalendarCall(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress) // DueAt left nil
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	newTitle := "renamed, unrelated to due date"
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Title: &newTitle}, "actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.upsertCount() != 0 || cal.deleteCount() != 0 {
		t.Errorf("expected no calendar calls, got %d upserts / %d deletes", cal.upsertCount(), cal.deleteCount())
	}
}

// TestService_Create_NonEmailPrincipal_NoCalendarCall covers finding #2:
// when neither AssigneeID nor RequesterID is email-shaped,
// ticketCalendarPrincipal resolves to "" and syncCalendarEvent must no-op
// rather than sending tojihub a call with an empty/non-email principal.
func TestService_Create_NonEmailPrincipal_NoCalendarCall(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title:      "opaque requester id",
		AssigneeID: "bob",
		DueAt:      &due,
	}, "svc-account-42") // requesterID is not email-shaped either
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.upsertCount() != 0 {
		t.Errorf("expected 0 upserts for non-email principal, got %d", cal.upsertCount())
	}
}

// TestService_Update_CompletedTicket_NonEmailPrincipal_NoCalendarCall is the
// delete-path counterpart of TestService_Create_NonEmailPrincipal_NoCalendarCall.
func TestService_Update_CompletedTicket_NonEmailPrincipal_NoCalendarCall(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	base := newTestTicket(ticket.StatusInProgress)
	base.DueAt = &due
	base.AssigneeID = "bob"
	base.RequesterID = "svc-account-42"
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // give a wrongly-firing goroutine a chance to show up
	if cal.deleteCount() != 0 {
		t.Errorf("expected 0 deletes for non-email principal, got %d", cal.deleteCount())
	}
}

// failingCalendarClient always errors — used to pin the single most
// important invariant of this feature: a calendar backend outage must
// never fail the ticket mutation that triggered it. Every other fake
// CalendarClient in this suite returns nil, so nothing else exercises this
// path.
type failingCalendarClient struct{}

func (failingCalendarClient) UpsertEvent(_ context.Context, _ calendarsync.Event) error {
	return errors.New("boom")
}

func (failingCalendarClient) DeleteEvent(_ context.Context, _, _, _ string) error {
	return errors.New("boom")
}

func TestService_Create_CalendarClientFails_TicketCreationStillSucceeds(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(failingCalendarClient{})

	tk, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "calendar backend is down",
		DueAt: &due,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("expected calendar failure not to fail ticket creation, got error: %v", err)
	}
	if tk == nil {
		t.Fatal("expected non-nil ticket")
	}
	if tk.Title != "calendar backend is down" {
		t.Errorf("unexpected title: %q", tk.Title)
	}
	if tk.ID.String() == "" {
		t.Error("expected a valid ticket ID")
	}
}

// TestService_Create_NoDueAt_StillSyncs pins goquest-sync-tickets-without-due-at:
// a ticket with no due_at (but a resolvable principal) must still be
// upserted to the calendar — the old due_at requirement is gone.
func TestService_Create_NoDueAt_StillSyncs(t *testing.T) {
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "no due date at all",
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
}

// TestService_Update_TerminalNoDueAt_StillAttemptsDelete pins the delete-branch
// half of the fix: a terminal ticket that never had a due_at must still get
// a delete attempt (defense against a partial fix that only updates the
// upsert branch and leaves due_at-less tickets' events undeleted forever).
func TestService_Update_TerminalNoDueAt_StillAttemptsDelete(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress)
	base.RequesterID = "requester@example.com"
	// DueAt intentionally left nil.
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.deleteCount() == 1 })
}

// TestService_Reopen_NoDueAt_StillSyncs is the Reopen-side equivalent.
func TestService_Reopen_NoDueAt_StillSyncs(t *testing.T) {
	base := newTestTicket(ticket.StatusCompleted)
	base.RequesterID = "requester@example.com"
	// DueAt intentionally left nil.
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Reopen(context.Background(), base.ID, "actor", "reopened by mistake")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
}

func TestService_Reopen_WithDueAt_UpsertsCalendarEvent(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	base := newTestTicket(ticket.StatusCompleted)
	base.DueAt = &due
	base.RequesterID = "requester@example.com"
	// ReopenTerminal's mock (internal/ticket/service_test.go) returns a copy
	// of getTicket with Status forced to StatusInProgress — DueAt carries
	// through unchanged, so no extra mockStore wiring is needed here.
	store := &mockStore{getTicket: base}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Reopen(context.Background(), base.ID, "actor", "reopened by mistake")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
}

// fakeProjectNameResolver is a simple in-memory ProjectNameResolver fake —
// no concurrency concerns beyond syncFakeCalendarClient's (reads happen from
// the same background goroutine that eventually calls UpsertEvent, never
// concurrently with the map being built in the test itself).
type fakeProjectNameResolverEntry struct {
	workspaceName string
	projectName   string
}
type fakeProjectNameResolver map[uuid.UUID]fakeProjectNameResolverEntry

func (f fakeProjectNameResolver) ProjectName(_ context.Context, id uuid.UUID) (string, string, error) {
	e, ok := f[id]
	if !ok {
		return "", "", errors.New("project not found")
	}
	return e.workspaceName, e.projectName, nil
}

func TestService_Create_WithProject_UsesProjectCalendarIDAndColor(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	projectID := uuid.New()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)
	svc.SetProjectNameResolver(fakeProjectNameResolver{
		projectID: {workspaceName: "toji", projectName: "Project Foo"},
	})

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title:     "project-scoped ticket",
		DueAt:     &due,
		ProjectID: &projectID,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
	ev := cal.upserted[0]
	if ev.CalendarID != projectID.String() {
		t.Errorf("expected calendar_id %q, got %q", projectID.String(), ev.CalendarID)
	}
	if ev.CalendarDisplayName != "toji-Project Foo" {
		t.Errorf("expected calendar display name %q, got %q", "toji-Project Foo", ev.CalendarDisplayName)
	}
	if ev.CalendarWorkspaceName != "toji" {
		t.Errorf("expected calendar workspace name %q, got %q", "toji", ev.CalendarWorkspaceName)
	}
	if ev.CalendarProjectName != "Project Foo" {
		t.Errorf("expected calendar project name %q, got %q", "Project Foo", ev.CalendarProjectName)
	}
	if ev.CalendarColor == "" {
		t.Error("expected a non-empty deterministic calendar color for a project ticket")
	}
}

// TestService_Create_WithProject_WorkspaceNameEmpty_DisplayNameDegradesToProjectOnly
// covers the resolver's own degrade path (e.g. main.go's real implementation
// falls back to an empty workspace name when the workspace lookup fails,
// while still returning the project name) — CalendarDisplayName must fall
// back to the bare project name (matching this repo's pre-existing behavior
// before CalendarWorkspaceName existed), not "-Project Foo".
func TestService_Create_WithProject_WorkspaceNameEmpty_DisplayNameDegradesToProjectOnly(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	projectID := uuid.New()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)
	svc.SetProjectNameResolver(fakeProjectNameResolver{
		projectID: {workspaceName: "", projectName: "Project Foo"},
	})

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title:     "project-scoped ticket, no workspace name",
		DueAt:     &due,
		ProjectID: &projectID,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
	ev := cal.upserted[0]
	if ev.CalendarDisplayName != "Project Foo" {
		t.Errorf("expected calendar display name to degrade to bare project name %q, got %q", "Project Foo", ev.CalendarDisplayName)
	}
	if ev.CalendarWorkspaceName != "" {
		t.Errorf("expected empty calendar workspace name, got %q", ev.CalendarWorkspaceName)
	}
	if ev.CalendarProjectName != "Project Foo" {
		t.Errorf("expected calendar project name %q, got %q", "Project Foo", ev.CalendarProjectName)
	}
}

func TestService_Create_WithoutProject_UsesDefaultCalendar(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "no project",
		DueAt: &due,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
	ev := cal.upserted[0]
	if ev.CalendarID != "default" {
		t.Errorf("expected calendar_id %q, got %q", "default", ev.CalendarID)
	}
	if ev.CalendarColor != "" {
		t.Errorf("expected no color for the shared default calendar, got %q", ev.CalendarColor)
	}
}

func TestService_Create_ProjectNameResolverNil_StillUpsertsWithEmptyDisplayName(t *testing.T) {
	// SetProjectNameResolver is never called here — must not panic or block,
	// same nil-checked-dependency contract as SetCalendarClient.
	due := time.Now().Add(24 * time.Hour).UTC()
	projectID := uuid.New()
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title:     "project ticket, no resolver wired",
		DueAt:     &due,
		ProjectID: &projectID,
	}, "user1@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	waitFor(t, func() bool { return cal.upsertCount() == 1 })
	if cal.upserted[0].CalendarDisplayName != "" {
		t.Errorf("expected empty display name with no resolver wired, got %q", cal.upserted[0].CalendarDisplayName)
	}
	if cal.upserted[0].CalendarWorkspaceName != "" {
		t.Errorf("expected empty workspace name with no resolver wired, got %q", cal.upserted[0].CalendarWorkspaceName)
	}
	if cal.upserted[0].CalendarProjectName != "" {
		t.Errorf("expected empty project name with no resolver wired, got %q", cal.upserted[0].CalendarProjectName)
	}
}

func TestService_SyncCalendarEventNow_NoCalendarClient_NoopNilError(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	// SetCalendarClient never called.

	due := time.Now().UTC()
	tk := &ticket.Ticket{ID: uuid.New(), Title: "x", RequesterID: "user@example.com", DueAt: &due}

	if err := svc.SyncCalendarEventNow(context.Background(), tk); err != nil {
		t.Errorf("expected nil error with no CalendarClient wired, got %v", err)
	}
}

func TestService_SyncCalendarEventNow_EmptyPrincipal_NoopNilError(t *testing.T) {
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	due := time.Now().UTC()
	tk := &ticket.Ticket{ID: uuid.New(), Title: "x", AssigneeID: "bob", RequesterID: "svc-account", DueAt: &due}

	if err := svc.SyncCalendarEventNow(context.Background(), tk); err != nil {
		t.Errorf("expected nil error with an unresolvable principal, got %v", err)
	}
	if cal.upsertCount() != 0 {
		t.Errorf("expected no upsert call with an unresolvable principal, got %d", cal.upsertCount())
	}
}

func TestService_SyncCalendarEventNow_Success_CallsUpsertSynchronously(t *testing.T) {
	store := &mockStore{}
	cal := &syncFakeCalendarClient{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)
	projectID := uuid.New()
	svc.SetProjectNameResolver(fakeProjectNameResolver{
		projectID: {workspaceName: "toji", projectName: "Project Foo"},
	})

	due := time.Now().UTC()
	tk := &ticket.Ticket{
		ID: uuid.New(), Title: "sync me now", RequesterID: "user@example.com",
		DueAt: &due, ProjectID: &projectID,
	}

	err := svc.SyncCalendarEventNow(context.Background(), tk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No waitFor: SyncCalendarEventNow is synchronous, the call must have
	// already happened by the time it returns.
	if cal.upsertCount() != 1 {
		t.Fatalf("expected 1 synchronous upsert call, got %d", cal.upsertCount())
	}
	ev := cal.upserted[0]
	if ev.CalendarDisplayName != "toji-Project Foo" {
		t.Errorf("expected calendar display name %q, got %q", "toji-Project Foo", ev.CalendarDisplayName)
	}
	if ev.CalendarWorkspaceName != "toji" {
		t.Errorf("expected calendar workspace name %q, got %q", "toji", ev.CalendarWorkspaceName)
	}
	if ev.CalendarProjectName != "Project Foo" {
		t.Errorf("expected calendar project name %q, got %q", "Project Foo", ev.CalendarProjectName)
	}
}

func TestService_SyncCalendarEventNow_PropagatesCalendarClientError(t *testing.T) {
	store := &mockStore{}
	cal := &syncFakeCalendarClient{err: errors.New("tojihub unavailable")}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	svc.SetCalendarClient(cal)

	due := time.Now().UTC()
	tk := &ticket.Ticket{ID: uuid.New(), Title: "x", RequesterID: "user@example.com", DueAt: &due}

	err := svc.SyncCalendarEventNow(context.Background(), tk)
	if err == nil {
		t.Fatal("expected the CalendarClient's error to propagate, got nil")
	}
}
