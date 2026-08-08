package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

// relationsTestSetup mirrors refsTestSetup but seeds two project-visibility
// tickets (a, b) so relation tests have a distinct from/to pair.
func relationsTestSetup() (router http.Handler, ticketA, ticketB uuid.UUID) {
	proj := pid()
	a := tProject(proj)
	b := tProject(proj)
	store := newMemStore(a, b)
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {a.ID: true, b.ID: true},
		},
	}
	return e2eRouter(store, checker), a.ID, b.ID
}

type relationResp struct {
	Relation ticket.TicketRelation `json:"relation"`
	Created  bool                  `json:"created"`
}

// relationListItem mirrors the handler's flattened relationView response
// shape for GET .../relations (direction/label computed server-side, other-
// side ticket summary inlined, from_ticket_id/to_ticket_id/visibility omitted).
type relationListItem struct {
	ID                   uuid.UUID `json:"id"`
	RelationType         string    `json:"relation_type"`
	Direction            string    `json:"direction"`
	Label                string    `json:"label"`
	OtherTicketID        uuid.UUID `json:"other_ticket_id"`
	OtherTicketTitleName string    `json:"other_ticket_title_name"`
	OtherTicketTitle     string    `json:"other_ticket_title"`
	OtherTicketStatus    string    `json:"other_ticket_status"`
	CreatedAt            time.Time `json:"created_at"`
	CreatedBy            string    `json:"created_by"`
}

