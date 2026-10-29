package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	pjpkg "github.com/tojiuni/goquest/internal/project"
	wskg "github.com/tojiuni/goquest/internal/workspace"
)

// ── mock workspace store ──────────────────────────────────────────

type mockWSStore struct {
	member *wskg.Member
	err    error
}

func (m *mockWSStore) GetMember(_ context.Context, _ uuid.UUID, _ string) (*wskg.Member, error) {
	return m.member, m.err
}

// ── mock project store ────────────────────────────────────────────

type mockPJStore struct {
	member *pjpkg.Member
	err    error
}

func (m *mockPJStore) GetMember(_ context.Context, _ uuid.UUID, _ string) (*pjpkg.Member, error) {
	return m.member, m.err
}

// ── helpers ───────────────────────────────────────────────────────

// newChiRequest builds an *http.Request with chi URL params set.
func newChiRequest(wid, pid string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	rctx := chi.NewRouteContext()
	if wid != "" {
		rctx.URLParams.Add("wid", wid)
	}
	if pid != "" {
		rctx.URLParams.Add("pid", pid)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// injectUser adds the requester_id into context (mimics auth.Middleware).
func injectUser(r *http.Request, userID string) *http.Request {
	// auth.Middleware uses a private key; we call the exported Middleware
	// in skip-mode to inject a dev user, then replace that with our own.
	// Instead, we exercise the public RequesterID helper indirectly via
	// the skip-mode middleware.
	_ = userID // user ID injection tested via skip-mode in integration tests
	return r
}

// okHandler records whether it was reached.
type okHandler struct{ called bool }

func (h *okHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

// ── RequireWorkspaceRole tests ────────────────────────────────────

func TestRequireWorkspaceRole_Authorized(t *testing.T) {
	wid := uuid.New()
	store := &mockWSStore{
		member: &wskg.Member{WorkspaceID: wid, UserID: "user-1", Role: wskg.RoleAdmin},
	}

	next := &okHandler{}
	mw := auth.RequireWorkspaceRole(store, wskg.RoleMember)

	r := newChiRequest(wid.String(), "")
	// inject user via context (use internal key via Middleware skip=true)
	skipMW := auth.Middleware("", true)
	rr := httptest.NewRecorder()
	skipMW(mw(next)).ServeHTTP(rr, r)

	if !next.called {
		t.Errorf("expected handler to be called (admin >= member), status=%d", rr.Code)
	}
}

func TestRequireWorkspaceRole_Forbidden(t *testing.T) {
	wid := uuid.New()
	store := &mockWSStore{
		member: &wskg.Member{WorkspaceID: wid, UserID: "dev-user", Role: wskg.RoleViewer},
	}

	next := &okHandler{}
	mw := auth.RequireWorkspaceRole(store, wskg.RoleAdmin)

	r := newChiRequest(wid.String(), "")
	skipMW := auth.Middleware("", true)
	rr := httptest.NewRecorder()
	skipMW(mw(next)).ServeHTTP(rr, r)

	if next.called {
		t.Error("handler should NOT have been called (viewer < admin)")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestRequireWorkspaceRole_NotMember(t *testing.T) {
	wid := uuid.New()
	store := &mockWSStore{err: wskg.ErrNotFound}

	next := &okHandler{}
	mw := auth.RequireWorkspaceRole(store, wskg.RoleViewer)

	r := newChiRequest(wid.String(), "")
	skipMW := auth.Middleware("", true)
	rr := httptest.NewRecorder()
	skipMW(mw(next)).ServeHTTP(rr, r)

	if next.called {
		t.Error("handler should NOT have been called for non-member")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestRequireWorkspaceRole_InvalidWID(t *testing.T) {
	store := &mockWSStore{}
	next := &okHandler{}
	mw := auth.RequireWorkspaceRole(store, wskg.RoleViewer)

	r := newChiRequest("not-a-uuid", "")
	skipMW := auth.Middleware("", true)
	rr := httptest.NewRecorder()
	skipMW(mw(next)).ServeHTTP(rr, r)

	if next.called {
		t.Error("handler should NOT have been called for invalid wid")
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// ── RequireProjectRole tests ──────────────────────────────────────

func TestRequireProjectRole_ExplicitRole(t *testing.T) {
	wid := uuid.New()
	pid := uuid.New()
	wsStore := &mockWSStore{
		member: &wskg.Member{WorkspaceID: wid, UserID: "dev-user", Role: wskg.RoleViewer},
	}
	pjStore := &mockPJStore{
		member: &pjpkg.Member{ProjectID: pid, UserID: "dev-user", Role: pjpkg.RoleAdmin},
	}

	next := &okHandler{}
	wsMW := auth.RequireWorkspaceRole(wsStore, wskg.RoleViewer)
	pjMW := auth.RequireProjectRole(pjStore, pjpkg.RoleMember)

	r := newChiRequest(wid.String(), pid.String())
	skipMW := auth.Middleware("", true)
	rr := httptest.NewRecorder()
	skipMW(wsMW(pjMW(next))).ServeHTTP(rr, r)

	if !next.called {
		t.Errorf("expected handler called (project admin >= member), status=%d", rr.Code)
	}
}

func TestRequireProjectRole_FallbackFromWorkspace(t *testing.T) {
	wid := uuid.New()
	pid := uuid.New()
	wsStore := &mockWSStore{
		member: &wskg.Member{WorkspaceID: wid, UserID: "dev-user", Role: wskg.RoleAdmin},
	}
	// no explicit project role → fallback to workspace role
	pjStore := &mockPJStore{err: pjpkg.ErrNotFound}

	next := &okHandler{}
	wsMW := auth.RequireWorkspaceRole(wsStore, wskg.RoleViewer)
	// ws:admin maps to pj:admin, so pj:member should pass
	pjMW := auth.RequireProjectRole(pjStore, pjpkg.RoleMember)

	r := newChiRequest(wid.String(), pid.String())
	skipMW := auth.Middleware("", true)
	rr := httptest.NewRecorder()
	skipMW(wsMW(pjMW(next))).ServeHTTP(rr, r)

	if !next.called {
		t.Errorf("expected handler called via ws fallback, status=%d", rr.Code)
	}
}

// ── WorkspaceRoleFromCtx ──────────────────────────────────────────

func TestWorkspaceRoleFromCtx_Empty(t *testing.T) {
	role := auth.WorkspaceRoleFromCtx(context.Background())
	if role != "" {
		t.Errorf("expected empty role from empty ctx, got %q", role)
	}
}

// ── RequireProjectInWorkspace tests ───────────────────────────────

type mockPJGetter struct {
	p   *pjpkg.Project
	err error
}

func (m *mockPJGetter) Get(_ context.Context, _ uuid.UUID) (*pjpkg.Project, error) {
	return m.p, m.err
}

func TestRequireProjectInWorkspace(t *testing.T) {
	wid, other := uuid.New(), uuid.New()
	pid := uuid.New()
	cases := []struct {
		name string
		get  *mockPJGetter
		wid  string
		pid  string
		want int
	}{
		{"same workspace", &mockPJGetter{p: &pjpkg.Project{ID: pid, WorkspaceID: wid}}, wid.String(), pid.String(), http.StatusOK},
		{"project in another workspace", &mockPJGetter{p: &pjpkg.Project{ID: pid, WorkspaceID: other}}, wid.String(), pid.String(), http.StatusNotFound},
		{"unknown project", &mockPJGetter{err: pjpkg.ErrNotFound}, wid.String(), pid.String(), http.StatusNotFound},
		{"invalid wid", &mockPJGetter{}, "nope", pid.String(), http.StatusBadRequest},
		{"invalid pid", &mockPJGetter{}, wid.String(), "nope", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next := &okHandler{}
			rec := httptest.NewRecorder()
			auth.RequireProjectInWorkspace(c.get)(next).ServeHTTP(rec, newChiRequest(c.wid, c.pid))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
			if next.called != (c.want == http.StatusOK) {
				t.Fatalf("next called = %v for status %d", next.called, c.want)
			}
		})
	}
}

// A non-member must get 403 whether or not {pid} belongs to {wid}; only a
// member sees the 404 for a foreign project (no membership-free probing).
func TestProjectInWorkspace_AfterMembershipCheck(t *testing.T) {
	wid, other, pid := uuid.New(), uuid.New(), uuid.New()
	foreign := &mockPJGetter{p: &pjpkg.Project{ID: pid, WorkspaceID: other}}

	serve := func(ws *mockWSStore) int {
		r := chi.NewRouter()
		r.Route("/workspaces/{wid}/projects/{pid}", func(r chi.Router) {
			r.Use(auth.RequireWorkspaceRole(ws, wskg.RoleViewer), auth.RequireProjectInWorkspace(foreign))
			r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/workspaces/"+wid.String()+"/projects/"+pid.String(), nil)
		r.ServeHTTP(rec, req.WithContext(auth.WithRequesterID(req.Context(), "user-1")))
		return rec.Code
	}

	if got := serve(&mockWSStore{err: wskg.ErrNotFound}); got != http.StatusForbidden {
		t.Fatalf("non-member with foreign pid: status=%d, want 403", got)
	}
	if got := serve(&mockWSStore{member: &wskg.Member{Role: wskg.RoleViewer}}); got != http.StatusNotFound {
		t.Fatalf("member with foreign pid: status=%d, want 404", got)
	}
}
