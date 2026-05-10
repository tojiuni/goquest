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
