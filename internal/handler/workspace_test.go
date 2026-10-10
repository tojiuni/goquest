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
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/workspace"
)

// ── stub workspace service ────────────────────────────────────────

type stubWSSvc struct {
	addMemberErr error
	addMemberGot workspace.AddMemberReq
	created    *workspace.Workspace
	listed     []*workspace.Workspace
	got        *workspace.Workspace
	accepted   *workspace.Workspace
	createErr  error
	listErr    error
	getErr     error
	acceptErr  error
	updateErr  error
}

func (s *stubWSSvc) Create(_ context.Context, _ string, req workspace.CreateReq) (*workspace.Workspace, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.created == nil {
		s.created = &workspace.Workspace{ID: uuid.New(), Name: req.Name, Type: req.Type}
	}
	return s.created, nil
}

func (s *stubWSSvc) ListByUser(_ context.Context, _ string) ([]*workspace.Workspace, error) {
	return s.listed, s.listErr
}

func (s *stubWSSvc) Get(_ context.Context, id uuid.UUID) (*workspace.Workspace, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.got != nil {
		return s.got, nil
	}
	return &workspace.Workspace{ID: id, Name: "ws"}, nil
}

func (s *stubWSSvc) Update(_ context.Context, _ uuid.UUID, _ workspace.UpdateReq) (*workspace.Workspace, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return s.got, nil
}

func (s *stubWSSvc) Delete(_ context.Context, _ uuid.UUID) error { return nil }

func (s *stubWSSvc) GetMember(_ context.Context, _ uuid.UUID, _ string) (*workspace.Member, error) {
	return nil, workspace.ErrNotFound
}

func (s *stubWSSvc) ListMembers(_ context.Context, _ uuid.UUID) ([]*workspace.Member, error) {
	return nil, nil
}

func (s *stubWSSvc) SetMemberRole(_ context.Context, _ uuid.UUID, _ string, _ workspace.SetRoleReq, _ workspace.Role) error {
	return nil
}

func (s *stubWSSvc) AddMember(_ context.Context, _ uuid.UUID, req workspace.AddMemberReq, _ workspace.Role) error {
	s.addMemberGot = req
	return s.addMemberErr
}

func (s *stubWSSvc) RemoveMember(_ context.Context, _ uuid.UUID, _ string, _ workspace.Role) error {
	return nil
}

func (s *stubWSSvc) CreateInvitation(_ context.Context, _ uuid.UUID, _ string, _ workspace.InviteReq) (*workspace.Invitation, error) {
	return &workspace.Invitation{ID: uuid.New(), Token: "tok"}, nil
}

func (s *stubWSSvc) ListInvitations(_ context.Context, _ uuid.UUID) ([]*workspace.Invitation, error) {
	return nil, nil
}

func (s *stubWSSvc) DeleteInvitation(_ context.Context, _ uuid.UUID) error { return nil }

func (s *stubWSSvc) EnsurePersonalWorkspace(_ context.Context, _, _ string) (*workspace.Workspace, error) {
	return s.created, nil
}

func (s *stubWSSvc) AcceptInvitation(_ context.Context, _ string, _ string) (*workspace.Workspace, error) {
	if s.acceptErr != nil {
		return nil, s.acceptErr
	}
	return s.accepted, nil
}

// ── test helpers ──────────────────────────────────────────────────

func skipAuthRouter(h http.Handler) http.Handler {
	return auth.Middleware("", true)(h)
}

func newRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(auth.Middleware("", true)) // skip=true injects "dev-user"
	return r
}

// ── WorkspaceHandler.Create ───────────────────────────────────────

