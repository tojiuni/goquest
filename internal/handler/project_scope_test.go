package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/apikey"
	"github.com/tojiuni/goquest/internal/template"
	"github.com/tojiuni/goquest/internal/ticket"
)

// Regression: handlers that look up a child row by its own id must also check
// the row belongs to the {pid} the caller was authorized for.

type scopeKeySvc struct {
	keys    []*apikey.APIKey
	deleted uuid.UUID
}

func (s *scopeKeySvc) Create(context.Context, uuid.UUID, apikey.CreateReq, string) (*apikey.CreateResult, error) {
	return nil, nil
}
func (s *scopeKeySvc) List(context.Context, uuid.UUID) ([]*apikey.APIKey, error) { return s.keys, nil }
func (s *scopeKeySvc) Delete(_ context.Context, id uuid.UUID) error {
	s.deleted = id
	return nil
}

type scopeTmplSvc struct {
	t       *template.Template
	deleted uuid.UUID
}

func (s *scopeTmplSvc) Create(context.Context, uuid.UUID, template.CreateReq, string) (*template.Template, error) {
	return nil, nil
}
func (s *scopeTmplSvc) List(context.Context, uuid.UUID) ([]*template.Template, error) { return nil, nil }
func (s *scopeTmplSvc) Get(context.Context, uuid.UUID) (*template.Template, error)    { return s.t, nil }
func (s *scopeTmplSvc) Delete(_ context.Context, id uuid.UUID) error {
	s.deleted = id
	return nil
}
func (s *scopeTmplSvc) ToTicketReq(*template.Template, ticket.CreateTicketReq) ticket.CreateTicketReq {
	return ticket.CreateTicketReq{}
}

type scopeTickets struct{ created bool }

func (s *scopeTickets) Create(context.Context, ticket.CreateTicketReq, string) (*ticket.Ticket, error) {
	s.created = true
	return &ticket.Ticket{}, nil
}

func chiReq(method string, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, "/", nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestAPIKeyDelete_RejectsKeyFromOtherProject(t *testing.T) {
	mine, foreign, pid := uuid.New(), uuid.New(), uuid.New()
	svc := &scopeKeySvc{keys: []*apikey.APIKey{{ID: mine}}}
	h := NewAPIKeyHandler(svc)

	rec := httptest.NewRecorder()
	h.Delete(rec, chiReq(http.MethodDelete, map[string]string{"pid": pid.String(), "kid": foreign.String()}))
	if rec.Code != http.StatusNotFound || svc.deleted != uuid.Nil {
		t.Fatalf("foreign key: status=%d deleted=%v, want 404 and no delete", rec.Code, svc.deleted)
	}

	rec = httptest.NewRecorder()
	h.Delete(rec, chiReq(http.MethodDelete, map[string]string{"pid": pid.String(), "kid": mine.String()}))
	if rec.Code != http.StatusNoContent || svc.deleted != mine {
		t.Fatalf("own key: status=%d deleted=%v, want 204 and delete", rec.Code, svc.deleted)
	}
}

func TestTemplateHandlers_RejectTemplateFromOtherProject(t *testing.T) {
	pid, otherPid, tid := uuid.New(), uuid.New(), uuid.New()
	svc := &scopeTmplSvc{t: &template.Template{ID: tid, ProjectID: otherPid}}
	tickets := &scopeTickets{}
	h := NewTemplateHandler(svc, tickets)
	params := map[string]string{"pid": pid.String(), "tid": tid.String()}

	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"get": h.Get, "delete": h.Delete, "instantiate": h.Instantiate,
	} {
		rec := httptest.NewRecorder()
		fn(rec, chiReq(http.MethodGet, params))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404", name, rec.Code)
		}
	}
	if svc.deleted != uuid.Nil || tickets.created {
		t.Fatalf("foreign template was deleted=%v or instantiated=%v", svc.deleted, tickets.created)
	}

	svc.t.ProjectID = pid
	rec := httptest.NewRecorder()
	h.Get(rec, chiReq(http.MethodGet, params))
	if rec.Code != http.StatusOK {
		t.Fatalf("own template get: status=%d, want 200", rec.Code)
	}
}
