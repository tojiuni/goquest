package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/brainclient"
	"github.com/tojiuni/goquest/internal/githubapp"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
	wskg "github.com/tojiuni/goquest/internal/workspace"
)

// fakeStaffChecker stubs auth.StaffChecker. A nil/empty workspaces slice
// means "not staff" (matches most existing tests' external-caller intent);
// tests proving the staff+org-claim fix set workspaces non-empty.
type fakeStaffChecker struct {
	workspaces []*wskg.Workspace
	err        error
}

func (f *fakeStaffChecker) ListByUser(_ context.Context, _ string) ([]*wskg.Workspace, error) {
	return f.workspaces, f.err
}

type fakeBrainLookuper struct {
	docs []brainclient.RelatedDoc
	err  error
}

func (f *fakeBrainLookuper) TicketDocs(_ context.Context, _, _ string) ([]brainclient.RelatedDoc, error) {
	return f.docs, f.err
}

type fakeContentFetcher struct {
	content string
	err     error
	called  bool
}

func (f *fakeContentFetcher) Content(_ context.Context, _, _, _ string) (string, error) {
	f.called = true
	return f.content, f.err
}

// refDocsRouter mounts TicketDocs inside the same RequireTicketView-gated
// /tickets/{id} group as production, and Content as a standalone route
// (mirrors cmd/service/main.go's authenticated group, minus API-key/JWT
// middleware — auth.WithRequesterID + a raw Authorization header stand in
// for it in tests, matching this package's existing test conventions).
func refDocsRouter(rd *handler.RefDocsHandler, svc *ticket.Service, checker spicedb.PermissionChecker) http.Handler {
	r := chi.NewRouter()
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(auth.RequireTicketView(svc, checker))
		r.Get("/docs", rd.TicketDocs)
	})
	r.Get("/docs/content", rd.Content)
	return r
}

func withBearerJWT(req *http.Request, jwt string) *http.Request {
	req.Header.Set("Authorization", "Bearer "+jwt)
	return req
}

func TestRefDocs_TicketDocs_ReturnsResolvedDocs(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := newMemStore(tk)
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	rd := handler.NewRefDocsHandler(
		&fakeBrainLookuper{docs: []brainclient.RelatedDoc{{Title: "Foo", SourcePath: "goquest/spec/foo.md"}}},
		&fakeContentFetcher{}, &fakeStaffChecker{}, "tojiuni", "geneso",
	)
	r := refDocsRouter(rd, svc, &worldChecker{})

	req := httptest.NewRequest("GET", "/tickets/"+tk.ID.String()+"/docs", nil)
	req = asUser(req, "staff@toji.homes")
	req = withBearerJWT(req, "header.payload.sig")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"docs":[{"title":"Foo","source_path":"goquest/spec/foo.md"}]}`, rec.Body.String())
}

func TestRefDocs_TicketDocs_NoBearerToken_Returns401(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := newMemStore(tk)
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{}, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, svc, &worldChecker{})

	req := httptest.NewRequest("GET", "/tickets/"+tk.ID.String()+"/docs", nil)
	req = asUser(req, "staff@toji.homes")
	// No Authorization header set — API-key-authenticated caller, no JWT to forward.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 401, rec.Code)
}

func TestRefDocs_TicketDocs_JWTDecodeError_Returns401(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := newMemStore(tk)
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	rd := handler.NewRefDocsHandler(
		&fakeBrainLookuper{err: fmt.Errorf("brainclient: decode caller JWT: %w: %w", brainclient.ErrJWTDecode, errors.New("malformed JWT"))},
		&fakeContentFetcher{}, &fakeStaffChecker{}, "tojiuni", "geneso",
	)
	r := refDocsRouter(rd, svc, &worldChecker{})

	req := withBearerJWT(asUser(httptest.NewRequest("GET", "/tickets/"+tk.ID.String()+"/docs", nil), "staff@toji.homes"), "not.a.validjwt")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 401, rec.Code)
}

func TestRefDocs_TicketDocs_BrainError_Returns502(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := newMemStore(tk)
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	rd := handler.NewRefDocsHandler(
		&fakeBrainLookuper{err: errors.New("brain unavailable")},
		&fakeContentFetcher{}, &fakeStaffChecker{}, "tojiuni", "geneso",
	)
	r := refDocsRouter(rd, svc, &worldChecker{})

	req := withBearerJWT(asUser(httptest.NewRequest("GET", "/tickets/"+tk.ID.String()+"/docs", nil), "staff@toji.homes"), "header.payload.sig")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 502, rec.Code)
}

func TestRefDocs_Content_ReturnsContent(t *testing.T) {
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{content: "# Foo\n"}, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=goquest/spec/foo.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"content":"# Foo\n","source_path":"goquest/spec/foo.md"}`, rec.Body.String())
}

func TestRefDocs_Content_StripsGenesoPrefix(t *testing.T) {
	fetcher := &fakeContentFetcher{content: "x"}
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, fetcher, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=geneso:goquest/spec/foo.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"content":"x","source_path":"goquest/spec/foo.md"}`, rec.Body.String())
}

func TestRefDocs_Content_MissingSourcePath_Returns400(t *testing.T) {
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{}, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 400, rec.Code)
}

func TestRefDocs_Content_NotFound_Returns404(t *testing.T) {
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{err: githubapp.ErrNotFound}, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=missing.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 404, rec.Code)
}

// asOrg mirrors ticket_org_reassign_test.go's helper (same package, but kept
// local to this test's call site for clarity since refdocs_test.go doesn't
// otherwise depend on that file).
func TestRefDocs_Content_CustomerToken_Returns403_DoesNotCallGitHub(t *testing.T) {
	fetcher := &fakeContentFetcher{content: "should never be returned"}
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, fetcher, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=neunexus/.hermes/SOUL.md", nil)
	req = asCustomer(req, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 403, rec.Code)
	assert.False(t, fetcher.called, "github Content fetcher must not be invoked for external customer callers")
}

func TestRefDocs_Content_OrgContext_Returns403_DoesNotCallGitHub(t *testing.T) {
	fetcher := &fakeContentFetcher{content: "should never be returned"}
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, fetcher, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=neunexus/.hermes/SOUL.md", nil)
	req = asOrg(req, &auth.OrgCtx{ID: uuid.New(), Role: "admin"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 403, rec.Code)
	assert.False(t, fetcher.called, "github Content fetcher must not be invoked for external org callers")
}

// TestRefDocs_Content_StaffWithOrgClaim_ReturnsContent reproduces the
// production bug (goq-org-claim-staff-content-comment-bypass): a staff
// account that also holds an org claim (e.g. lyckabc, dual-role for org
// e2e testing) must still be able to read internal doc content — the org
// claim only adds access, it must never subtract the access the caller
// already has as staff.
func TestRefDocs_Content_StaffWithOrgClaim_ReturnsContent(t *testing.T) {
	fetcher := &fakeContentFetcher{content: "# Foo\n"}
	staff := &fakeStaffChecker{workspaces: []*wskg.Workspace{{ID: uuid.New(), Name: "personal"}}}
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, fetcher, staff, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=goquest/spec/foo.md", nil)
	req = asUser(req, "lyckabc@gmail.com")
	req = req.WithContext(auth.WithOrgCtx(req.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "admin"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.True(t, fetcher.called)
}

func TestRefDocs_Content_InternalStaff_StillReturnsContent(t *testing.T) {
	fetcher := &fakeContentFetcher{content: "# Foo\n"}
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, fetcher, &fakeStaffChecker{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=goquest/spec/foo.md", nil)
	req = asUser(req, "staff@toji.homes")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.True(t, fetcher.called)
}
