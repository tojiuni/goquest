package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/resolve"
)

// stubResolver implements handler.ProjectResolver for testing.
type stubResolver struct {
	err error
	id  *uuid.UUID
}

func (s *stubResolver) ResolveProjectID(_ context.Context, _ resolve.Ref) (*uuid.UUID, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.id, nil
}

// ticketRouter sets up a minimal chi router with the ticket Create endpoint.
// svc, checker, and writer may be nil when the test only exercises the resolver
// error paths (which return before any of those are used).
func ticketRouter(h *handler.TicketHandler) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req)
		})
	})
	r.Post("/tickets", h.Create)
	return r
}

// nameRefBody returns a JSON body with workspace_name + project_name set and
// project_id absent — this triggers the resolver branch in TicketHandler.Create.
func nameRefBody(t *testing.T) *bytes.Reader {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"workspace_name": "my-workspace",
		"project_name":   "my-project",
		"title":          "test ticket",
	})
	return bytes.NewReader(b)
}

// ── resolver error-path tests ────────────────────────────────────────────────
// All three tests exercise the resolver branch that returns BEFORE h.svc is
// called, so passing nil for svc/checker/writer is safe.

func TestTicketCreate_ResolveWorkspaceNotFound_400(t *testing.T) {
	resolver := &stubResolver{err: resolve.ErrWorkspaceNotFound}
	h := handler.NewTicketHandler(nil, nil, nil, resolver, nil, nil)

	r := ticketRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/tickets", nameRefBody(t))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestTicketCreate_ResolveIncompleteRef_400(t *testing.T) {
	resolver := &stubResolver{err: resolve.ErrIncompleteRef}
	h := handler.NewTicketHandler(nil, nil, nil, resolver, nil, nil)

	r := ticketRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/tickets", nameRefBody(t))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

func TestTicketCreate_ResolveGenericError_500(t *testing.T) {
	resolver := &stubResolver{err: errors.New("db down")}
	h := handler.NewTicketHandler(nil, nil, nil, resolver, nil, nil)

	r := ticketRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/tickets", nameRefBody(t))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", rr.Code, rr.Body.String())
	}
}

// stubTagResolver implements handler.TagResolver for testing.
type stubTagResolver struct {
	ids []uuid.UUID
	err error
}

func (s stubTagResolver) ResolveTagIDs(_ context.Context, _ uuid.UUID, _ string, _ []string) ([]uuid.UUID, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.ids, nil
}

// tagNamesRefBody returns a JSON body with a resolvable workspace/project ref
// plus tag_names — this reaches the tag-resolution branch (after the project is
// resolved) so the test exercises ResolveTagIDs error mapping.
func tagNamesRefBody(t *testing.T) *bytes.Reader {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"workspace_name": "my-workspace",
		"project_name":   "my-project",
		"title":          "test ticket",
		"tag_names":      []string{"infra"},
	})
	return bytes.NewReader(b)
}

// TestCreate_TagResolveProjectNotFound_400 verifies that an input-class error
// from the tag resolver (the project disappeared between resolution and tag
// lookup) maps to 400, mirroring the project-resolver branch.
func TestCreate_TagResolveProjectNotFound_400(t *testing.T) {
	pid := uuid.New()
	resolver := &stubResolver{id: &pid}
	tags := stubTagResolver{err: project.ErrNotFound}
	h := handler.NewTicketHandler(nil, nil, nil, resolver, tags, nil)

	r := ticketRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/tickets", tagNamesRefBody(t))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
}

// TestCreate_TagResolveInfraError_500 verifies that an unexpected (infra) error
// from the tag resolver maps to 500, not 400 — a tag-store DB failure is not the
// caller's fault.
func TestCreate_TagResolveInfraError_500(t *testing.T) {
	pid := uuid.New()
	resolver := &stubResolver{id: &pid}
	tags := stubTagResolver{err: errors.New("db down")}
	h := handler.NewTicketHandler(nil, nil, nil, resolver, tags, nil)

	r := ticketRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/tickets", tagNamesRefBody(t))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body: %s", rr.Code, rr.Body.String())
	}
}

// TestCreate_TagNamesRequiresProject verifies that supplying tag_names without
// a resolvable project (no project_id and no workspace_name/project_name ref)
// is rejected with 400, even when a TagResolver is wired in.
func TestCreate_TagNamesRequiresProject(t *testing.T) {
	resolver := &stubResolver{}
	h := handler.NewTicketHandler(nil, nil, nil, resolver, stubTagResolver{}, nil)

	r := ticketRouter(h)
	body, _ := json.Marshal(map[string]any{
		"title":     "test ticket",
		"tag_names": []string{"infra"},
	})
	req := httptest.NewRequest(http.MethodPost, "/tickets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "tag_names requires a resolvable project") {
		t.Errorf("body = %s, want it to mention %q", rr.Body.String(), "tag_names requires a resolvable project")
	}
}