func decodeRelationList(t *testing.T, body []byte) []relationListItem {
	t.Helper()
	var v struct {
		Relations []relationListItem `json:"relations"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode relations list: %v (body=%s)", err, body)
	}
	return v.Relations
}

func TestRelations_AddIdempotent(t *testing.T) {
	router, a, b := relationsTestSetup()
	body := map[string]string{"relation_type": "blocks", "to_ticket_id": b.String()}

	req1 := asUser(postJSON("/tickets/"+a.String()+"/relations", body), "alice")
	rec1 := do(router, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first add: want 200, got %d (%s)", rec1.Code, rec1.Body.String())
	}
	var r1 relationResp
	if err := json.Unmarshal(rec1.Body.Bytes(), &r1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !r1.Created {
		t.Fatalf("want created=true on first add")
	}

	req2 := asUser(postJSON("/tickets/"+a.String()+"/relations", body), "alice")
	rec2 := do(router, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay: want 200, got %d (%s)", rec2.Code, rec2.Body.String())
	}
	var r2 relationResp
	if err := json.Unmarshal(rec2.Body.Bytes(), &r2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r2.Created {
		t.Fatalf("want created=false on replay")
	}
	if r1.Relation.ID != r2.Relation.ID {
		t.Fatalf("replay returned different id: %v vs %v", r1.Relation.ID, r2.Relation.ID)
	}
}

func TestRelations_AddSelfReference400(t *testing.T) {
	router, a, _ := relationsTestSetup()
	body := map[string]string{"relation_type": "blocks", "to_ticket_id": a.String()}

	req := asUser(postJSON("/tickets/"+a.String()+"/relations", body), "alice")
	rec := do(router, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestRelations_AddTargetNotFound404(t *testing.T) {
	router, a, _ := relationsTestSetup()
	body := map[string]string{"relation_type": "blocks", "to_ticket_id": uuid.New().String()}

	req := asUser(postJSON("/tickets/"+a.String()+"/relations", body), "alice")
	rec := do(router, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestRelations_RemoveNotOwnedByTicket404(t *testing.T) {
	proj := pid()
	a := tProject(proj)
	b := tProject(proj)
	c := tProject(proj)
	store := newMemStore(a, b, c)
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {a.ID: true, b.ID: true, c.ID: true},
		},
	}
	router := e2eRouter(store, checker)

	// Create a relation between b and c (unrelated to a).
	body := map[string]string{"relation_type": "blocks", "to_ticket_id": c.ID.String()}
	addRec := do(router, asUser(postJSON("/tickets/"+b.ID.String()+"/relations", body), "alice"))
	if addRec.Code != http.StatusOK {
		t.Fatalf("setup add: want 200, got %d (%s)", addRec.Code, addRec.Body.String())
	}
	var added relationResp
	if err := json.Unmarshal(addRec.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Attempt to delete it via ticket a's path — a is not part of this relation.
	delRec := do(router, asUser(delReq("/tickets/"+a.ID.String()+"/relations/"+added.Relation.ID.String()), "alice"))
	if delRec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for cross-ticket delete, got %d (%s)", delRec.Code, delRec.Body.String())
	}

	// Confirm the relation still exists (wasn't actually deleted).
	list := decodeRelationList(t, do(router, asUser(get("/tickets/"+b.ID.String()+"/relations"), "alice")).Body.Bytes())
	if len(list) != 1 {
		t.Fatalf("want relation to survive the rejected cross-ticket delete, got %d relations", len(list))
	}
}

func TestRelations_ListBothDirections(t *testing.T) {
	router, a, b := relationsTestSetup()
	body := map[string]string{"relation_type": "blocks", "to_ticket_id": b.String()}
	do(router, asUser(postJSON("/tickets/"+a.String()+"/relations", body), "alice"))

	listA := decodeRelationList(t, do(router, asUser(get("/tickets/"+a.String()+"/relations"), "alice")).Body.Bytes())
	listB := decodeRelationList(t, do(router, asUser(get("/tickets/"+b.String()+"/relations"), "alice")).Body.Bytes())

	if len(listA) != 1 || len(listB) != 1 {
		t.Fatalf("want 1 relation on each side, got a=%d b=%d", len(listA), len(listB))
	}

	// From a's perspective, a is the from-side ("outgoing"): "blocks" → "Blocks".
	if listA[0].Direction != "outgoing" || listA[0].Label != "Blocks" {
		t.Fatalf("want outgoing/Blocks from a's side, got direction=%q label=%q", listA[0].Direction, listA[0].Label)
	}
	if listA[0].OtherTicketID != b {
		t.Fatalf("want other_ticket_id=%v on a's side, got %v", b, listA[0].OtherTicketID)
	}

	// From b's perspective, b is the to-side ("incoming"): "blocks" → "Blocked by".
	if listB[0].Direction != "incoming" || listB[0].Label != "Blocked by" {
		t.Fatalf("want incoming/Blocked by from b's side, got direction=%q label=%q", listB[0].Direction, listB[0].Label)
	}
	if listB[0].OtherTicketID != a {
		t.Fatalf("want other_ticket_id=%v on b's side, got %v", a, listB[0].OtherTicketID)
	}
}

// TestRelations_AddTargetNotViewable404 covers Finding 1: a caller must be
// able to VIEW to_ticket_id, not just have it exist, before a relation can be
// written onto it. Without this check, requireTicketView on the path ticket
// only proves the caller can see the SOURCE side of the edge.
func TestRelations_AddTargetNotViewable404(t *testing.T) {
	proj := pid()
	a := tProject(proj)
	restricted := tRestricted(proj)
	store := newMemStore(a, restricted)
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {a.ID: true}, // alice can see a, but has no restricted-visibility grant at all
			"bob":   {a.ID: true}, // bob can see both, used below to verify nothing was created
		},
		restr: map[string]map[uuid.UUID]bool{
			"bob": {restricted.ID: true},
		},
	}
	router := e2eRouter(store, checker)

	body := map[string]string{"relation_type": "blocks", "to_ticket_id": restricted.ID.String()}
	rec := do(router, asUser(postJSON("/tickets/"+a.ID.String()+"/relations", body), "alice"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 (same as target-not-found, no existence oracle), got %d (%s)", rec.Code, rec.Body.String())
	}

	// Confirm no relation was actually created: list a's relations as bob, who
	// CAN view both a and the restricted ticket, so nothing should be filtered.
	list := decodeRelationList(t, do(router, asUser(get("/tickets/"+a.ID.String()+"/relations"), "bob")).Body.Bytes())
	if len(list) != 0 {
		t.Fatalf("want no relation created by the rejected add, got %d", len(list))
	}
}

// TestRelations_ListFiltersUnviewableOtherSide covers Finding 2's security
// half: a relation whose "other side" ticket the caller cannot view must be
// silently dropped from the response (not 403/error) — a rejection would
// still leak that a relation to *something* exists. The relation is seeded
// directly via the store (bypassing AddRelation's own visibility gate from
// Finding 1) to simulate the case where the caller's access to the other
// ticket changed after the relation was created.
func TestRelations_ListFiltersUnviewableOtherSide(t *testing.T) {
	proj := pid()
	a := tProject(proj)
	restricted := tRestricted(proj)
	store := newMemStore(a, restricted)
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {a.ID: true},
			"bob":   {a.ID: true},
		},
		restr: map[string]map[uuid.UUID]bool{
			"bob": {restricted.ID: true},
		},
	}
	router := e2eRouter(store, checker)

	if _, _, err := store.AddRelation(context.Background(), a.ID, restricted.ID, "blocks", "seed"); err != nil {
		t.Fatalf("seed relation: %v", err)
	}

	// alice can view a (the path ticket) but not the restricted other side:
	// the relation must be silently omitted, not surfaced as an error.
	aliceList := decodeRelationList(t, do(router, asUser(get("/tickets/"+a.ID.String()+"/relations"), "alice")).Body.Bytes())
	if len(aliceList) != 0 {
		t.Fatalf("want relation to unviewable other side omitted for alice, got %d", len(aliceList))
	}

	// Sanity: bob, who can view both sides, does see it.
	bobList := decodeRelationList(t, do(router, asUser(get("/tickets/"+a.ID.String()+"/relations"), "bob")).Body.Bytes())
	if len(bobList) != 1 {
		t.Fatalf("want bob (who can view both sides) to see the relation, got %d", len(bobList))
	}
}
