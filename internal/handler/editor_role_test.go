package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/cogitoclient"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// editorWorld has one ticket the caller can work (editor) and one they can both
// manage and work (manager).
type editorWorld struct {
	editorT, managerT *ticket.Ticket
	h                 *handler.TicketHandler
}

func newEditorWorld(t *testing.T) *editorWorld {
	t.Helper()
	pid := uuid.New()
	mk := func() *ticket.Ticket {
		return &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: ticket.StatusInProgress,
			Priority: ticket.PriorityNormal, Visibility: ticket.VisibilityProject, Title: "orig", Description: "orig"}
	}
	w := &editorWorld{editorT: mk(), managerT: mk()}
	store := &bulkStore{newMemStore(w.editorT, w.managerT)}
	checker := &manageChecker{worldChecker: &worldChecker{},
		allow: map[uuid.UUID]bool{w.managerT.ID: true},
		work:  map[uuid.UUID]bool{w.editorT.ID: true}}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	w.h = handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, &fakeCogitoSplitter{sections: nil})
	return w
}

func (w *editorWorld) patch(id uuid.UUID, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Patch("/tickets/{id}", w.h.Update)
	req := httptest.NewRequest("PATCH", "/tickets/"+id.String(), bytes.NewBufferString(body))
	req = req.WithContext(auth.WithRequesterID(req.Context(), "ws-editor"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestEditorPatch_AllowedFieldsApply(t *testing.T) {
	w := newEditorWorld(t)
	rec := w.patch(w.editorT.ID, `{"status":"pending_approval","title":"new title","description":"new desc","assignee_id":"bob","due_at":"2026-12-01T00:00:00Z","priority":"high"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("an editor's work fields must apply: %d %s", rec.Code, rec.Body)
	}
	var got ticket.Ticket
	json.Unmarshal(rec.Body.Bytes(), &got)
	// the in-memory store these tests share only persists status and assignee; the
	// other fields are checked for acceptance (200, nothing refused) here and for
	// persistence in the ticket package
	if got.Status != ticket.StatusPendingApproval || got.AssigneeID != "bob" {
		t.Errorf("not applied: status=%s assignee=%q", got.Status, got.AssigneeID)
	}
}

func TestEditorPatch_ManagerOnlyFieldsRejectTheWholeRequest(t *testing.T) {
	w := newEditorWorld(t)
	for name, tc := range map[string]struct {
		body   string
		fields []string
	}{
		"title_name":    {`{"title":"x","title_name":"goq-new-name"}`, []string{"title_name"}},
		"cancel":        {`{"status":"cancelled"}`, []string{"status"}},
		"visibility":    {`{"visibility":"restricted","priority":"low"}`, []string{"visibility"}},
		"tags":          {`{"tag_ids":[]}`, []string{"tag_ids"}},
		"approval meta": {`{"metadata":{"approval_decision":"approved"}}`, []string{"metadata"}},
		"several":       {`{"project_id":"` + uuid.NewString() + `","reward_xp":5}`, []string{"project_id", "reward_xp"}},
	} {
		rec := w.patch(w.editorT.ID, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: got %d want 403 (%s)", name, rec.Code, rec.Body)
			continue
		}
		var out struct {
			Reason string   `json:"reason"`
			Fields []string `json:"fields"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Reason != "field_requires_manage" || strings.Join(out.Fields, ",") != strings.Join(tc.fields, ",") {
			t.Errorf("%s: reason=%q fields=%v, want field_requires_manage %v", name, out.Reason, out.Fields, tc.fields)
		}
	}
	// all or nothing: the allowed half of a rejected request was not applied
	rec := w.patch(w.editorT.ID, `{"title":"half applied?","title_name":"goq-a-b"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("%d", rec.Code)
	}
	cur := w.editorT
	if cur.Title != "orig" {
		t.Errorf("a rejected request must not change anything, title is %q", cur.Title)
	}
}

func TestManagerPatch_IsNotRestricted(t *testing.T) {
	w := newEditorWorld(t)
	rec := w.patch(w.managerT.ID, `{"title_name":"goq-renamed-ok","visibility":"restricted","status":"cancelled"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a manager keeps every power: %d %s", rec.Code, rec.Body)
	}
}

func TestBulkUpdate_AppliesTheEditorRulePerTicket(t *testing.T) {
	w := newEditorWorld(t)
	body, _ := json.Marshal(map[string]any{
		"ids":   []string{w.editorT.ID.String(), w.managerT.ID.String()},
		"patch": map[string]any{"visibility": "restricted"},
	})
	r := chi.NewRouter()
	r.Post("/tickets/bulk", w.h.BulkUpdate)
	req := asUser(httptest.NewRequest("POST", "/tickets/bulk", bytes.NewReader(body)), "ws-editor")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var resp struct {
		Updated int `json:"updated"`
		Results []struct {
			ID    string `json:"id"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"results"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	got := map[string]struct {
		ok  bool
		err string
	}{}
	for _, row := range resp.Results {
		got[row.ID] = struct {
			ok  bool
			err string
		}{row.OK, row.Error}
	}
	if e := got[w.editorT.ID.String()]; e.ok || !strings.HasPrefix(e.err, "field_requires_manage") || !strings.Contains(e.err, "visibility") {
		t.Errorf("the ticket the caller can only work must be refused with the field named: %+v", e)
	}
	if m := got[w.managerT.ID.String()]; !m.ok {
		t.Errorf("the managed ticket is updated: %+v", m)
	}
	if resp.Updated != 1 {
		t.Errorf("updated = %d, want 1", resp.Updated)
	}
}

func TestSubtasksAndBulkCreate_AcceptAnEditorOnTheParent(t *testing.T) {
	pid := uuid.New()
	parent := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Type: ticket.TypeTask, Priority: ticket.PriorityNormal,
		Domain: ticket.DomainCode, Status: ticket.StatusPending, Visibility: ticket.VisibilityProject}
	for name, tc := range map[string]struct {
		work, allow map[uuid.UUID]bool
		want        int
	}{
		"editor":  {work: map[uuid.UUID]bool{parent.ID: true}, want: http.StatusOK},
		"manager": {allow: map[uuid.UUID]bool{parent.ID: true}, want: http.StatusOK},
		"member":  {want: http.StatusForbidden},
	} {
		store := &bulkCreateStore{newMemStore(parent)}
		checker := &manageChecker{worldChecker: &worldChecker{}, allow: tc.allow, work: tc.work}
		svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
		cogito := &fakeCogitoSplitter{sections: []cogitoclient.Section{{Title: "s", Contents: "c"}}}
		h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, cogito)

		body, _ := json.Marshal(map[string]any{"text": "some text", "title": "doc"})
		req := asUser(httptest.NewRequest("POST", "/tickets/"+parent.ID.String()+"/subtasks/from-text", bytes.NewReader(body)), "ws-x")
		rec := httptest.NewRecorder()
		subtasksFromTextRouter(h).ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("from-text as %s: got %d want %d (%s)", name, rec.Code, tc.want, rec.Body)
		}

		bc, _ := json.Marshal(map[string]any{"parent_id": parent.ID.String(), "source": "manual", "items": []map[string]any{{"title": "child"}}})
		r := chi.NewRouter()
		r.Post("/tickets/bulk-create", h.BulkCreate)
		rec2 := httptest.NewRecorder()
		r.ServeHTTP(rec2, asUser(httptest.NewRequest("POST", "/tickets/bulk-create", bytes.NewReader(bc)), "ws-x"))
		if rec2.Code != tc.want {
			t.Errorf("bulk-create as %s: got %d want %d (%s)", name, rec2.Code, tc.want, rec2.Body)
		}
	}
}
