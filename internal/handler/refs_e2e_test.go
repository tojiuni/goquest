package handler_test

// End-to-end scenarios from docs/specs §7 covering the goquest side of the
// gopedia ref-doc feature. The four tests below correspond to spec scenarios
// 1, 2, 3, and 8 (orchestrator/bot scenarios 4–6 are out of scope here).
//
// NOTE: SQL-level CASCADE is covered by TestRefs_CascadeOnTicketDelete in
// internal/ticket/refs_store_test.go. The cancel endpoint transitions status
// but does not DELETE the row, so there is no HTTP-level CASCADE scenario.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ── Scenario 1: full lifecycle through the HTTP boundary ────────────────────────

// TestRefsE2E_Lifecycle exercises POST /tickets/{id}/refs → GET /tickets/{id}
// (proving Refs serialization survives the HTTP round-trip via the ref_ids
// field) → GET /refs?gopedia_l1_id=X reverse lookup → DELETE /refs/{l1_id} →
// GET /tickets/{id}/refs (only the unaffected ref remains).
func TestRefsE2E_Lifecycle(t *testing.T) {
	router, tid := refsTestSetup()

	// 1. Attach two refs.
	rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-a", "l1-b"},
	}), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST refs: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// 2. GET /tickets/{id} → response body's ref_ids field reflects attached refs
	//    in position order (proves end-to-end serialization, not just the model
	//    unit test).
	rec = do(router, asUser(get("/tickets/"+tid.String()+"/"), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET ticket: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var ticketResp struct {
		ID     uuid.UUID `json:"id"`
		RefIDs []string  `json:"ref_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ticketResp); err != nil {
		t.Fatalf("decode ticket: %v (body=%s)", err, rec.Body.String())
	}
	if len(ticketResp.RefIDs) != 2 || ticketResp.RefIDs[0] != "l1-a" || ticketResp.RefIDs[1] != "l1-b" {
		t.Errorf("GET ticket ref_ids: want [l1-a l1-b], got %+v (body=%s)", ticketResp.RefIDs, rec.Body.String())
	}

	// 3. Reverse lookup finds the ticket.
	rec = do(router, asUser(get("/refs?gopedia_l1_id=l1-a"), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /refs: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rr := decodeReverse(t, rec)
	if len(rr.TicketIDs) != 1 || rr.TicketIDs[0] != tid.String() {
		t.Errorf("reverse lookup: want [%s], got %+v", tid, rr.TicketIDs)
	}

	// 4. DELETE one ref → 204.
	rec = do(router, asUser(delReq("/tickets/"+tid.String()+"/refs/l1-a"), "alice"))
	if rec.Code != http.StatusNoContent {
		t.Errorf("DELETE ref: want 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// 5. GET ticket-scoped refs → only l1-b remains.
	rec = do(router, asUser(get("/tickets/"+tid.String()+"/refs"), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET refs after delete: want 200, got %d", rec.Code)
	}
	listed := decodeRefs(t, rec)
	if len(listed.Refs) != 1 || listed.Refs[0].GopediaL1ID != "l1-b" {
		t.Errorf("after delete: want [l1-b], got %+v", listed.Refs)
	}
}

// ── Scenario 2: complete authz separation between goquest and gopedia ──────────

// TestRefsE2E_AuthzSeparation_GopediaACLNotChecked locks in the spec's
// "complete authz separation" policy: goquest stores whatever L1 ids the caller
// supplies (subject only to its own ticket authz) and never validates them
// against gopedia. A caller with ticket view permission can attach arbitrary
// strings, including ids of private/nonexistent gopedia docs, and the request
// must succeed.
func TestRefsE2E_AuthzSeparation_GopediaACLNotChecked(t *testing.T) {
	router, tid := refsTestSetup()

	rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-private-secret", "l1-does-not-exist"},
	}), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST refs: want 200 (goquest must not validate gopedia ACL), got %d (body=%s)",
			rec.Code, rec.Body.String())
	}
	added := decodeRefs(t, rec)
	if len(added.Refs) != 2 ||
		added.Refs[0].GopediaL1ID != "l1-private-secret" ||
		added.Refs[1].GopediaL1ID != "l1-does-not-exist" {
		t.Errorf("attached refs: want both arbitrary ids stored verbatim, got %+v", added.Refs)
	}
}

// ── Scenario 3: reverse lookup does not leak across users ───────────────────────

// TestRefsE2E_ReverseLookupNoLeakAcrossUsers verifies that GET /refs returns
// an empty (but well-formed) result to a caller who cannot see any of the
// matching tickets. The response must use the literal "ticket_ids":[] (not
// null, not missing) so clients can iterate without null-guards. This is the
// non-existence-revealing no-leak invariant from the spec.
func TestRefsE2E_ReverseLookupNoLeakAcrossUsers(t *testing.T) {
	proj := pid()
	tk := tProject(proj)
	store := newMemStore(tk)
	// alice can see the ticket and seeds it; bob has no visibility on anything.
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {tk.ID: true},
		},
	}
	router := e2eRouter(store, checker)

	if rec := do(router, asUser(postJSON("/tickets/"+tk.ID.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-shared"},
	}), "alice")); rec.Code != http.StatusOK {
		t.Fatalf("seed POST as alice: %d (%s)", rec.Code, rec.Body.String())
	}

	rec := do(router, asUser(get("/refs?gopedia_l1_id=l1-shared"), "bob"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /refs as bob: want 200 (no-leak), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	rr := decodeReverse(t, rec)
	if len(rr.TicketIDs) != 0 {
		t.Errorf("want empty ticket_ids (no-leak across users), got %+v", rr.TicketIDs)
	}
	// Lock the empty-array literal to guarantee JS clients see [] not null.
	if !strings.Contains(rec.Body.String(), `"ticket_ids":[]`) {
		t.Errorf(`want "ticket_ids":[] literal in body, got %s`, rec.Body.String())
	}
}

// ── Scenario 8: org-axis regression smoke (no-op-shaped) ────────────────────────

// TestRefsE2E_OrgAxisCompat_NoRegression is a regression smoke test on the same
// shared fixtures that drive every other handler test in this package. The
// shared refsTestSetup already produces a ticket with project visibility wired
// the way the dual-write org-axis (workspace+project) path expects, so a green
// run here proves the refs path coexists with the org-axis changes from
// Tasks 4–9 without regression. Asserts the basic POST/GET round-trip works.
func TestRefsE2E_OrgAxisCompat_NoRegression(t *testing.T) {
	router, tid := refsTestSetup()

	rec := do(router, asUser(postJSON("/tickets/"+tid.String()+"/refs", map[string]any{
		"ref_ids": []string{"l1-org-axis-smoke"},
	}), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST refs: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	rec = do(router, asUser(get("/tickets/"+tid.String()+"/refs"), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET refs: want 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	listed := decodeRefs(t, rec)
	if len(listed.Refs) != 1 || listed.Refs[0].GopediaL1ID != "l1-org-axis-smoke" {
		t.Errorf("org-axis smoke: want [l1-org-axis-smoke], got %+v", listed.Refs)
	}
}

