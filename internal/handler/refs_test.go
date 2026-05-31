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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/audit"
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

// fakeAudit is an in-memory audit.Logger test double. It records entries
// without touching Postgres, which keeps audit-verification tests hermetic
// (no GOQUEST_TEST_DATABASE_URL env requirement). It implements the full
// audit.Logger interface; List supports the same filter fields the production
// pgLogger does so tests assert against the public contract.
type fakeAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (f *fakeAudit) Record(_ context.Context, actor, action, targetKind, targetID string, detail map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy the detail map — callers may mutate the original after Record returns.
	cp := make(map[string]any, len(detail))
	for k, v := range detail {
		cp[k] = v
	}
	f.entries = append(f.entries, audit.Entry{
		ID:         int64(len(f.entries) + 1),
		Actor:      actor,
		Action:     action,
		TargetKind: targetKind,
		TargetID:   targetID,
		Detail:     cp,
	})
}

func (f *fakeAudit) List(_ context.Context, filter audit.ListFilter) ([]*audit.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*audit.Entry
	for i := range f.entries {
		e := f.entries[i]
		if filter.Actor != "" && e.Actor != filter.Actor {
			continue
		}
		if filter.Action != "" && e.Action != filter.Action {
			continue
		}
		if filter.TargetKind != "" && e.TargetKind != filter.TargetKind {
			continue
		}
		if filter.TargetID != "" && e.TargetID != filter.TargetID {
			continue
		}
		cp := e
		out = append(out, &cp)
	}
	return out, nil
}

// refsTestSetupWithAudit mirrors refsTestSetup but wires an in-memory
// audit.Logger into the TicketHandler so callers can assert recorded entries.
func refsTestSetupWithAudit(_ *testing.T) (router http.Handler, ticketID uuid.UUID, logger *fakeAudit) {
	proj := pid()
	tk := tProject(proj)
	store := newMemStore(tk)
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {tk.ID: true},
		},
	}
	logger = &fakeAudit{}
	return e2eRouterWithAudit(store, checker, logger), tk.ID, logger
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

// ── reverse-lookup tests (Task 8: GET /api/v1/refs?gopedia_l1_id=X) ─────────────

// reverseLookupResp matches the handler envelope {"ticket_ids": [...]}.
type reverseLookupResp struct {
	TicketIDs []string `json:"ticket_ids"`
}

func decodeReverse(t *testing.T, rec *httptest.ResponseRecorder) reverseLookupResp {
	t.Helper()
	var rr reverseLookupResp
	if err := json.Unmarshal(rec.Body.Bytes(), &rr); err != nil {
		t.Fatalf("decode reverse: %v (body=%s)", err, rec.Body.String())
	}
	return rr
}

