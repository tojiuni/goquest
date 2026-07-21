package handler_test

// Regression tests for goq-ticket-update-validation-500: PATCH /tickets/{id}
// must return 400 for client-input validation failures in ticket.Service.Update
// (title, title_name, domain, risk_tier, org assignment, ref ids), not 500.

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

// updateStore makes memStore.Update functional (the base stub returns
// nil,nil) by applying the requested field changes and returning the ticket,
// which service.Update dereferences unconditionally after a successful store
// update (e.g. to check t.Status).
type updateStore struct {
	*memStore
}

func (s *updateStore) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	t, ok := s.byID[id]
	if !ok {
		return nil, ticket.ErrNotFound
	}
	if req.Title != nil {
		t.Title = *req.Title
	}
	if req.TitleName != nil {
		t.TitleName = *req.TitleName
	}
	if req.Domain != nil {
		t.Domain = *req.Domain
	}
	if req.RiskTier != nil {
		t.RiskTier = *req.RiskTier
	}
	if req.AssigneeType != nil {
		t.AssigneeType = *req.AssigneeType
	}
	if req.AssigneeOrgID != nil {
		t.AssigneeOrgID = req.AssigneeOrgID
	}
	return t, nil
}

// updateRouter mirrors the cmd/service/main.go /tickets/{id} PATCH wiring
// closely enough to exercise the real error-mapping path in TicketHandler.Update.
func updateRouter(svc *ticket.Service) http.Handler {
	h := handler.NewTicketHandler(svc, &worldChecker{}, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil)
	r := chi.NewRouter()
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(auth.RequireTicketView(svc, &worldChecker{}))
		r.Patch("/", h.Update)
	})
	return r
}

func patchReq(id uuid.UUID, body map[string]any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/tickets/"+id.String()+"/", bytes.NewReader(b))
	return asUser(req, "staff@toji.homes")
}

func TestUpdate_ValidationErrors_Return400(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty title", map[string]any{"title": ""}},
		{"bad title_name format", map[string]any{"title_name": "BadFormat"}},
		{"invalid domain", map[string]any{"domain": "not-a-domain"}},
		{"invalid risk_tier", map[string]any{"risk_tier": "not-a-tier"}},
		{"org assignment missing org id", map[string]any{"assignee_type": "org"}},
		{"invalid ref id", map[string]any{"ref_ids": []string{""}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := &ticket.Ticket{
				ID: uuid.New(), Title: "orig", Status: ticket.StatusPending,
				Priority: ticket.PriorityNormal, Visibility: ticket.VisibilityProject,
				AssigneeType: ticket.AssigneeHuman,
			}
			store := &updateStore{memStore: newMemStore(tk)}
			svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
			r := updateRouter(svc)

			req := patchReq(tk.ID, tc.body)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUpdate_ValidUpdate_Returns200(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Title: "orig", Status: ticket.StatusPending,
		Priority: ticket.PriorityNormal, Visibility: ticket.VisibilityProject,
		AssigneeType: ticket.AssigneeHuman,
	}
	store := &updateStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := updateRouter(svc)

	req := patchReq(tk.ID, map[string]any{"title": "updated title"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}
