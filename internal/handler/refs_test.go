package handler_test

// HTTP-level tests for the ticket-scoped refs endpoints (Task 7):
//
//   GET    /tickets/{id}/refs
//   POST   /tickets/{id}/refs
//   DELETE /tickets/{id}/refs/{l1_id}
//   PATCH  /tickets/{id}/refs/reorder
//
// These exercise the real router wiring from e2eRouter (authz_e2e_test.go) so
// that RequireTicketView is the actual gate, and the memStore now carries real
// in-memory ref behavior (faithful to the PG store contract). PG-backed
// integration coverage lives in internal/ticket/refs_store_test.go.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// ── helpers ─────────────────────────────────────────────────────────────────────

type refsResp struct {
	Refs []ticket.TicketRef `json:"refs"`
}

func decodeRefs(t *testing.T, rec *httptest.ResponseRecorder) refsResp {
	t.Helper()
	var rr refsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &rr); err != nil {
		t.Fatalf("decode refs: %v (body=%s)", err, rec.Body.String())
	}
	return rr
}

func postJSON(path string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func patchJSON(path string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("PATCH", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func delReq(path string) *http.Request { return httptest.NewRequest("DELETE", path, nil) }

// refsTestSetup creates one project-visibility ticket, an allow-all checker for
// "alice", and an e2eRouter wired to the (refs-aware) memStore.
func refsTestSetup() (router http.Handler, ticketID uuid.UUID) {
	proj := pid()
	tk := tProject(proj)
	store := newMemStore(tk)
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {tk.ID: true},
		},
	}
	return e2eRouter(store, checker), tk.ID
}

// ── tests ───────────────────────────────────────────────────────────────────────

func TestRefsHandler_AddAndList(t *testing.T) {
	router, tid := refsTestSetup()

	// POST /refs with two ids.
	rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a", "l1-b"},
	}), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST refs: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	added := decodeRefs(t, rec)
	if len(added.Refs) != 2 || added.Refs[0].GopediaL1ID != "l1-a" || added.Refs[1].GopediaL1ID != "l1-b" {
		t.Errorf("POST returned wrong refs: %+v", added.Refs)
	}

	// GET /refs returns the same ordered pair.
	rec = do(router, asUser(get("/tickets/"+tid.String()+"/refs"), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET refs: want 200, got %d", rec.Code)
	}
	listed := decodeRefs(t, rec)
	if len(listed.Refs) != 2 {
		t.Fatalf("GET refs: want 2, got %d (%+v)", len(listed.Refs), listed.Refs)
	}
	if listed.Refs[0].GopediaL1ID != "l1-a" || listed.Refs[1].GopediaL1ID != "l1-b" {
		t.Errorf("GET refs: wrong order: %+v", listed.Refs)
	}
	if listed.Refs[0].Position != 0 || listed.Refs[1].Position != 1 {
		t.Errorf("GET refs: wrong positions: %d, %d", listed.Refs[0].Position, listed.Refs[1].Position)
	}
}

// TestRefsHandler_AddRequiresAuth: the production router additionally fronts
// these routes with JWT/customer-token middleware that returns 401 for
// unprincipaled requests. In the test router we omit that outer layer; with no
// principal RequireTicketView's SpiceDB gate denies → 403. Either is a
// non-leak; the assertion below mirrors the actual code path under test.
func TestRefsHandler_AddRequiresAuth(t *testing.T) {
	router, tid := refsTestSetup()
	rec := do(router, postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-x"},
	}))
	if rec.Code != http.StatusForbidden {
		t.Errorf("want 403 (no principal → SpiceDB denies), got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestRefsHandler_AddDeniedWithoutViewPermission(t *testing.T) {
	proj := pid()
	tk := tProject(proj)
	store := newMemStore(tk)
	// checker grants alice nothing; alice is therefore denied at RequireTicketView.
	router := e2eRouter(store, &worldChecker{})
	rec := do(router, asUser(postJSON("/tickets/"+tk.ID.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-x"},
	}), "alice"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestRefsHandler_DeleteIdempotent(t *testing.T) {
	router, tid := refsTestSetup()
	// DELETE non-existent ref → 204.
	rec := do(router, asUser(delReq("/tickets/"+tid.String()+"/refs/never-attached"), "alice"))
	if rec.Code != http.StatusNoContent {
		t.Errorf("DELETE non-existent: want 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// Attach two, then DELETE one → 204; subsequent GET shows the other only.
	_ = do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a", "l1-b"},
	}), "alice"))
	rec = do(router, asUser(delReq("/tickets/"+tid.String()+"/refs/l1-a"), "alice"))
	if rec.Code != http.StatusNoContent {
		t.Errorf("DELETE existing: want 204, got %d", rec.Code)
	}
	rec = do(router, asUser(get("/tickets/"+tid.String()+"/refs"), "alice"))
	listed := decodeRefs(t, rec)
	if len(listed.Refs) != 1 || listed.Refs[0].GopediaL1ID != "l1-b" {
		t.Errorf("after delete: want [l1-b], got %+v", listed.Refs)
	}

	// Second DELETE of the same id is also 204 (idempotent).
	rec = do(router, asUser(delReq("/tickets/"+tid.String()+"/refs/l1-a"), "alice"))
	if rec.Code != http.StatusNoContent {
		t.Errorf("DELETE again: want 204, got %d", rec.Code)
	}
}

func TestRefsHandler_Reorder(t *testing.T) {
	router, tid := refsTestSetup()

	if rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a", "l1-b", "l1-c"},
	}), "alice")); rec.Code != http.StatusOK {
		t.Fatalf("seed POST: %d (%s)", rec.Code, rec.Body.String())
	}

	rec := do(router, asUser(patchJSON("/tickets/"+tid.String()+"/refs/reorder", map[string]any{
		"ordered_l1_ids": []string{"l1-c", "l1-a", "l1-b"},
	}), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH reorder: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	listed := decodeRefs(t, rec)
	if len(listed.Refs) != 3 {
		t.Fatalf("after reorder: want 3 refs, got %d", len(listed.Refs))
	}
	wantOrder := []string{"l1-c", "l1-a", "l1-b"}
	for i, w := range wantOrder {
		if listed.Refs[i].GopediaL1ID != w {
			t.Errorf("position %d: want %s, got %s", i, w, listed.Refs[i].GopediaL1ID)
		}
		if listed.Refs[i].Position != i {
			t.Errorf("position field %d: got %d", i, listed.Refs[i].Position)
		}
	}
}

func TestRefsHandler_AddRejectsDuplicates(t *testing.T) {
	router, tid := refsTestSetup()
	rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a", "l1-a"},
	}), "alice"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "duplicate") {
		t.Errorf("want error mentioning duplicate, got %s", rec.Body.String())
	}
}

func TestRefsHandler_AddRejectsConflict(t *testing.T) {
	router, tid := refsTestSetup()
	if rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a"},
	}), "alice")); rec.Code != http.StatusOK {
		t.Fatalf("seed: %d", rec.Code)
	}
	// re-attach the same id → ErrRefConflict → 409.
	rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a"},
	}), "alice"))
	if rec.Code != http.StatusConflict {
		t.Errorf("want 409, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// guard: ensure spicedb.NoopChecker still satisfies the interface signature we
// pass to e2eRouter elsewhere (compile-time check only).
var _ spicedb.PermissionChecker = (*spicedb.NoopChecker)(nil)
