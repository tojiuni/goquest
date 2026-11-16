package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/cogitoclient"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// hardeningWorld: caller "ws-editor" can view and work project ticket P and can
// only work (no view) restricted ticket R. R is hidden from the caller.
type hardeningWorld struct {
	P, R *ticket.Ticket
	svc  *ticket.Service
	h    *handler.TicketHandler
	pid  uuid.UUID
}

func newHardeningWorld(t *testing.T, pendingApproval bool) *hardeningWorld {
	t.Helper()
	pid := uuid.New()
	status := ticket.StatusInProgress
	if pendingApproval {
		status = ticket.StatusPendingApproval
	}
	mk := func(vis ticket.Visibility) *ticket.Ticket {
		return &ticket.Ticket{ID: uuid.New(), ProjectID: &pid, Status: status, Priority: ticket.PriorityNormal,
			Type: ticket.TypeTask, Domain: ticket.DomainCode, Visibility: vis, Title: "orig", Description: "orig"}
	}
	w := &hardeningWorld{P: mk(ticket.VisibilityProject), R: mk(ticket.VisibilityRestricted), pid: pid}
	st := newMemStore(w.P, w.R)
	checker := &manageChecker{
		worldChecker: &worldChecker{proj: map[string]map[uuid.UUID]bool{"ws-editor": {w.P.ID: true}}},
		work:         map[uuid.UUID]bool{w.P.ID: true, w.R.ID: true},
	}
	w.svc = ticket.NewService(&bulkStore{st}, noopPublisher{}, nil, nil, nil, nil)
	w.h = handler.NewTicketHandler(w.svc, checker, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil,
		&fakeCogitoSplitter{sections: []cogitoclient.Section{{Title: "s", Contents: "c"}}})
	return w
}

func (w *hardeningWorld) patch(t *testing.T, id uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Patch("/tickets/{id}", w.h.Update)
	req := asUser(httptest.NewRequest("PATCH", "/tickets/"+id.String(), bytes.NewBufferString(body)), "ws-editor")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// H1: while a ticket awaits approval, an editor may not move its status.
func TestEditorPatch_PendingApprovalStatusMoveNeedsManage(t *testing.T) {
	w := newHardeningWorld(t, true)
	for _, body := range []string{`{"status":"assigned"}`, `{"status":"pending_revision"}`, `{"status":"completed"}`} {
		rec := w.patch(t, w.P.ID, body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"status"`) {
			t.Errorf("%s while pending_approval: got %d (%s), want 403 naming status", body, rec.Code, rec.Body)
		}
	}
	// no status change on a pending ticket: the editor's other fields still apply
	if rec := w.patch(t, w.P.ID, `{"status":"pending_approval","title":"edited while waiting"}`); rec.Code != http.StatusOK {
		t.Errorf("same-status edit while pending_approval: got %d (%s), want 200", rec.Code, rec.Body)
	}
}

func TestEditorPatch_RequestApprovalFromInProgressStaysOpen(t *testing.T) {
	w := newHardeningWorld(t, false)
	if rec := w.patch(t, w.P.ID, `{"status":"pending_approval"}`); rec.Code != http.StatusOK {
		t.Errorf("editor asks for approval from in_progress: got %d (%s), want 200", rec.Code, rec.Body)
	}
}

func TestEditorPatch_CallbackURLNeedsManage(t *testing.T) {
	w := newHardeningWorld(t, false)
	rec := w.patch(t, w.P.ID, `{"callback_url":"https://example.com/hook"}`)
	var out struct {
		Reason string   `json:"reason"`
		Fields []string `json:"fields"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusForbidden || out.Reason != "field_requires_manage" || strings.Join(out.Fields, ",") != "callback_url" {
		t.Errorf("callback_url: got %d reason=%q fields=%v", rec.Code, out.Reason, out.Fields)
	}
}

// H2: a restricted ticket the caller cannot view is reported exactly like a
// missing id, and is not mutated.
func TestBulkUpdate_HiddenRestrictedTicketIsNotMutated(t *testing.T) {
	w := newHardeningWorld(t, false)
	missing := uuid.New().String()
	body, _ := json.Marshal(map[string]any{
		"ids":   []string{w.R.ID.String(), missing},
		"patch": map[string]any{"assignee_id": "ws-editor"},
	})
	r := chi.NewRouter()
	r.Post("/tickets/bulk", w.h.BulkUpdate)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, asUser(httptest.NewRequest("POST", "/tickets/bulk", bytes.NewReader(body)), "ws-editor"))

	var resp struct {
		Updated int `json:"updated"`
		Results []struct {
			ID    string `json:"id"`
			Error string `json:"error"`
		} `json:"results"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	errs := map[string]string{}
	for _, row := range resp.Results {
		errs[row.ID] = row.Error
	}
	if errs[w.R.ID.String()] != errs[missing] || errs[missing] != "ticket not found" {
		t.Errorf("hidden ticket must look like a missing one: hidden=%q missing=%q", errs[w.R.ID.String()], errs[missing])
	}
	if resp.Updated != 0 {
		t.Errorf("updated = %d, want 0", resp.Updated)
	}
	got, err := w.svc.Get(context.Background(), w.R.ID)
	if err != nil || got.AssigneeID == "ws-editor" {
		t.Errorf("hidden ticket was mutated: assignee=%q err=%v", got.AssigneeID, err)
	}
}

// H2: the bulk-create and subtask routes check the parent's view before work.
func TestParentRoutes_HiddenRestrictedParentLooksMissing(t *testing.T) {
	w := newHardeningWorld(t, false)
	missing := uuid.New()
	for name, parent := range map[string]uuid.UUID{"hidden": w.R.ID, "missing": missing} {
		bc, _ := json.Marshal(map[string]any{"parent_id": parent.String(), "source": "manual", "items": []map[string]any{{"title": "child"}}})
		r := chi.NewRouter()
		r.Post("/tickets/bulk-create", w.h.BulkCreate)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, asUser(httptest.NewRequest("POST", "/tickets/bulk-create", bytes.NewReader(bc)), "ws-editor"))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "parent ticket not found") {
			t.Errorf("bulk-create %s parent: got %d (%s), want 404 parent ticket not found", name, rec.Code, rec.Body)
		}

		body, _ := json.Marshal(map[string]any{"text": "some text", "title": "doc"})
		rec2 := httptest.NewRecorder()
		subtasksFromTextRouter(w.h).ServeHTTP(rec2, asUser(httptest.NewRequest("POST", "/tickets/"+parent.String()+"/subtasks/from-text", bytes.NewReader(body)), "ws-editor"))
		if rec2.Code != http.StatusNotFound || !strings.Contains(rec2.Body.String(), "parent ticket not found") {
			t.Errorf("from-text %s parent: got %d (%s), want 404 parent ticket not found", name, rec2.Code, rec2.Body)
		}
	}
}

// H2 control: a visible ticket the caller can work still takes bulk-create.
func TestParentRoutes_VisibleEditorParentStillWorks(t *testing.T) {
	w := newHardeningWorld(t, false)
	bc, _ := json.Marshal(map[string]any{"parent_id": w.P.ID.String(), "source": "manual", "items": []map[string]any{{"title": "child"}}})
	r := chi.NewRouter()
	r.Post("/tickets/bulk-create", w.h.BulkCreate)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, asUser(httptest.NewRequest("POST", "/tickets/bulk-create", bytes.NewReader(bc)), "ws-editor"))
	if rec.Code != http.StatusOK {
		t.Errorf("visible editor parent: got %d (%s), want 200", rec.Code, rec.Body)
	}
}
