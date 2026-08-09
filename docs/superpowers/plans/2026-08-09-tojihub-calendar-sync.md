# tojihub Calendar Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sync a goquest ticket's `due_at` to a calendar event on tojihub, through a backend-agnostic `CalendarClient` interface that keeps `ticket.Service` free of any tojihub-specific code.

**Architecture:** `ticket.Service` gets an optional `CalendarClient` dependency (same nil-checked-optional pattern already used for `nuclex`/`xp`/`webhook`/`magic`), set via a new `SetCalendarClient` method rather than a `NewService` positional parameter — `NewService` already has ~100 call sites across this repo's tests, and appending a 7th positional argument to all of them is pure churn with no benefit over a setter. The interface (`CalendarClient`) and the plain data type it exchanges (`calendarsync.Event`) are defined so that `internal/calendarsync` never needs to import `internal/ticket`. The concrete tojihub implementation lives in `internal/calendarsync/tojihub`, isolated behind that same interface, so a future non-tojihub backend is a new package, not a change to `ticket.Service`.

**Tech Stack:** Go 1.25, standard `net/http` (no new dependencies), existing `internal/config` env-var pattern, existing test conventions (external `_test` packages, `mockX` fakes, `atomic.Int32` counters for goroutine-driven calls, `time.Sleep` + poll for async assertions — matches `mockWebhook`/`TestService_Update_CompletedFiresWebhook` already in `internal/ticket/service_test.go`).

## Global Constraints

- Nothing tojihub-specific may appear in `internal/calendarsync/client.go` or in `internal/ticket/service.go`'s use of `CalendarClient` — all tojihub-specific logic (iCal building, the HTTP calls, the `X-Tojihub-Service-Token` header) stays inside `internal/calendarsync/tojihub/`.
- A `CalendarClient` failure must never fail a ticket mutation request. Every call site logs with `log.Printf("warn: ...")` and continues, exactly like the existing `s.pub.Publish*` calls.
- The tojihub HTTP call must run in its own goroutine with its own timeout-bound `context.Background()`-derived context — never the inbound request's `ctx`, which is cancelled once the HTTP response is written.
- `object_id` is always `"goquest-" + ticket.ID.String() + ".ics"` — stable across repeated calls.
- `DeleteEvent` is implemented and called now even though tojihub doesn't serve it yet (tracked separately as ticket `infra-tojihub-goquest-delete-event`, `depends_on` non-gating) — until tojihub ships it, calls just error and get logged, same as any other transient failure.
- This plan does not touch `goquest-ui`, the `tojihub` repo, or ticket `infra-goquest-assignee-email`'s scope (assignee email validation) — the principal-mapping fallback below is the only place this plan touches assignee/requester fields, and it's a read-only, best-effort heuristic, not new validation.
- This plan does not touch Vault/VSO/k8s deploy manifests — `TOJIHUB_CONNECT_URL`/`TOJIHUB_SERVICE_TOKEN` are read from env vars; wiring the actual secret value into the deployment is separate infra work.

---

### Task 1: `internal/calendarsync` — `Event` type and `CalendarClient` interface

**Files:**
- Create: `internal/calendarsync/client.go`
- Modify: `internal/ticket/service.go` (add `CalendarClient` interface, `cal` field, `SetCalendarClient` method)
- Test: `internal/ticket/calendar_client_test.go`

**Interfaces:**
- Produces: `calendarsync.Event{Principal, ObjectID, Summary string; DTStart time.Time}` (exported struct, no behavior).
- Produces: `ticket.CalendarClient` interface:
  ```go
  type CalendarClient interface {
      UpsertEvent(ctx context.Context, ev calendarsync.Event) error
      DeleteEvent(ctx context.Context, principal, objectID string) error
  }
  ```
- Produces: `(*Service).SetCalendarClient(c CalendarClient)` — sets `s.cal`. Never called → `s.cal` stays `nil` (zero value), matching the existing nil-checked-optional pattern for `nuclex`/`xp`/`webhook`/`magic`.

- [ ] **Step 1: Write the failing test**