// TestRefsHandler_ReverseLookup_UnrestrictedShadow: shadow/NoopChecker reports
// unrestricted=true, so all matching tickets are returned regardless of caller.
func TestRefsHandler_ReverseLookup_UnrestrictedShadow(t *testing.T) {
	proj := pid()
	t1 := tProject(proj)
	t2 := tProject(proj)
	store := newMemStore(t1, t2)
	router := e2eRouter(store, &spicedb.NoopChecker{})

	// Seed: attach the same ref to both tickets. The ticket-scoped POST is gated by
	// RequireTicketView; under NoopChecker that gate is open for any caller.
	for _, tid := range []uuid.UUID{t1.ID, t2.ID} {
		rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
			"ref_ids": []string{"l1-shared"},
		}), "alice"))
		if rec.Code != http.StatusOK {
			t.Fatalf("seed POST %s: %d (%s)", tid, rec.Code, rec.Body.String())
		}
	}

	rec := do(router, asUser(get("/refs?gopedia_l1_id=l1-shared"), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /refs: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rr := decodeReverse(t, rec)
	if len(rr.TicketIDs) != 2 {
		t.Errorf("want 2 ticket ids, got %d (%+v)", len(rr.TicketIDs), rr.TicketIDs)
	}
	gotSet := map[string]bool{}
	for _, id := range rr.TicketIDs {
		gotSet[id] = true
	}
	if !gotSet[t1.ID.String()] || !gotSet[t2.ID.String()] {
		t.Errorf("want both ticket ids in response, got %+v", rr.TicketIDs)
	}
}

// TestRefsHandler_ReverseLookup_FilteredByVisibility: under enforce, the
// caller's VisibleResourceIDs trims the result to only the tickets they can see.
func TestRefsHandler_ReverseLookup_FilteredByVisibility(t *testing.T) {
	proj := pid()
	t1 := tProject(proj)
	t2 := tProject(proj)
	store := newMemStore(t1, t2)
	// alice can see both (to seed refs on both); bob can see only t1.
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {t1.ID: true, t2.ID: true},
			"bob":   {t1.ID: true},
		},
	}
	router := e2eRouter(store, checker)

	// Seed both via alice.
	for _, tid := range []uuid.UUID{t1.ID, t2.ID} {
		rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
			"ref_ids": []string{"l1-shared"},
		}), "alice"))
		if rec.Code != http.StatusOK {
			t.Fatalf("seed POST %s: %d (%s)", tid, rec.Code, rec.Body.String())
		}
	}

	rec := do(router, asUser(get("/refs?gopedia_l1_id=l1-shared"), "bob"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /refs as bob: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rr := decodeReverse(t, rec)
	if len(rr.TicketIDs) != 1 || rr.TicketIDs[0] != t1.ID.String() {
		t.Errorf("want exactly [t1], got %+v", rr.TicketIDs)
	}
}

// TestRefsHandler_ReverseLookup_RequiresGopediaL1ID: missing query param → 400.
func TestRefsHandler_ReverseLookup_RequiresGopediaL1ID(t *testing.T) {
	proj := pid()
	tk := tProject(proj)
	store := newMemStore(tk)
	router := e2eRouter(store, &spicedb.NoopChecker{})

	rec := do(router, asUser(get("/refs"), "alice"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestRefsHandler_ReverseLookup_NoLeak: a caller who sees NO tickets must get
// 200 + empty list — NOT 403/404 (which would leak existence of the ref).
func TestRefsHandler_ReverseLookup_NoLeak(t *testing.T) {
	proj := pid()
	tk := tProject(proj)
	store := newMemStore(tk)
	// alice seeds; bob sees nothing.
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {tk.ID: true},
		},
	}
	router := e2eRouter(store, checker)

	if rec := do(router, asUser(postJSON("/tickets/"+tk.ID.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-secret"},
	}), "alice")); rec.Code != http.StatusOK {
		t.Fatalf("seed: %d (%s)", rec.Code, rec.Body.String())
	}

	rec := do(router, asUser(get("/refs?gopedia_l1_id=l1-secret"), "bob"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /refs as bob: want 200 (no-leak), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rr := decodeReverse(t, rec)
	if len(rr.TicketIDs) != 0 {
		t.Errorf("want empty ticket_ids (no-leak), got %+v", rr.TicketIDs)
	}
	// Also ensure the JSON literal is an empty array, not null, so JS clients can
	// safely iterate without null-guards.
	if !strings.Contains(rec.Body.String(), `"ticket_ids":[]`) {
		t.Errorf("want ticket_ids:[] literal in body, got %s", rec.Body.String())
	}
}

// ── audit-logging tests (Task 11) ───────────────────────────────────────────────

// TestRefsHandler_AuditOnAdd: a successful POST /tickets/{id}/refs must produce
// one audit_log entry per attached ref, with action="ref.add", the caller as
// actor, the ticket id as target, and gopedia_l1_id in detail. Uses an
// in-memory audit.Logger double so the test is hermetic (no PG required).
func TestRefsHandler_AuditOnAdd(t *testing.T) {
	router, tid, auditLogger := refsTestSetupWithAudit(t)

	body := postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a"},
	})
	rec := do(router, asUser(body, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST refs: want 200, got %d", rec.Code)
	}

	entries, err := auditLogger.List(context.Background(), audit.ListFilter{
		Action:     "ref.add",
		TargetKind: "ticket",
		TargetID:   tid.String(),
	})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("want 1 audit entry, got %d", len(entries))
	} else {
		if entries[0].Actor != "alice" {
			t.Errorf("actor: want alice, got %q", entries[0].Actor)
		}
		if did, _ := entries[0].Detail["gopedia_l1_id"].(string); did != "l1-a" {
			t.Errorf("detail.gopedia_l1_id: want l1-a, got %v", entries[0].Detail["gopedia_l1_id"])
		}
	}
}
