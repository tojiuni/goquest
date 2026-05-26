package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

type stubGetter struct {
	t   *ticket.Ticket
	err error
}

func (s stubGetter) Get(_ context.Context, _ uuid.UUID) (*ticket.Ticket, error) {
	return s.t, s.err
}

// stubChecker only overrides CanViewTicket; embed *NoopChecker (pointer — its methods
// have pointer receivers) for the rest of the interface. A nil embedded pointer is fine
// since the Noop methods never dereference the receiver.
type stubChecker struct {
	*spicedb.NoopChecker
	allow bool
	err   error
}

func (s stubChecker) CanViewTicket(_ context.Context, _ uuid.UUID, _ string, _ spicedb.VisibilityMode) (bool, error) {
	return s.allow, s.err
}

func serve(t *testing.T, mw func(http.Handler) http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(mw)
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func reqWithUser(id uuid.UUID, userID string) *http.Request {
	r := httptest.NewRequest("GET", "/tickets/"+id.String()+"/", nil)
	return r.WithContext(auth.WithRequesterID(r.Context(), userID))
}

func TestRequireTicketView_OIDC(t *testing.T) {
	id := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityProject}

	mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: true})
	if rec := serve(t, mw, reqWithUser(id, "member")); rec.Code != http.StatusOK {
		t.Errorf("member: want 200, got %d", rec.Code)
	}
	mw = auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: false})
	if rec := serve(t, mw, reqWithUser(id, "stranger")); rec.Code != http.StatusForbidden {
		t.Errorf("non-member: want 403, got %d", rec.Code)
	}
	mw = auth.RequireTicketView(stubGetter{err: ticket.ErrNotFound}, stubChecker{allow: true})
	if rec := serve(t, mw, reqWithUser(id, "member")); rec.Code != http.StatusNotFound {
		t.Errorf("not found: want 404, got %d", rec.Code)
	}
	mw = auth.RequireTicketView(stubGetter{t: tk}, stubChecker{err: context.DeadlineExceeded})
	if rec := serve(t, mw, reqWithUser(id, "member")); rec.Code != http.StatusInternalServerError {
		t.Errorf("checker err: want 500, got %d", rec.Code)
	}
}

func TestRequireTicketView_Shadow(t *testing.T) {
	id := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityRestricted}
	mw := auth.RequireTicketView(stubGetter{t: tk}, &spicedb.NoopChecker{})
	if rec := serve(t, mw, reqWithUser(id, "anyone")); rec.Code != http.StatusOK {
		t.Errorf("shadow: want 200, got %d", rec.Code)
	}
}

func TestRequireTicketView_LegacyNoProject(t *testing.T) {
	id := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: nil, Visibility: ticket.VisibilityProject}
	mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: false})
	if rec := serve(t, mw, reqWithUser(id, "anyone")); rec.Code != http.StatusOK {
		t.Errorf("legacy: want 200, got %d", rec.Code)
	}
}

func TestRequireTicketView_CustomerToken(t *testing.T) {
	id := uuid.New()
	other := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityProject}

	withCust := func(reqID uuid.UUID, bound *uuid.UUID) *http.Request {
		r := httptest.NewRequest("GET", "/tickets/"+reqID.String()+"/", nil)
		return r.WithContext(auth.WithCustomerCtx(r.Context(), &auth.CustomerCtx{Email: "c@x.com", TicketID: bound}))
	}

	mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: false})
	if rec := serve(t, mw, withCust(id, &id)); rec.Code != http.StatusOK {
		t.Errorf("bound customer: want 200, got %d", rec.Code)
	}
	if rec := serve(t, mw, withCust(id, &other)); rec.Code != http.StatusForbidden {
		t.Errorf("mismatched customer: want 403, got %d", rec.Code)
	}
	if rec := serve(t, mw, withCust(id, nil)); rec.Code != http.StatusForbidden {
		t.Errorf("unbound customer: want 403, got %d", rec.Code)
	}
}