`internal/ticket/calendar_client_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ticket/... -run TestService_SetCalendarClient -v`
Expected: FAIL — `undefined: calendarsync`, `svc.SetCalendarClient undefined`.

- [ ] **Step 3: Create `internal/calendarsync/client.go`**

```go
// Package calendarsync defines a backend-agnostic interface for syncing
// goquest tickets to an external calendar. It has no knowledge of any
// specific backend (tojihub, or anything else) — see the tojihub
// subpackage for the concrete implementation used today.
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
```

- [ ] **Step 4: Add `CalendarClient`, `cal` field, and `SetCalendarClient` to `internal/ticket/service.go`**

Add the import (alongside the existing `"github.com/google/uuid"` import):
```go
	"github.com/tojiuni/goquest/internal/calendarsync"
	"github.com/google/uuid"
```

Add the interface right after the existing `Publisher` interface (after line 24, before `NuclexEmitter`):
```go
// CalendarClient syncs a ticket's due date to an external calendar. Backend
// agnostic — see internal/calendarsync/tojihub for the concrete
// implementation this repo wires up today. A nil CalendarClient (the
// zero value, never set via SetCalendarClient) disables calendar sync
// entirely, same as nuclex/xp/webhook/magic when left unset.
type CalendarClient interface {
	UpsertEvent(ctx context.Context, ev calendarsync.Event) error
	DeleteEvent(ctx context.Context, principal, objectID string) error
}
```

Add the `cal` field to the `Service` struct:
```go
type Service struct {
	store   Store
	pub     Publisher
	nuclex  NuclexEmitter
	xp      XPAwarder
	webhook WebhookSender
	magic   MagicTokener
	cal     CalendarClient
}
```

Add the setter right after `NewService`:
```go
// SetCalendarClient wires an optional CalendarClient into the service. Not
// a NewService parameter deliberately — NewService already has ~100 call
// sites across this repo's tests; a setter lets calendar sync be adopted
// without touching any of them. Leaving it unset (nil) disables calendar
// sync, matching the nuclex/xp/webhook/magic nil-checked pattern.
func (s *Service) SetCalendarClient(c CalendarClient) {
	s.cal = c
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ticket/... -run TestService_SetCalendarClient -v`
Expected: PASS

- [ ] **Step 6: Run the full existing ticket package test suite to confirm nothing broke**

Run: `go test ./internal/ticket/... ./internal/handler/...`
Expected: PASS (all pre-existing tests unaffected — no `NewService` call site required changes)

- [ ] **Step 7: Commit**

```bash
git add internal/calendarsync/client.go internal/ticket/service.go internal/ticket/calendar_client_test.go
git commit -m "feat(ticket): add optional CalendarClient interface to ticket.Service"
```

---

### Task 2: principal-mapping helper — build `calendarsync.Event` from a `*ticket.Ticket`

**Files:**
- Create: `internal/ticket/calendar_event.go`
- Test: `internal/ticket/calendar_event_test.go`

