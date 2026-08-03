package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/cogitoclient"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// subtasksFromTextRouter wires POST /tickets/{id}/subtasks/from-text over the
// given handler, mirroring bulkCreateRouter's minimal-router pattern used by
// the other bulk-* handler tests in this package.
func subtasksFromTextRouter(h *handler.TicketHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/tickets/{id}/subtasks/from-text", h.SubtasksFromText)
	return r
}

// fakeCogitoSplitter implements handler.CogitoSplitter without dialing a real
// cogito gRPC endpoint.
type fakeCogitoSplitter struct {
	sections []cogitoclient.Section
	err      error
}

func (f *fakeCogitoSplitter) SplitContent(_ context.Context, _, _ string) ([]cogitoclient.Section, []string, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.sections, nil, nil
}

// DecomposeQuestion is a no-op stub so fakeCogitoSplitter satisfies the
// (now two-RPC) handler.CogitoSplitter interface; the from-text tests never
// exercise this path. subtasks_from_chat_test.go's fakeCogitoDecomposer
// embeds fakeCogitoSplitter and overrides this method with real behavior.
func (f *fakeCogitoSplitter) DecomposeQuestion(_ context.Context, _ string, _ cogitoclient.DecomposeMode, _ []cogitoclient.ChatTurn) ([]cogitoclient.SubQuestion, []cogitoclient.TaskCandidate, error) {
	return nil, nil, nil
}

func TestSubtasksFromText_Success(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{
		ID: uuid.New(), ProjectID: &pid,
		Type: ticket.TypeBug, Priority: ticket.PriorityUrgent, Domain: ticket.DomainCode,
		Status: ticket.StatusPending, Visibility: ticket.VisibilityProject,
	}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoSplitter{sections: []cogitoclient.Section{
		{Title: "sec 1", Contents: "body 1"},
		{Title: "sec 2", Contents: "body 2"},
		{Title: "sec 3", Contents: "body 3", SubSections: []cogitoclient.Section{{Title: "nested", Contents: "ignored in v1"}}},
	}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"text": "some long text", "title": "doc title"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-text", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromTextRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Created int `json:"created"`
		Results []struct {
			OK bool `json:"ok"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Created != 3 {
		t.Fatalf("created = %d, want 3", resp.Created)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(resp.Results))
	}
	for i, res := range resp.Results {
		if !res.OK {
			t.Errorf("results[%d].OK = false, want true", i)
		}
	}
}

func TestSubtasksFromText_EmptyText_400(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoSplitter{sections: []cogitoclient.Section{{Title: "sec 1", Contents: "body"}}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"text": ""})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-text", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromTextRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromText_CogitoUnavailable_502(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	// nil CogitoSplitter: the handler must 502 rather than dereference nil.
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil)

	body, _ := json.Marshal(map[string]any{"text": "some text"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-text", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromTextRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromText_CogitoRPCError_502(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoSplitter{err: errors.New("boom")}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"text": "some text"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-text", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromTextRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromText_ZeroSections_400(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoSplitter{sections: nil}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"text": "some text"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-text", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromTextRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromText_ManageDenied_403(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{}} // deny everything
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoSplitter{sections: []cogitoclient.Section{{Title: "sec 1", Contents: "body"}}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"text": "some text"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-text", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromTextRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromText_ParentNotFound_404(t *testing.T) {
	store := &bulkCreateStore{newMemStore()}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoSplitter{sections: []cogitoclient.Section{{Title: "sec 1", Contents: "body"}}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"text": "some text"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+uuid.New().String()+"/subtasks/from-text", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromTextRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rr.Code, rr.Body.String())
	}
}
