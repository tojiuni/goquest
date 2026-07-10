package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// bulkStore makes memStore.Update functional (the base stub returns nil,nil):
// it applies the patch fields the bulk endpoint exercises and returns the
// mutated ticket, so the success path is covered end to end.
type bulkStore struct{ *memStore }

func (s *bulkStore) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	t, ok := s.byID[id]
	if !ok {
		return nil, ticket.ErrNotFound
	}
	if req.Status != nil {
		t.Status = *req.Status
	}
	if req.AssigneeID != nil {
		t.AssigneeID = *req.AssigneeID
	}
	if req.Domain != nil {
		t.Domain = *req.Domain
	}
	return t, nil
}

// noopPublisher satisfies ticket.Publisher; the service calls PublishUpdated on
// every Update, so a non-nil publisher is required even for read-shaped tests.
type noopPublisher struct{}

func (noopPublisher) PublishCreated(context.Context, *ticket.Ticket) error   { return nil }
func (noopPublisher) PublishAssigned(context.Context, *ticket.Ticket) error  { return nil }
func (noopPublisher) PublishUpdated(context.Context, *ticket.Ticket) error   { return nil }
func (noopPublisher) PublishCompleted(context.Context, *ticket.Ticket) error { return nil }

// manageChecker grants manage only for the ids in allow; every other method is
// inherited from worldChecker (unused — the bulk route mounts no view gate).
type manageChecker struct {
	*worldChecker
	allow map[uuid.UUID]bool
}

func (c *manageChecker) CanManageTicket(_ context.Context, id uuid.UUID, _ string) (bool, error) {
	return c.allow[id], nil
}

// TestBulkUpdate_PerIDAuthzAndPartialFailure drives POST /tickets/bulk with a
// mix of allowed, manage-denied, missing, and malformed ids and asserts each is
// reported independently: one row's failure never aborts the others, and only
// authorized rows are mutated.
func TestBulkUpdate_PerIDAuthzAndPartialFailure(t *testing.T) {
	pid := uuid.New()
	mk := func() *ticket.Ticket {
		return &ticket.Ticket{
			ID: uuid.New(), ProjectID: &pid,
			Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
			Visibility: ticket.VisibilityProject,
		}
	}
	t1, t2, t3 := mk(), mk(), mk() // t3 will be manage-denied
	store := &bulkStore{newMemStore(t1, t2, t3)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{t1.ID: true, t2.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil)

	r := chi.NewRouter()
	r.Post("/tickets/bulk", h.BulkUpdate)

	bogus := uuid.New().String()
	body, _ := json.Marshal(map[string]any{
		"ids":   []string{t1.ID.String(), t2.ID.String(), t3.ID.String(), bogus, "not-a-uuid"},
		"patch": map[string]any{"assignee_id": "bob"},
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/bulk", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Updated int `json:"updated"`
		Results []struct {
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Updated != 2 {
		t.Errorf("updated = %d, want 2", resp.Updated)
	}
	got := map[string]struct {
		ok  bool
		err string
	}{}
	for _, row := range resp.Results {
		got[row.ID] = struct {
			ok  bool
			err string
		}{row.OK, row.Error}
	}
	if !got[t1.ID.String()].ok || !got[t2.ID.String()].ok {
		t.Errorf("t1/t2 should be ok: %+v", got)
	}
	if got[t3.ID.String()].ok || got[t3.ID.String()].err != "access denied" {
		t.Errorf("t3 should be forbidden, got %+v", got[t3.ID.String()])
	}
	if got[bogus].ok || got[bogus].err != "ticket not found" {
		t.Errorf("bogus id should be not found, got %+v", got[bogus])
	}
	if got["not-a-uuid"].ok || got["not-a-uuid"].err != "invalid ticket id" {
		t.Errorf("malformed id should be invalid, got %+v", got["not-a-uuid"])
	}
	// Mutation applied only to authorized rows.
	if t1.AssigneeID != "bob" {
		t.Errorf("t1 assignee = %q, want bob", t1.AssigneeID)
	}
	if t3.AssigneeID != "" {
		t.Errorf("t3 must be untouched (denied), got assignee %q", t3.AssigneeID)
	}
}

// TestBulkUpdate_RejectsEmptyAndOversizedIDs covers the request-level guards.
func TestBulkUpdate_RejectsEmptyAndOversizedIDs(t *testing.T) {
	svc := ticket.NewService(&bulkStore{newMemStore()}, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, &worldChecker{}, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil)
	r := chi.NewRouter()
	r.Post("/tickets/bulk", h.BulkUpdate)

	post := func(ids []string) int {
		body, _ := json.Marshal(map[string]any{"ids": ids, "patch": map[string]any{"priority": "high"}})
		req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/bulk", bytes.NewReader(body)), "alice")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := post(nil); code != http.StatusBadRequest {
		t.Errorf("empty ids: status = %d, want 400", code)
	}
	oversized := make([]string, 101)
	for i := range oversized {
		oversized[i] = uuid.New().String()
	}
	if code := post(oversized); code != http.StatusBadRequest {
		t.Errorf("oversized ids: status = %d, want 400", code)
	}
}
