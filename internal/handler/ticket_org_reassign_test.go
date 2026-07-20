package handler_test

// Tests for TicketHandler.OrgReassign's access-control gate (goq-org-backend
// Task 5). The handler sits behind auth.RequireTicketView in production, so
// these tests mount the real middleware + real ticket.Service (mirroring the
// authz_e2e_test.go pattern) rather than stubbing auth.TicketFrom directly —
// there is no exported way to stash the ticket in context other than through
// RequireTicketView itself.
//
// All test tickets use ProjectID == nil (legacy) so the OIDC/SpiceDB branch of
// RequireTicketView is a no-op gate, isolating the assertions to the org-rule
// branch (Task 4) and OrgReassign's own narrower gate (Task 5).

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

// reassignStore makes memStore.Update functional (the base stub returns
// nil,nil) and records the exact UpdateTicketReq the service forwarded to the
// store, so tests can assert only AssigneeID was touched.
type reassignStore struct {
	*memStore
	lastReq *ticket.UpdateTicketReq
}

func (s *reassignStore) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	s.lastReq = &req
	t, ok := s.byID[id]
	if !ok {
		return nil, ticket.ErrNotFound
	}
	if req.AssigneeID != nil {
		t.AssigneeID = *req.AssigneeID
	}
	if req.AssigneeType != nil {
		t.AssigneeType = *req.AssigneeType
	}
	if req.AssigneeOrgID != nil {
		t.AssigneeOrgID = req.AssigneeOrgID
	}
	return t, nil
}

// orgReassignRouter mirrors the cmd/service/main.go wiring: RequireTicketView
// gates the whole /tickets/{id} group, and org-reassign is mounted inside it
// (no additional requireTicketManage — OrgReassign enforces its own gate).
func orgReassignRouter(svc *ticket.Service, checker spicedb.PermissionChecker) http.Handler {
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil)
	r := chi.NewRouter()
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(auth.RequireTicketView(svc, checker))
		r.Post("/org-reassign", h.OrgReassign)
	})
	return r
}

func asOrg(req *http.Request, o *auth.OrgCtx) *http.Request {
	return req.WithContext(auth.WithOrgCtx(req.Context(), o))
}

func reassignReq(id uuid.UUID, assigneeID string) *http.Request {
	body, _ := json.Marshal(map[string]any{"assignee_id": assigneeID})
	return httptest.NewRequest(http.MethodPost, "/tickets/"+id.String()+"/org-reassign", bytes.NewReader(body))
}