func TestWorkspaceHandler_Create_201(t *testing.T) {
	stub := &stubWSSvc{}
	h := handler.NewWorkspaceHandler(stub)

	body, _ := json.Marshal(workspace.CreateReq{Name: "My Guild", Type: workspace.TypeShared})
	r := newRouter()
	r.Post("/workspaces", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var out workspace.Workspace
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Name != "My Guild" {
		t.Errorf("name = %q, want %q", out.Name, "My Guild")
	}
}

func TestWorkspaceHandler_Create_BadBody(t *testing.T) {
	stub := &stubWSSvc{}
	h := handler.NewWorkspaceHandler(stub)

	r := newRouter()
	r.Post("/workspaces", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader([]byte("not-json")))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// ── WorkspaceHandler.List ─────────────────────────────────────────

func TestWorkspaceHandler_List_200(t *testing.T) {
	stub := &stubWSSvc{
		listed: []*workspace.Workspace{
			{ID: uuid.New(), Name: "ws-1"},
			{ID: uuid.New(), Name: "ws-2"},
		},
	}
	h := handler.NewWorkspaceHandler(stub)

	r := newRouter()
	r.Get("/workspaces", h.List)

	req := httptest.NewRequest(http.MethodGet, "/workspaces", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var out struct {
		Workspaces []*workspace.Workspace `json:"workspaces"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Workspaces) != 2 {
		t.Errorf("expected 2 workspaces, got %d", len(out.Workspaces))
	}
}

func TestWorkspaceHandler_List_Empty(t *testing.T) {
	stub := &stubWSSvc{listed: []*workspace.Workspace{}}
	h := handler.NewWorkspaceHandler(stub)

	r := newRouter()
	r.Get("/workspaces", h.List)

	req := httptest.NewRequest(http.MethodGet, "/workspaces", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// ── WorkspaceHandler.AcceptInvitation ────────────────────────────

// ── WorkspaceHandler.Update ───────────────────────────────────────

func TestWorkspaceCreate_DuplicateName409(t *testing.T) {
	stub := &stubWSSvc{createErr: workspace.ErrDuplicateName}
	h := handler.NewWorkspaceHandler(stub)

	body, _ := json.Marshal(workspace.CreateReq{Name: "taken", Type: workspace.TypeShared})
	r := newRouter()
	r.Post("/workspaces", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", rr.Code, rr.Body.String())
	}
}

func TestWorkspaceUpdate_DuplicateName409(t *testing.T) {
	stub := &stubWSSvc{updateErr: workspace.ErrDuplicateName}
	h := handler.NewWorkspaceHandler(stub)

	body, _ := json.Marshal(map[string]string{"name": "taken"})
	id := uuid.New()

	r := newRouter()
	r.Patch("/workspaces/{wid}", h.Update)

	req := httptest.NewRequest(http.MethodPatch, "/workspaces/"+id.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", rr.Code, rr.Body.String())
	}
}

// ── WorkspaceHandler.AcceptInvitation ────────────────────────────

func TestWorkspaceHandler_AcceptInvitation_Success(t *testing.T) {
	wsID := uuid.New()
	stub := &stubWSSvc{
		accepted: &workspace.Workspace{ID: wsID, Name: "Team"},
	}
	h := handler.NewWorkspaceHandler(stub)

	r := newRouter()
	r.Post("/invitations/{token}/accept", h.AcceptInvitation)

	req := httptest.NewRequest(http.MethodPost, "/invitations/valid-token/accept", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
}

func TestWorkspaceHandler_AcceptInvitation_Error(t *testing.T) {
	stub := &stubWSSvc{acceptErr: workspace.ErrNotFound}
	h := handler.NewWorkspaceHandler(stub)

	r := newRouter()
	r.Post("/invitations/{token}/accept", h.AcceptInvitation)

	req := httptest.NewRequest(http.MethodPost, "/invitations/bad-token/accept", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// ── WorkspaceHandler.AddMember ────────────────────────────────────

func postAddMember(t *testing.T, stub *stubWSSvc, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := handler.NewWorkspaceHandler(stub)
	r := newRouter()
	r.Post("/workspaces/{wid}/members", h.AddMember)
	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+uuid.New().String()+"/members", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func TestWorkspaceHandler_AddMember_204(t *testing.T) {
	stub := &stubWSSvc{}
	rr := postAddMember(t, stub, `{"user_id":"cv2418@naver.com","role":"owner"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rr.Code, rr.Body.String())
	}
	if stub.addMemberGot.UserID != "cv2418@naver.com" || stub.addMemberGot.Role != workspace.RoleOwner {
		t.Errorf("req = %+v", stub.addMemberGot)
	}
}

func TestWorkspaceHandler_AddMember_ErrorMapping(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"invalid": {workspace.ErrInvalidMember, http.StatusBadRequest},
		"forbid":  {workspace.ErrOwnerRequired, http.StatusForbidden},
		"other":   {errors.New("boom"), http.StatusInternalServerError},
	}
	for name, c := range cases {
		rr := postAddMember(t, &stubWSSvc{addMemberErr: c.err}, `{"user_id":"u","role":"member"}`)
		if rr.Code != c.want {
			t.Errorf("%s: status = %d, want %d", name, rr.Code, c.want)
		}
	}
}

func TestWorkspaceHandler_AddMember_BadBody(t *testing.T) {
	rr := postAddMember(t, &stubWSSvc{}, "{not json")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
