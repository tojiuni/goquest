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
	"github.com/tojiuni/goquest/internal/project"
)

// ── stub project service ──────────────────────────────────────────

type stubPJSvc struct {
	created   *project.Project
	listed    []*project.Project
	createErr error
	listErr   error
}

func (s *stubPJSvc) Create(_ context.Context, wid uuid.UUID, creatorID string, req project.CreateReq) (*project.Project, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.created != nil {
		return s.created, nil
	}
	return &project.Project{
		ID: uuid.New(), WorkspaceID: wid, Name: req.Name,
		Status: project.StatusActive, CreatedBy: creatorID,
	}, nil
}

func (s *stubPJSvc) Get(_ context.Context, id uuid.UUID) (*project.Project, error) {
	return &project.Project{ID: id, Name: "proj"}, nil
}

func (s *stubPJSvc) Update(_ context.Context, id uuid.UUID, _ project.UpdateReq) (*project.Project, error) {
	return &project.Project{ID: id}, nil
}

func (s *stubPJSvc) Delete(_ context.Context, _ uuid.UUID) error { return nil }

func (s *stubPJSvc) ListByWorkspace(_ context.Context, _ uuid.UUID) ([]*project.Project, error) {
	return s.listed, s.listErr
}

func (s *stubPJSvc) GetMember(_ context.Context, _ uuid.UUID, _ string) (*project.Member, error) {
	return nil, errors.New("not found")
}

func (s *stubPJSvc) ListMembers(_ context.Context, _ uuid.UUID) ([]*project.Member, error) {
	return nil, nil
}

func (s *stubPJSvc) AddMember(_ context.Context, _ uuid.UUID, _ project.SetRoleReq, _ string) error {
	return nil
}

func (s *stubPJSvc) SetMemberRole(_ context.Context, _ uuid.UUID, _ string, _ project.SetRoleReq) error {
	return nil
}

func (s *stubPJSvc) RemoveMember(_ context.Context, _ uuid.UUID, _ string) error { return nil }

// ── ProjectHandler.Create ─────────────────────────────────────────

func TestProjectHandler_Create_201(t *testing.T) {
	stub := &stubPJSvc{}
	h := handler.NewProjectHandler(stub)

	wid := uuid.New()
	body, _ := json.Marshal(project.CreateReq{Name: "Sprint 1"})

	r := chi.NewRouter()
	r.Use(auth.Middleware("", true))
	r.Post("/workspaces/{wid}/projects", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+wid.String()+"/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body: %s", rr.Code, rr.Body.String())
	}
	var out project.Project
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "Sprint 1" {
		t.Errorf("name = %q, want Sprint 1", out.Name)
	}
}

func TestProjectHandler_Create_BadBody(t *testing.T) {
	stub := &stubPJSvc{}
	h := handler.NewProjectHandler(stub)

	wid := uuid.New()
	r := chi.NewRouter()
	r.Use(auth.Middleware("", true))
	r.Post("/workspaces/{wid}/projects", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+wid.String()+"/projects", bytes.NewReader([]byte("bad")))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// ── ProjectHandler.List ───────────────────────────────────────────

func TestProjectHandler_List_200(t *testing.T) {
	wid := uuid.New()
	stub := &stubPJSvc{
		listed: []*project.Project{
			{ID: uuid.New(), WorkspaceID: wid, Name: "proj-1"},
			{ID: uuid.New(), WorkspaceID: wid, Name: "proj-2"},
		},
	}
	h := handler.NewProjectHandler(stub)

	r := chi.NewRouter()
	r.Use(auth.Middleware("", true))
	r.Get("/workspaces/{wid}/projects", h.List)

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+wid.String()+"/projects", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	var out []*project.Project
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("expected 2 projects, got %d", len(out))
	}
}