func TestOrgReassign_AdminOfAssigneeOrg_Succeeds(t *testing.T) {
	assigneeOrg := uuid.New()
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeOrg,
		AssigneeOrgID: &assigneeOrg, AssigneeID: "old@corp.com",
	}
	store := &reassignStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := orgReassignRouter(svc, &worldChecker{})

	req := reassignReq(tk.ID, "new@corp.com")
	req = asUser(req, "boss@corp.com")
	req = asOrg(req, &auth.OrgCtx{ID: assigneeOrg, Role: "admin"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if tk.AssigneeID != "new@corp.com" {
		t.Errorf("AssigneeID = %q, want new@corp.com", tk.AssigneeID)
	}
	// Only assignee_id may travel through this endpoint: assignee_type and
	// assignee_org_id must be absent from the request the service received.
	if store.lastReq == nil {
		t.Fatal("store.Update was never called")
	}
	if store.lastReq.AssigneeType != nil {
		t.Errorf("AssigneeType must not be set on the update req, got %v", *store.lastReq.AssigneeType)
	}
	if store.lastReq.AssigneeOrgID != nil {
		t.Errorf("AssigneeOrgID must not be set on the update req, got %v", *store.lastReq.AssigneeOrgID)
	}
	if store.lastReq.AssigneeID == nil || *store.lastReq.AssigneeID != "new@corp.com" {
		t.Errorf("AssigneeID on update req = %v, want new@corp.com", store.lastReq.AssigneeID)
	}
}

func TestOrgReassign_NonAdminOfAssigneeOrg_Forbidden(t *testing.T) {
	assigneeOrg := uuid.New()
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeOrg,
		AssigneeOrgID: &assigneeOrg, AssigneeID: "member@corp.com",
	}
	store := &reassignStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := orgReassignRouter(svc, &worldChecker{})

	// A member can view their own assigned ticket (Task 4 org rule), but
	// OrgReassign requires admin regardless.
	req := reassignReq(tk.ID, "new@corp.com")
	req = asUser(req, "member@corp.com")
	req = asOrg(req, &auth.OrgCtx{ID: assigneeOrg, Role: "member"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if tk.AssigneeID != "member@corp.com" {
		t.Errorf("AssigneeID must be untouched, got %q", tk.AssigneeID)
	}
}

func TestOrgReassign_AdminOfDifferentOrg_Forbidden(t *testing.T) {
	assigneeOrg := uuid.New()
	callerOrg := uuid.New() // caller's own org — distinct from the ticket's assignee org
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeOrg,
		AssigneeOrgID: &assigneeOrg, AssigneeID: "other@corp.com",
		// Requester side is the caller's own org, so RequireTicketView's
		// broader admin-view rule (sameOrg(RequesterOrgID) || sameOrg(AssigneeOrgID))
		// lets them view the ticket — proving OrgReassign's gate is strictly
		// narrower than the view gate, not merely redundant with it.
		RequesterOrgID: &callerOrg,
	}
	store := &reassignStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := orgReassignRouter(svc, &worldChecker{})

	req := reassignReq(tk.ID, "new@corp.com")
	req = asUser(req, "boss@rival.com")
	req = asOrg(req, &auth.OrgCtx{ID: callerOrg, Role: "admin"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if tk.AssigneeID != "other@corp.com" {
		t.Errorf("AssigneeID must be untouched, got %q", tk.AssigneeID)
	}
}

func TestOrgReassign_AssigneeTypeNotOrg_Forbidden(t *testing.T) {
	assigneeOrg := uuid.New()
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject,
		// Data-inconsistency case: AssigneeOrgID is set but AssigneeType never
		// transitioned to "org" (e.g. left over from a prior individual
		// assignment). OrgReassign's precondition — this ticket is genuinely
		// org-assigned — must be enforced explicitly, not assumed from
		// AssigneeOrgID alone.
		AssigneeType:  ticket.AssigneeHuman,
		AssigneeOrgID: &assigneeOrg, AssigneeID: "old@corp.com",
	}
	store := &reassignStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := orgReassignRouter(svc, &worldChecker{})

	req := reassignReq(tk.ID, "new@corp.com")
	req = asUser(req, "boss@corp.com")
	req = asOrg(req, &auth.OrgCtx{ID: assigneeOrg, Role: "admin"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if tk.AssigneeID != "old@corp.com" {
		t.Errorf("AssigneeID must be untouched, got %q", tk.AssigneeID)
	}
}

func TestOrgReassign_NoOrgClaim_Forbidden(t *testing.T) {
	assigneeOrg := uuid.New()
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeOrg,
		AssigneeOrgID: &assigneeOrg, AssigneeID: "old@corp.com",
		// Legacy (no ProjectID) so the OIDC/staff caller sails through the view
		// gate unconditionally, isolating the assertion to OrgReassign's own
		// o == nil check.
		ProjectID: nil,
	}
	store := &reassignStore{memStore: newMemStore(tk)}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	r := orgReassignRouter(svc, &worldChecker{})

	req := reassignReq(tk.ID, "new@corp.com")
	req = asUser(req, "staff@internal") // no org claim: staff/OIDC caller
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if tk.AssigneeID != "old@corp.com" {
		t.Errorf("AssigneeID must be untouched, got %q", tk.AssigneeID)
	}
}
