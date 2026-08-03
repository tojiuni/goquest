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

// subtasksFromChatRouter wires POST /tickets/{id}/subtasks/from-chat over the
// given handler, mirroring subtasksFromTextRouter's minimal-router pattern.
func subtasksFromChatRouter(h *handler.TicketHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/tickets/{id}/subtasks/from-chat", h.SubtasksFromChat)
	return r
}

// fakeCogitoDecomposer implements handler.CogitoSplitter without dialing a
// real cogito gRPC endpoint. It embeds fakeCogitoSplitter so SplitContent is
// also satisfied (the handler.CogitoSplitter interface now covers both RPCs).
type fakeCogitoDecomposer struct {
	fakeCogitoSplitter
	subQuestions []cogitoclient.SubQuestion
	candidates   []cogitoclient.TaskCandidate
	decomposeErr error
	gotMode      cogitoclient.DecomposeMode
	gotHistory   []cogitoclient.ChatTurn
}

func (f *fakeCogitoDecomposer) DecomposeQuestion(_ context.Context, _ string, mode cogitoclient.DecomposeMode, history []cogitoclient.ChatTurn) ([]cogitoclient.SubQuestion, []cogitoclient.TaskCandidate, error) {
	f.gotMode = mode
	f.gotHistory = history
	if f.decomposeErr != nil {
		return nil, nil, f.decomposeErr
	}
	return f.subQuestions, f.candidates, nil
}

func TestSubtasksFromChat_Success(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{
		ID: uuid.New(), ProjectID: &pid,
		Type: ticket.TypeBug, Priority: ticket.PriorityUrgent, Domain: ticket.DomainCode,
		Status: ticket.StatusPending, Visibility: ticket.VisibilityProject,
	}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoDecomposer{candidates: []cogitoclient.TaskCandidate{
		{Title: "task 1", Description: "desc 1"},
		{Title: "task 2", Description: "desc 2"},
		{Title: "task 3", Description: "desc 3"},
	}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{
		"message": "how do we fix this?",
		"history": []map[string]string{
			{"role": "user", "content": "earlier turn"},
			{"role": "assistant", "content": "earlier reply"},
		},
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-chat", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromChatRouter(h).ServeHTTP(rr, req)

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
	if cogito.gotMode != cogitoclient.DecomposeModeTaskCandidates {
		t.Errorf("mode = %v, want DecomposeModeTaskCandidates", cogito.gotMode)
	}
	if len(cogito.gotHistory) != 2 {
		t.Fatalf("history len = %d, want 2", len(cogito.gotHistory))
	}
	if cogito.gotHistory[0].Role != "user" || cogito.gotHistory[0].Content != "earlier turn" {
		t.Errorf("history[0] = %+v, want role=user content='earlier turn'", cogito.gotHistory[0])
	}
}

func TestSubtasksFromChat_EmptyMessage_400(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoDecomposer{candidates: []cogitoclient.TaskCandidate{{Title: "t", Description: "d"}}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"message": ""})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-chat", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromChatRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromChat_CogitoUnavailable_502(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	// nil CogitoSplitter: the handler must 502 rather than dereference nil.
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil)

	body, _ := json.Marshal(map[string]any{"message": "some question"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-chat", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromChatRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromChat_CogitoRPCError_502(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoDecomposer{decomposeErr: errors.New("boom")}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"message": "some question"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-chat", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromChatRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromChat_ZeroCandidates_400(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{parent.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoDecomposer{candidates: nil}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"message": "some question"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-chat", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromChatRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromChat_ManageDenied_403(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	store := &bulkCreateStore{newMemStore(parent)}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{}} // deny everything
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoDecomposer{candidates: []cogitoclient.TaskCandidate{{Title: "t", Description: "d"}}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"message": "some question"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+parent.ID.String()+"/subtasks/from-chat", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromChatRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rr.Code, rr.Body.String())
	}
}

func TestSubtasksFromChat_ParentNotFound_404(t *testing.T) {
	store := &bulkCreateStore{newMemStore()}
	checker := &manageChecker{worldChecker: &worldChecker{}, allow: map[uuid.UUID]bool{}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	cogito := &fakeCogitoDecomposer{candidates: []cogitoclient.TaskCandidate{{Title: "t", Description: "d"}}}
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

	body, _ := json.Marshal(map[string]any{"message": "some question"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/tickets/"+uuid.New().String()+"/subtasks/from-chat", bytes.NewReader(body)), "alice")
	rr := httptest.NewRecorder()
	subtasksFromChatRouter(h).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rr.Code, rr.Body.String())
	}
}
