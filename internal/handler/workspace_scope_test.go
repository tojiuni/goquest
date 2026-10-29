package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/macro"
	"github.com/tojiuni/goquest/internal/tag"
	"github.com/tojiuni/goquest/internal/workspace"
)

// Regression: workspace-level routes that address a child row by its own id
// (tags, macros, invitations) must 404 when the row isn't in the {wid} the
// caller was authorized for, and must not reach the mutating store call.

func wsChiReq(method string, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, "/", strings.NewReader("{}"))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

type scopeTagSvc struct {
	TagSvc
	t       *tag.Tag
	mutated bool
}

func (s *scopeTagSvc) Get(context.Context, uuid.UUID) (*tag.Tag, error) { return s.t, nil }
func (s *scopeTagSvc) Update(context.Context, uuid.UUID, tag.UpdateReq) (*tag.Tag, error) {
	s.mutated = true
	return s.t, nil
}
func (s *scopeTagSvc) Delete(context.Context, uuid.UUID) error {
	s.mutated = true
	return nil
}

type scopeMacroSvc struct {
	MacroSvc
	m       *macro.Macro
	mutated bool
}

func (s *scopeMacroSvc) Get(context.Context, uuid.UUID) (*macro.Macro, error) { return s.m, nil }
func (s *scopeMacroSvc) Update(context.Context, uuid.UUID, macro.UpdateReq) (*macro.Macro, error) {
	s.mutated = true
	return s.m, nil
}
func (s *scopeMacroSvc) Delete(context.Context, uuid.UUID) error {
	s.mutated = true
	return nil
}

type scopeInvSvc struct {
	WorkspaceSvc
	invs    []*workspace.Invitation
	deleted bool
}

func (s *scopeInvSvc) ListInvitations(context.Context, uuid.UUID) ([]*workspace.Invitation, error) {
	return s.invs, nil
}
func (s *scopeInvSvc) DeleteInvitation(context.Context, uuid.UUID) error {
	s.deleted = true
	return nil
}

func TestTagRoutes_RejectTagFromOtherWorkspace(t *testing.T) {
	wid, other, tid := uuid.New(), uuid.New(), uuid.New()
	svc := &scopeTagSvc{t: &tag.Tag{ID: tid, WorkspaceID: other}}
	h := NewTagHandler(svc)
	params := map[string]string{"wid": wid.String(), "tid": tid.String()}

	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"get": h.Get, "update": h.Update, "delete": h.Delete,
	} {
		rec := httptest.NewRecorder()
		fn(rec, wsChiReq(http.MethodGet, params))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404", name, rec.Code)
		}
	}
	if svc.mutated {
		t.Fatal("tag from another workspace was mutated")
	}

	svc.t.WorkspaceID = wid
	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"get": h.Get, "update": h.Update, "delete": h.Delete,
	} {
		rec := httptest.NewRecorder()
		fn(rec, wsChiReq(http.MethodGet, params))
		if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
			t.Errorf("own tag %s: status=%d, want 2xx", name, rec.Code)
		}
	}
}

func TestMacroRoutes_RejectMacroFromOtherWorkspace(t *testing.T) {
	wid, other, mid := uuid.New(), uuid.New(), uuid.New()
	svc := &scopeMacroSvc{m: &macro.Macro{ID: mid, WorkspaceID: other}}
	h := NewMacroHandler(svc)
	params := map[string]string{"wid": wid.String(), "mid": mid.String()}

	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"update": h.Update, "delete": h.Delete,
	} {
		rec := httptest.NewRecorder()
		fn(rec, wsChiReq(http.MethodPatch, params))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404", name, rec.Code)
		}
	}
	if svc.mutated {
		t.Fatal("macro from another workspace was mutated")
	}

	svc.m.WorkspaceID = wid
	for name, fn := range map[string]func(http.ResponseWriter, *http.Request){
		"update": h.Update, "delete": h.Delete,
	} {
		rec := httptest.NewRecorder()
		fn(rec, wsChiReq(http.MethodPatch, params))
		if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
			t.Errorf("own macro %s: status=%d, want 2xx", name, rec.Code)
		}
	}
}

func TestInvitationDelete_RejectsInvitationFromOtherWorkspace(t *testing.T) {
	wid, mine, foreign := uuid.New(), uuid.New(), uuid.New()
	svc := &scopeInvSvc{invs: []*workspace.Invitation{{ID: mine}}}
	h := NewWorkspaceHandler(svc)

	rec := httptest.NewRecorder()
	h.DeleteInvitation(rec, wsChiReq(http.MethodDelete, map[string]string{"wid": wid.String(), "iid": foreign.String()}))
	if rec.Code != http.StatusNotFound || svc.deleted {
		t.Fatalf("foreign invitation: status=%d deleted=%v, want 404 and no delete", rec.Code, svc.deleted)
	}

	rec = httptest.NewRecorder()
	h.DeleteInvitation(rec, wsChiReq(http.MethodDelete, map[string]string{"wid": wid.String(), "iid": mine.String()}))
	if rec.Code != http.StatusNoContent || !svc.deleted {
		t.Fatalf("own invitation: status=%d deleted=%v, want 204 and delete", rec.Code, svc.deleted)
	}
}
