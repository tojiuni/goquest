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

func TestRequireTicketView_OrgRules(t *testing.T) {
	org, otherOrg := uuid.New(), uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid,
		RequesterID: "m@corp.com", RequesterOrgID: &org}

	cases := []struct {
		name     string
		orgCtx   *auth.OrgCtx
		caller   string
		wantCode int
	}{
		{"org admin views org ticket", &auth.OrgCtx{ID: org, Role: "admin"}, "boss@corp.com", http.StatusOK},
		{"org member views own ticket", &auth.OrgCtx{ID: org, Role: "member"}, "m@corp.com", http.StatusOK},
		{"org member blocked from colleague ticket", &auth.OrgCtx{ID: org, Role: "member"}, "other@corp.com", http.StatusForbidden},
		{"foreign org blocked", &auth.OrgCtx{ID: otherOrg, Role: "admin"}, "x@rival.com", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// An org-axis match must grant without consulting SpiceDB at all
			// (case 1 and 2 below never reach the checker). A non-match must
			// fall through to SpiceDB instead of denying outright, so the
			// checker here denies explicitly rather than erroring — cases 3
			// and 4 rely on this stub actually being consulted.
			mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: false})
			req := reqWithUser(tk.ID, tc.caller)
			req = req.WithContext(auth.WithOrgCtx(req.Context(), tc.orgCtx))
			rec := serve(t, mw, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("%s: code=%d want %d", tc.name, rec.Code, tc.wantCode)
			}
		})
	}
}

// TestRequireTicketView_OrgClaim_FallsThroughToSpiceDB reproduces the
// production bug (goq-org-ticket-view-visibility-bypass): a staff caller who
// also holds an org claim must keep normal SpiceDB visibility on tickets that
// have no org axis at all. Under the old exclusive org branch this 403'd
// without ever consulting SpiceDB; it must now return 200.
func TestRequireTicketView_OrgClaim_FallsThroughToSpiceDB(t *testing.T) {
	pid := uuid.New()
	tk := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Visibility: ticket.VisibilityProject}

	mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: true})
	req := reqWithUser(tk.ID, "staff@corp.com")
	req = req.WithContext(auth.WithOrgCtx(req.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "admin"}))
	if rec := serve(t, mw, req); rec.Code != http.StatusOK {
		t.Errorf("staff+org caller on non-org ticket: want 200, got %d", rec.Code)
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