**Interfaces:**
- Consumes: `calendarsync.Event` (Task 1).
- Produces: `ticketCalendarEvent(t *Ticket) calendarsync.Event` (unexported — internal helper used by Task 3's call sites).
- Produces: `ticketCalendarPrincipal(t *Ticket) string` (unexported — the fallback logic in isolation, easiest to unit-test directly; `ticketCalendarEvent` calls it).

Principal rule: if `t.AssigneeID` contains `"@"`, use it. Otherwise use `t.RequesterID` (already guaranteed email-shaped by this workspace's session-account convention — see the approved design spec, `geneso:goquest/spec/2026-08-09-tojihub-calendar-integration-design.md`, "principal 매핑").

- [ ] **Step 1: Write the failing test**

`internal/ticket/calendar_event_test.go`:
```go
package ticket_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestTicketCalendarEvent_AssigneeEmailWins(t *testing.T) {
	due := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	id := uuid.New()
	tk := &ticket.Ticket{
		ID:          id,
		Title:       "ship the thing",
		AssigneeID:  "assignee@example.com",
		RequesterID: "requester@example.com",
		DueAt:       &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.Principal != "assignee@example.com" {
		t.Errorf("expected assignee email as principal, got %q", ev.Principal)
	}
	if ev.ObjectID != "goquest-"+id.String()+".ics" {
		t.Errorf("unexpected object id: %q", ev.ObjectID)
	}
	if ev.Summary != "ship the thing" {
		t.Errorf("unexpected summary: %q", ev.Summary)
	}
	if !ev.DTStart.Equal(due) {
		t.Errorf("expected DTStart %v, got %v", due, ev.DTStart)
	}
}

func TestTicketCalendarEvent_FallsBackToRequester(t *testing.T) {
	due := time.Now().UTC()
	tk := &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "no assignee yet",
		AssigneeID:  "bob", // not email-shaped
		RequesterID: "requester@example.com",
		DueAt:       &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.Principal != "requester@example.com" {
		t.Errorf("expected requester fallback, got %q", ev.Principal)
	}
}

func TestTicketCalendarEvent_EmptyAssigneeFallsBackToRequester(t *testing.T) {
	due := time.Now().UTC()
	tk := &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "unassigned",
		AssigneeID:  "",
		RequesterID: "requester@example.com",
		DueAt:       &due,
	}

	ev := ticket.ExportedTicketCalendarEvent(tk)

	if ev.Principal != "requester@example.com" {
		t.Errorf("expected requester fallback, got %q", ev.Principal)
	}
}
```

`internal/ticket/service.go`'s package is `ticket`, but these tests live in the external `ticket_test` package (matching every other test file in this directory). Since `ticketCalendarEvent` is unexported, add a tiny exported test shim at the bottom of `internal/ticket/calendar_event.go` (Step 3) rather than making the real helper exported — this keeps the production API surface minimal while still letting `ticket_test` exercise it directly, the same trick this package would need for any unexported-but-worth-unit-testing helper.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ticket/... -run TestTicketCalendarEvent -v`
Expected: FAIL — `undefined: ticket.ExportedTicketCalendarEvent`

- [ ] **Step 3: Implement**

`internal/ticket/calendar_event.go`:
```go
package ticket

import "strings"

// ticketCalendarPrincipal resolves the tojihub/CalDAV principal for a
// ticket: the assignee's email if AssigneeID is email-shaped, otherwise the
// requester's email — which this workspace's session-account convention
// already guarantees is email-shaped (see the "principal 매핑" section of
// geneso:goquest/spec/2026-08-09-tojihub-calendar-integration-design.md).
func ticketCalendarPrincipal(t *Ticket) string {
	if strings.Contains(t.AssigneeID, "@") {
		return t.AssigneeID
	}
	return t.RequesterID
}

// ticketCalendarEvent builds the calendarsync.Event for a ticket. Callers
// must check t.DueAt != nil before calling this — DTStart is meaningless
// otherwise (this function does not guard against a nil DueAt itself,
// since call sites already branch on DueAt presence to decide
// UpsertEvent-vs-DeleteEvent; see Task 3).
func ticketCalendarEvent(t *Ticket) calendarsync.Event {
	return calendarsync.Event{
		Principal: ticketCalendarPrincipal(t),
		ObjectID:  "goquest-" + t.ID.String() + ".ics",
		Summary:   t.Title,
		DTStart:   *t.DueAt,
	}
}

// ExportedTicketCalendarEvent is a test-only shim exposing
// ticketCalendarEvent to the external ticket_test package, matching how
// this package is tested everywhere else in the codebase.
func ExportedTicketCalendarEvent(t *Ticket) calendarsync.Event {
	return ticketCalendarEvent(t)
}
```

Add the `"github.com/tojiuni/goquest/internal/calendarsync"` import.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ticket/... -run TestTicketCalendarEvent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ticket/calendar_event.go internal/ticket/calendar_event_test.go
git commit -m "feat(ticket): principal-mapping helper for calendar events"
```

---

### Task 3: wire `CalendarClient` calls into `ticket.Service`'s lifecycle methods

**Files:**
- Modify: `internal/ticket/service.go` (`Create`, `Update`, `Reopen` — see exact insertion points below)
- Test: `internal/ticket/calendar_sync_test.go`

**Interfaces:**
- Consumes: `s.cal CalendarClient` (Task 1), `ticketCalendarEvent`/`ticketCalendarPrincipal` (Task 2).
- Produces: nothing new consumed by later tasks — this is where the design's trigger-point table becomes real code. Task 4 (tojihub client) is consumed here only through the `CalendarClient` interface, already defined.

Read `internal/ticket/service.go` fresh before editing — Task 1/2 already added a few lines above `Create`, so treat the following as *anchors to find*, not exact line numbers.

**3a. `Create`** — anchor: the existing block
```go
	if err := s.pub.PublishCreated(ctx, t); err != nil {
		log.Printf("warn: publish created: %v", err)
	}
```
Add immediately after it (before the `if s.nuclex != nil {` block):
```go
	if s.cal != nil && t.DueAt != nil {
		s.syncCalendarEvent(t)
	}
```

**3b. `Update`** — anchor: the existing block
```go
	if err := s.pub.PublishUpdated(ctx, t); err != nil {
		log.Printf("warn: publish updated: %v", err)
	}
```
Add immediately after it:
```go
	if s.cal != nil {
		switch {
		case t.Status == StatusCompleted || t.Status == StatusFailed || t.Status == StatusCancelled:
			s.deleteCalendarEvent(t)
		case t.DueAt != nil:
			s.syncCalendarEvent(t)
		}
		// t.DueAt == nil and not terminal: nothing to do. Confirmed against
		// store_pg.go's Update (internal/ticket/store_pg.go:273-275) — DueAt
		// is only ever overwritten when req.DueAt is non-nil; there is no
		// "clear the due date" path through UpdateTicketReq today. So a nil
		// t.DueAt here always means this ticket never had one, never that a
		// previously-set one was just cleared — no delete call needed.
	}
```

**3c. `Reopen`** — anchor: the existing block
```go
	if err := s.pub.PublishUpdated(ctx, reopened); err != nil {
		log.Printf("warn: publish updated: %v", err)
	}
```
Add immediately after it:
```go
	if s.cal != nil && reopened.DueAt != nil {
		s.syncCalendarEvent(reopened)
	}
```
(A reopened ticket is never terminal — `StatusInProgress` per `ReopenTerminal` — so only the upsert case applies; no delete branch needed here.)

**3d. Add the two goroutine-dispatch helpers** at the bottom of `internal/ticket/service.go`:
```go
// calendarSyncTimeout bounds each background tojihub call so a slow or
// unreachable backend can't leak goroutines indefinitely.
const calendarSyncTimeout = 10 * time.Second

// syncCalendarEvent upserts t's calendar event in the background. Never
// blocks the caller and never fails the request it was triggered from —
// errors are logged only, matching every other s.pub.Publish* call site in
// this file. Runs on a context.Background()-derived timeout, not the
// caller's ctx, which is cancelled once the HTTP response for the
// triggering request is written — a cancelled ctx would kill the tojihub
// call before it completes.
func (s *Service) syncCalendarEvent(t *Ticket) {
	ev := ticketCalendarEvent(t)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), calendarSyncTimeout)
		defer cancel()
		if err := s.cal.UpsertEvent(ctx, ev); err != nil {
			log.Printf("warn: calendar upsert for ticket %s: %v", t.ID, err)
		}
	}()
}

