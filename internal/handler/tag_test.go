package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/tag"
)

// --- stub ---

type stubTagSvc struct {
	created *tag.Tag
	list    []*tag.Tag
	err     error
}

func (s *stubTagSvc) Create(_ context.Context, _ uuid.UUID, _ string, req tag.CreateReq) (*tag.Tag, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &tag.Tag{ID: uuid.New(), Name: req.Name, Color: "#1677ff"}, nil
}

func (s *stubTagSvc) Get(_ context.Context, _ uuid.UUID) (*tag.Tag, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.list) > 0 {
		return s.list[0], nil
	}
	return nil, tag.ErrNotFound
}

func (s *stubTagSvc) Update(_ context.Context, _ uuid.UUID, _ tag.UpdateReq) (*tag.Tag, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.list) > 0 {
		return s.list[0], nil
	}
	return nil, tag.ErrNotFound
}

func (s *stubTagSvc) Delete(_ context.Context, _ uuid.UUID) error { return s.err }

func (s *stubTagSvc) ListByWorkspace(_ context.Context, _ uuid.UUID) ([]*tag.Tag, error) {
	return s.list, s.err
}

// --- helpers ---

func tagRouter(h *handler.TagHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/workspaces/{wid}/tags", h.Create)
	r.Get("/workspaces/{wid}/tags", h.List)
	r.Get("/workspaces/{wid}/tags/{tid}", h.Get)
	r.Patch("/workspaces/{wid}/tags/{tid}", h.Update)
	r.Delete("/workspaces/{wid}/tags/{tid}", h.Delete)
	return r
}

// --- tests ---

func TestTagHandler_Create(t *testing.T) {
	h := handler.NewTagHandler(&stubTagSvc{})
	body, _ := json.Marshal(map[string]string{"name": "infra"})
	wid := uuid.New()

	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+wid.String()+"/tags", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	tagRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}
}

func TestTagHandler_List(t *testing.T) {
	svc := &stubTagSvc{list: []*tag.Tag{{ID: uuid.New(), Name: "a"}, {ID: uuid.New(), Name: "b"}}}
	h := handler.NewTagHandler(svc)
	wid := uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+wid.String()+"/tags", nil)
	w := httptest.NewRecorder()

	tagRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	tags := resp["tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("len(tags) = %d, want 2", len(tags))
	}
}

func TestTagHandler_Get_NotFound(t *testing.T) {
	h := handler.NewTagHandler(&stubTagSvc{err: tag.ErrNotFound})
	wid, tid := uuid.New(), uuid.New()

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+wid.String()+"/tags/"+tid.String(), nil)
	w := httptest.NewRecorder()

	tagRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestTagHandler_Delete(t *testing.T) {
	h := handler.NewTagHandler(&stubTagSvc{})
	wid, tid := uuid.New(), uuid.New()

	req := httptest.NewRequest(http.MethodDelete, "/workspaces/"+wid.String()+"/tags/"+tid.String(), nil)
	w := httptest.NewRecorder()

	tagRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}
