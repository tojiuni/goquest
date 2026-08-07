package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

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

func TestRelations_ListBothDirections(t *testing.T) {
	router, a, b := relationsTestSetup()
	body := map[string]string{"relation_type": "blocks", "to_ticket_id": b.String()}
	do(router, asUser(postJSON("/tickets/"+a.String()+"/relations", body), "alice"))

	recA := do(router, asUser(get("/tickets/"+a.String()+"/relations"), "alice"))
	recB := do(router, asUser(get("/tickets/"+b.String()+"/relations"), "alice"))

	var listA, listB struct {
		Relations []ticket.TicketRelation `json:"relations"`
	}
	if err := json.Unmarshal(recA.Body.Bytes(), &listA); err != nil {
		t.Fatalf("decode a: %v", err)
	}
	if err := json.Unmarshal(recB.Body.Bytes(), &listB); err != nil {
		t.Fatalf("decode b: %v", err)
	}
	if len(listA.Relations) != 1 || len(listB.Relations) != 1 {
		t.Fatalf("want 1 relation on each side, got a=%d b=%d", len(listA.Relations), len(listB.Relations))
	}
}