// deleteCalendarEvent removes t's calendar event in the background. See
// syncCalendarEvent for the goroutine/timeout/error-handling rationale —
// identical here. tojihub does not implement DeleteEvent yet
// (infra-tojihub-goquest-delete-event) — until it does, every call here
// errors and gets logged, same as any other unreachable-backend failure.
func (s *Service) deleteCalendarEvent(t *Ticket) {
	principal := ticketCalendarPrincipal(t)
	objectID := "goquest-" + t.ID.String() + ".ics"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), calendarSyncTimeout)
		defer cancel()
		if err := s.cal.DeleteEvent(ctx, principal, objectID); err != nil {
			log.Printf("warn: calendar delete for ticket %s: %v", t.ID, err)
		}
	}()
}
```

- [ ] **Step 1: Write the failing tests**

`internal/ticket/calendar_sync_test.go`:
```go
package ticket_test

import (
	"context"
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
}

func (f *syncFakeCalendarClient) UpsertEvent(_ context.Context, ev calendarsync.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, ev)
	return nil
}

func (f *syncFakeCalendarClient) DeleteEvent(_ context.Context, _, objectID string) error {
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
	}, "user1")
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

func TestService_Reopen_WithDueAt_UpsertsCalendarEvent(t *testing.T) {
	due := time.Now().Add(24 * time.Hour).UTC()
	base := newTestTicket(ticket.StatusCompleted)
	base.DueAt = &due
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
```

Note: `mockStore.Update` (`internal/ticket/service_test.go:52-69`) only applies `req.Status`/`req.AssigneeID`/`req.AssigneeType` onto its returned copy today — it does not apply `req.Title`, `req.DueAt`, or other fields. `TestService_Update_NoDueAt_NotTerminal_NoCalendarCall` below doesn't need `req.Title` to actually take effect on the returned ticket (it only asserts on calendar-call counts, not on the returned title), so no `mockStore` changes are needed for this task.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ticket/... -run TestService_Create_WithDueAt|TestService_Create_WithoutDueAt|TestService_Update_CompletedTicket_DeletesCalendarEvent|TestService_Update_NoDueAt_NotTerminal|TestService_Reopen_WithDueAt -v`
Expected: FAIL (calendar sync code doesn't exist in `Create`/`Update`/`Reopen` yet).

- [ ] **Step 3: Implement the changes to `internal/ticket/service.go` described above (3a–3d)**

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ticket/... -run TestService_Create_WithDueAt|TestService_Create_WithoutDueAt|TestService_Update_CompletedTicket_DeletesCalendarEvent|TestService_Update_NoDueAt_NotTerminal|TestService_Reopen_WithDueAt -v`
Expected: PASS

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/ticket/service.go internal/ticket/calendar_sync_test.go
git commit -m "feat(ticket): call CalendarClient at due-date/completion lifecycle points"
```

---

### Task 4: `internal/calendarsync/tojihub` — the tojihub `CalendarClient` implementation

**Files:**
- Create: `internal/calendarsync/tojihub/client.go`
- Create: `internal/calendarsync/tojihub/ical.go`
- Test: `internal/calendarsync/tojihub/client_test.go`
- Test: `internal/calendarsync/tojihub/ical_test.go`

**Interfaces:**
- Consumes: `calendarsync.Event` (Task 1).
- Produces: `tojihub.New(baseURL, serviceToken string) *tojihub.Client` satisfying `ticket.CalendarClient` structurally (Go interfaces are structural — `tojihub.Client` does not import `internal/ticket`, it just happens to implement the two methods `ticket.CalendarClient` declares).

**4a. iCal serialization** (`internal/calendarsync/tojihub/ical.go`):

```go
package tojihub

import (
	"fmt"
	"strings"

	"github.com/tojiuni/goquest/internal/calendarsync"
)

// icalTimeFormat is iCal's UTC "floating" basic format: YYYYMMDDTHHMMSSZ.
const icalTimeFormat = "20060102T150405Z"

// toICal renders ev as a minimal VCALENDAR/VEVENT text block. UID matches
// ev.ObjectID without the ".ics" suffix, per the goquest-tojihub contract
// (geneso:neunexus/spec/2026-08-09-goquest-tojihub-integration.md).
func toICal(ev calendarsync.Event) string {
	uid := strings.TrimSuffix(ev.ObjectID, ".ics")
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("BEGIN:VEVENT\r\n")
	fmt.Fprintf(&b, "UID:%s\r\n", uid)
	fmt.Fprintf(&b, "SUMMARY:%s\r\n", escapeICalText(ev.Summary))
	fmt.Fprintf(&b, "DTSTART:%s\r\n", ev.DTStart.UTC().Format(icalTimeFormat))
	b.WriteString("END:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// escapeICalText escapes the RFC 5545 TEXT special characters that can
// appear in a ticket title: backslash, semicolon, comma, and newline.
// Order matters — backslash must be escaped first, or the escapes added
// for the other characters would themselves get re-escaped.
func escapeICalText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
```

- [ ] **Step 1: Write the failing test for iCal serialization**

`internal/calendarsync/tojihub/ical_test.go`:
```go
package tojihub

import (
	"strings"
	"testing"
	"time"

	"github.com/tojiuni/goquest/internal/calendarsync"
)

func TestToICal_BasicFields(t *testing.T) {
	ev := calendarsync.Event{
		Principal: "user@example.com",
		ObjectID:  "goquest-abc123.ics",
		Summary:   "ship the thing",
		DTStart:   time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC),
	}

	out := toICal(ev)

	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"BEGIN:VEVENT",
		"UID:goquest-abc123",
		"SUMMARY:ship the thing",
		"DTSTART:20260820T093000Z",
		"END:VEVENT",
		"END:VCALENDAR",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected iCal output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestToICal_EscapesSpecialCharacters(t *testing.T) {
	ev := calendarsync.Event{
		ObjectID: "goquest-x.ics",
		Summary:  `fix a; b, c\d`,
		DTStart:  time.Now().UTC(),
	}

	out := toICal(ev)

	if !strings.Contains(out, `SUMMARY:fix a\; b\, c\\d`) {
		t.Errorf("expected escaped summary, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/calendarsync/tojihub/... -run TestToICal -v`
Expected: FAIL — `undefined: toICal` (package doesn't exist yet)

- [ ] **Step 3: Implement `internal/calendarsync/tojihub/ical.go`** (content above)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/calendarsync/tojihub/... -run TestToICal -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/calendarsync/tojihub/ical.go internal/calendarsync/tojihub/ical_test.go
git commit -m "feat(calendarsync): tojihub iCal serialization"
```

---

### Task 5: `internal/calendarsync/tojihub` — the HTTP client (`UpsertEvent`/`DeleteEvent`)

**Files:**
- Create: `internal/calendarsync/tojihub/client.go` (Task 4 only created `ical.go` in this package — this task adds `client.go` alongside it)
- Test: `internal/calendarsync/tojihub/client_test.go`

**Interfaces:**
- Consumes: `toICal` (Task 4), `calendarsync.Event` (Task 1).
- Produces: `tojihub.New(baseURL, serviceToken string) *Client`, `(*Client).UpsertEvent`, `(*Client).DeleteEvent` — the two methods that make `*Client` satisfy `ticket.CalendarClient`.

`internal/calendarsync/tojihub/client.go` — modeled directly on `internal/webhook/deliver.go`'s structure (package-level `http.Client` with a timeout, `fmt.Errorf`-wrapped errors, explicit non-2xx check) since that's this repo's existing convention for a small outbound HTTP client, minus the retry loop (tojihub calls are already wrapped in the caller's own goroutine+timeout per Task 3 — no need for a second retry layer here):

```go
// Package tojihub implements calendarsync.CalendarClient against tojihub's
// v1 Connect API (a plain JSON POST today, not real Connect-RPC framing —
// see geneso:neunexus/plan/2026-08-09-tojihub-caldav-v1.md Task 12. When
// tojihub adopts real Connect-RPC (infra-tojihub-connect-real-protocol),
// only this file's HTTP calls change — callers of this package are
// unaffected, since they only see the calendarsync.CalendarClient
// interface.
package tojihub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tojiuni/goquest/internal/calendarsync"
)

const httpTimeout = 10 * time.Second

var httpClient = &http.Client{Timeout: httpTimeout}

// Client implements calendarsync.CalendarClient against a tojihub deployment.
type Client struct {
	baseURL      string
	serviceToken string
}

// New returns a Client. baseURL is tojihub's base address (e.g.
// "http://tojihub.tojihub.svc.cluster.local:8080", no trailing slash).
// serviceToken is the shared secret tojihub's Connect handlers check via
// the X-Tojihub-Service-Token header (Vault
// secret/neunexus/tojihub#connect_service_token).
func New(baseURL, serviceToken string) *Client {
	return &Client{baseURL: baseURL, serviceToken: serviceToken}
}

type upsertEventRequest struct {
	Principal  string `json:"principal"`
	CalendarID string `json:"calendar_id"`
	ObjectID   string `json:"object_id"`
	ICalData   string `json:"ical_data"`
}

// UpsertEvent creates or updates ev's calendar event on tojihub.
// CalendarID is always "default" — tojihub's v1 doesn't support
// per-user multiple calendars yet.
func (c *Client) UpsertEvent(ctx context.Context, ev calendarsync.Event) error {
	body, err := json.Marshal(upsertEventRequest{
		Principal:  ev.Principal,
		CalendarID: "default",
		ObjectID:   ev.ObjectID,
		ICalData:   toICal(ev),
	})
	if err != nil {
		return fmt.Errorf("tojihub: marshal UpsertEvent request: %w", err)
	}
	return c.post(ctx, "/connect/v1/UpsertEvent", body)
}

type deleteEventRequest struct {
	Principal  string `json:"principal"`
	CalendarID string `json:"calendar_id"`
	ObjectID   string `json:"object_id"`
}

// DeleteEvent removes a calendar event on tojihub. tojihub does not
// implement this endpoint yet (infra-tojihub-goquest-delete-event) — until
// it does, this returns a non-2xx error for every call, which callers
// (ticket.Service) already log-and-continue on.
func (c *Client) DeleteEvent(ctx context.Context, principal, objectID string) error {
	body, err := json.Marshal(deleteEventRequest{
		Principal:  principal,
		CalendarID: "default",
		ObjectID:   objectID,
	})
	if err != nil {
		return fmt.Errorf("tojihub: marshal DeleteEvent request: %w", err)
	}
	return c.post(ctx, "/connect/v1/DeleteEvent", body)
}

func (c *Client) post(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("tojihub: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tojihub-Service-Token", c.serviceToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tojihub: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tojihub: %s: non-2xx status %d", path, resp.StatusCode)
	}
	return nil
}
```

- [ ] **Step 1: Write the failing tests**

`internal/calendarsync/tojihub/client_test.go`:
```go
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
		gotPath   string
		gotToken  string
		gotBody   map[string]any
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
		Principal: "user@example.com",
		ObjectID:  "goquest-t1.ics",
		Summary:   "do the thing",
		DTStart:   time.Now().UTC(),
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
	if gotBody["calendar_id"] != "default" {
		t.Errorf("unexpected calendar_id: %v", gotBody["calendar_id"])
	}
	if gotBody["object_id"] != "goquest-t1.ics" {
		t.Errorf("unexpected object_id: %v", gotBody["object_id"])
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
	err := c.DeleteEvent(context.Background(), "user@example.com", "goquest-t1.ics")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/connect/v1/DeleteEvent" {
		t.Errorf("unexpected path: %q", gotPath)
	}
	if gotBody["object_id"] != "goquest-t1.ics" {
		t.Errorf("unexpected object_id: %v", gotBody["object_id"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/calendarsync/tojihub/... -run TestClient -v`
Expected: FAIL — `undefined: tojihub.New` (client.go doesn't exist yet)

- [ ] **Step 3: Implement `internal/calendarsync/tojihub/client.go`** (content above)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/calendarsync/tojihub/... -v`
Expected: PASS (all of Task 4 + Task 5's tests)

- [ ] **Step 5: Verify `*tojihub.Client` satisfies `ticket.CalendarClient`**

This is a compile-time check, not a runtime test — add this to the bottom of `internal/calendarsync/tojihub/client_test.go`:
```go
// Compile-time assertion that *Client implements calendarsync.CalendarClient's
// shape (the interface itself lives in package ticket to avoid an import
// cycle — calendarsync cannot import ticket — so this checks the two
// methods' signatures directly rather than referencing ticket.CalendarClient).
var _ interface {
	UpsertEvent(ctx context.Context, ev calendarsync.Event) error
	DeleteEvent(ctx context.Context, principal, objectID string) error
} = (*tojihub.Client)(nil)
```

Run: `go build ./...`
Expected: succeeds (this line alone would fail to compile if `*Client`'s method signatures ever drift from what `ticket.CalendarClient` expects)

- [ ] **Step 6: Commit**

```bash
git add internal/calendarsync/tojihub/client.go internal/calendarsync/tojihub/client_test.go
git commit -m "feat(calendarsync): tojihub HTTP client for UpsertEvent/DeleteEvent"
```

---

### Task 6: config + wiring — `cmd/service/main.go`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/service/main.go`
- Test: `internal/config/config_test.go` (check whether this file already exists first — if `internal/config` has no test file at all today, skip creating one for a single field addition; if a `TestLoad`-style test already exists, extend it instead of creating a new file)

**Interfaces:**
- Consumes: `tojihub.New` (Task 5), `(*ticket.Service).SetCalendarClient` (Task 1).
- Produces: `cfg.TojihubConnectURL`, `cfg.TojihubServiceToken` (new `Config` fields) — nothing later depends on these; this is the final integration point.

- [ ] **Step 1: Check for an existing config test file**

Run: `ls internal/config/*_test.go 2>/dev/null || echo "none"`

If a test file exists and has a `TestLoad`-shaped test asserting on `Config` fields via env vars, add two assertions for `TOJIHUB_CONNECT_URL`/`TOJIHUB_SERVICE_TOKEN` to it (matching whatever pattern it already uses for `COGITO_GRPC_ADDR` et al.) as this task's Step 1 test-writing work instead of the block below. If no test file exists, config loading isn't covered by tests in this repo today — skip straight to Step 2 (implementation) for this task, since there's no existing convention to extend and manufacturing a new one for a two-field addition isn't proportionate.

- [ ] **Step 2: Add config fields**

In `internal/config/config.go`, add near the existing `CogitoGRPCAddr`/`CogitoBearerToken` fields (after line 81):
```go
	// tojihub Connect API (infra-tojihub-goquest-integration) — syncs ticket
	// due dates to a tojihub calendar via POST /connect/v1/{Upsert,Delete}Event.
	// Empty TojihubConnectURL disables calendar sync entirely (SetCalendarClient
	// is never called). TojihubServiceToken is the shared secret tojihub's
	// Connect handlers check via X-Tojihub-Service-Token (Vault
	// secret/neunexus/tojihub#connect_service_token — VSO wiring into this
	// service's k8s deployment is separate infra work, not part of this repo's
	// code changes).
	TojihubConnectURL   string
	TojihubServiceToken string
```

In the `Load()` function, add near the existing `CogitoGRPCAddr`/`CogitoBearerToken` assignments:
```go
		TojihubConnectURL:   os.Getenv("TOJIHUB_CONNECT_URL"),
		TojihubServiceToken: os.Getenv("TOJIHUB_SERVICE_TOKEN"),
```

- [ ] **Step 3: Wire it up in `cmd/service/main.go`**

Add right after the existing `svc := ticket.NewService(...)` line (main.go:113):
```go
	// tojihub calendar sync (infra-tojihub-goquest-integration) — optional,
	// same non-fatal-on-missing-config pattern as the cogito client below.
	if cfg.TojihubConnectURL != "" {
		svc.SetCalendarClient(tojihub.New(cfg.TojihubConnectURL, cfg.TojihubServiceToken))
	}
```

Add the import: `"github.com/tojiuni/goquest/internal/calendarsync/tojihub"`.

- [ ] **Step 4: Build to confirm it compiles**

Run: `go build ./...`
Expected: succeeds

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Run `go vet`**

Run: `go vet ./...`
Expected: no issues

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go cmd/service/main.go
git commit -m "feat(service): wire tojihub CalendarClient into main.go"
```

---

## Post-plan note (not a task — informational)

`TOJIHUB_CONNECT_URL`/`TOJIHUB_SERVICE_TOKEN` need real values in the k8s
deployment (VSO secret wiring for `secret/neunexus/tojihub#connect_service_token`,
plus the in-cluster tojihub service address) before calendar sync is live in
any environment — that's tracked by the deploy side of
`infra-tojihub-goquest-integration`, not by this plan. Until wired, the
service simply runs with calendar sync disabled (`TojihubConnectURL == ""` →
`SetCalendarClient` never called), which is safe and requires no code change
to enable later.
