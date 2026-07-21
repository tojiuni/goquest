package handler_test

// Tests for TicketHandler.Reopen (goq-ticket-reopen-terminal): the audited
// escape hatch that moves a completed/cancelled ticket back to in_progress
// without touching the validateTransition state machine (terminals stay
// closed for the normal PATCH path — see statemachine_quality_test.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// reopenStore makes memStore.ReopenTerminal functional and records the id it
// was called with, so tests can assert the store path was actually invoked.
type reopenStore struct {
	*memStore
	reopenedID uuid.UUID
}

func (s *reopenStore) ReopenTerminal(_ context.Context, id uuid.UUID) (*ticket.Ticket, error) {
	s.reopenedID = id
	t, ok := s.byID[id]
	if !ok {
		return nil, ticket.ErrNotFound
	}
	t.Status = ticket.StatusInProgress
	t.CompletedAt = nil
	return t, nil
}

// reopenRouter mirrors cmd/service/main.go: reopen sits inside the
// requireTicketView-gated /tickets/{id} group, additionally gated by
// requireTicketManage (same as Update/Cancel/Approve).
func reopenRouter(svc *ticket.Service, checker spicedb.PermissionChecker) http.Handler {
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil)
	r := chi.NewRouter()
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(auth.RequireTicketView(svc, checker))
		r.With(auth.RequireTicketManage(svc, checker)).Post("/reopen", h.Reopen)
	})
	return r
}

func reopenReq(id uuid.UUID, reason string) *http.Request {
	body, _ := json.Marshal(map[string]any{"reason": reason})
	req := httptest.NewRequest(http.MethodPost, "/tickets/"+id.String()+"/reopen", bytes.NewReader(body))
	return asUser(req, "staff@toji.homes")
}

func TestReopen_CompletedTicket_Succeeds(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusCompleted, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := &reopenStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	// Legacy ticket (no ProjectID) skips the requireTicketManage SpiceDB gate,
	// isolating this test to the Reopen handler/service logic.
	r := reopenRouter(svc, &worldChecker{})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, reopenReq(tk.ID, "reopened by mistake"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if store.reopenedID != tk.ID {
		t.Errorf("store.ReopenTerminal called with %s, want %s", store.reopenedID, tk.ID)
	}
	if tk.Status != ticket.StatusInProgress {
		t.Errorf("ticket status = %s, want in_progress", tk.Status)
	}
}

func TestReopen_MissingReason_Returns400(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusCompleted, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := &reopenStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := reopenRouter(svc, &worldChecker{})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, reopenReq(tk.ID, ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestReopen_NonTerminalTicket_Returns422(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusInProgress, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := &reopenStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := reopenRouter(svc, &worldChecker{})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, reopenReq(tk.ID, "reason"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestReopen_WithoutManagePermission_Returns403(t *testing.T) {
	pid := uuid.New()
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusCompleted, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
		ProjectID: &pid,
	}
	store := &reopenStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	// worldChecker grants view (proj map below) but CanManageTicket always
	// returns false — exercises the requireTicketManage denial path.
	checker := &worldChecker{proj: map[string]map[uuid.UUID]bool{
		"staff@toji.homes": {tk.ID: true},
	}}
	r := reopenRouter(svc, checker)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, reopenReq(tk.ID, "reason"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if store.reopenedID != uuid.Nil {
		t.Error("store.ReopenTerminal must not be called when manage permission is denied")
	}
}
