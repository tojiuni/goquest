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

// bulkCreateRouter wires POST /tickets/bulk-create over the given handler,
// mirroring bulkRouter/ticketRouter's minimal-router pattern used by the other
// bulk-* handler tests in this package.
func bulkCreateRouter(h *handler.TicketHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/tickets/bulk-create", h.BulkCreate)
	return r
}

// bulkCreateStore embeds memStore but makes Create actually persist the
// ticket into byID (the shared memStore.Create only checks the title_name
// uniqueness constraint and is a no-op otherwise, since no existing test
// needed created tickets to be readable afterward). BulkCreate's tests do
// need that, to assert per-item field inheritance on the persisted ticket.
type bulkCreateStore struct{ *memStore }

func (s *bulkCreateStore) Create(_ context.Context, t *ticket.Ticket) error {
	if t.TitleName != "" && t.ProjectID != nil {
		for _, ex := range s.byID {
			if ex.TitleName == t.TitleName && ex.ProjectID != nil && *ex.ProjectID == *t.ProjectID {
				return ticket.ErrDuplicateTitleName
			}
		}
	}
	s.byID[t.ID] = t
	s.order = append(s.order, t.ID)
	return nil
}

// TestBulkCreate_Success_InheritsParentAndPartialFailure drives the full
// handler->service->store path: two valid items and one with an empty title.
// The valid items must inherit the parent's project/type/priority/domain; the
// invalid one must fail without aborting its siblings (mirrors BulkUpdate's
// partial-failure contract).
func TestBulkCreate_Success_InheritsParentAndPartialFailure(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{
		ID: uuid.New(), ProjectID: &pid,
		Type: ticket.TypeBug, Priority: ticket.PriorityUrgent, Domain: ticket.DomainCode,
		Status: ticket.StatusPending, Visibility: ticket.VisibilityProject,
	}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil)

	body, _ := json.Marshal(map[string]any{
		"parent_id": parent.ID.String(),
		"items": []map[string]any{
			{"title": "sub 1"},
			{"title": ""},
			{"title": "sub 2", "description": "desc"},
		},
		"source":     "content-split",
		"source_ref": "doc:1",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/bulk-create", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	bulkCreateRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Created int `json:"created"`
		Results []struct {
			Index int    `json:"index"`
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Created != 2 {
		t.Fatalf("created = %d, want 2", resp.Created)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(resp.Results))
	}
	if !resp.Results[0].OK || resp.Results[0].ID == "" {
		t.Errorf("results[0] = %+v, want ok", resp.Results[0])
	}
	if resp.Results[1].OK || resp.Results[1].Error == "" {
		t.Errorf("results[1] = %+v, want failed", resp.Results[1])
	}
	if !resp.Results[2].OK || resp.Results[2].ID == "" {
		t.Errorf("results[2] = %+v, want ok", resp.Results[2])
	}

	// Inheritance check: both created children exist in the store under the
	// parent's project with the parent's type/priority/domain.
	for _, res := range []struct{ id string }{{resp.Results[0].ID}, {resp.Results[2].ID}} {
		created := store.byID[uuid.MustParse(res.id)]
		if created == nil {
			t.Fatalf("created ticket %s not found in store", res.id)
		}
		if created.ProjectID == nil || *created.ProjectID != pid {
			t.Errorf("child %s project_id = %v, want %s", res.id, created.ProjectID, pid)
		}
		if created.Type != ticket.TypeBug || created.Priority != ticket.PriorityUrgent || created.Domain != ticket.DomainCode {
			t.Errorf("child %s did not inherit parent type/priority/domain: %+v", res.id, created)
		}
		if created.ParentID == nil || *created.ParentID != parent.ID {
			t.Errorf("child %s parent_id = %v, want %s", res.id, created.ParentID, parent.ID)
		}
	}
}

// TestBulkCreate_ExceedsMaxItems_400 asserts the maxBulkCreateItems cap is
// enforced before any store access (mirrors maxBulkUpdateIDs on /tickets/bulk).
func TestBulkCreate_ExceedsMaxItems_400(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil)

	items := make([]map[string]any, 51)
	for i := range items {
		items[i] = map[string]any{"title": "sub"}
	}
	body, _ := json.Marshal(map[string]any{
		"parent_id": parent.ID.String(),
		"items":     items,
		"source":    "content-split",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/bulk-create", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	bulkCreateRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

// TestBulkCreate_ManageDenied_403 asserts the single upfront authz check
// against parent_id gates the whole batch.
func TestBulkCreate_ManageDenied_403(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{}} // deny everything
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil)

	body, _ := json.Marshal(map[string]any{
		"parent_id": parent.ID.String(),
		"items":     []map[string]any{{"title": "sub"}},
		"source":    "manual",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/bulk-create", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	bulkCreateRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rr.Code, rr.Body.String())
	}
}

// TestBulkCreate_ParentNotFound_404 asserts a nonexistent parent_id 404s
// before any authz check or item processing.
func TestBulkCreate_ParentNotFound_404(t *testing.T) {
	store := &bulkCreateStore{newMemStore()}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil)

	body, _ := json.Marshal(map[string]any{
		"parent_id": uuid.New().String(),
		"items":     []map[string]any{{"title": "sub"}},
		"source":    "manual",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/bulk-create", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	bulkCreateRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rr.Code, rr.Body.String())
	}
}
