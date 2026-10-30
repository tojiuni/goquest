package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/approval"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/ticket"
)

type decideFixture struct {
	store *approval.MemStore
	rules *approval.MemRuleStore
	ws    uuid.UUID
	tid   uuid.UUID
	pj    uuid.UUID
}

func newDecideFixture() *decideFixture {
	return &decideFixture{store: approval.NewMemStore(), rules: approval.NewMemRuleStore(),
		ws: uuid.New(), tid: uuid.New(), pj: uuid.New()}
}

func (f *decideFixture) router(principal string, kind auth.PrincipalKind) http.Handler {
	svc := approval.NewService(f.store).WithRules(f.rules)
	h := NewApprovalHandler(svc, func(context.Context, *ticket.Ticket) (uuid.UUID, error) { return f.ws, nil })
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithRequesterID(req.Context(), principal)
			ctx = auth.WithPrincipalKind(ctx, kind)
			ctx = auth.WithTicket(ctx, &ticket.Ticket{ID: f.tid, ProjectID: &f.pj})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/tickets/{id}/approvals/{aid}/decision", h.Decide)
	return r
}

func (f *decideFixture) pending(t *testing.T, kind approval.Kind, s approval.Subject) *approval.Approval {
	t.Helper()
	a, err := f.store.Create(context.Background(), approval.NewReq{TicketID: f.tid, Kind: kind, Subject: s, RequestedBy: "requester"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *decideFixture) decide(h http.Handler, a *approval.Approval, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/tickets/"+f.tid.String()+"/approvals/"+a.ID.String()+"/decision", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestApprovalDecide_HumanBodyCannotForgeIdentity(t *testing.T) {
	f := newDecideFixture()
	a := f.pending(t, approval.KindInfra, approval.Subject{Target: "vault:x"})
	rec := f.decide(f.router("user@x.io", auth.PrincipalHuman), a,
		`{"decision":"approved","note":"go","decided_by":"mallory","basis":"desk_delegated","status":"rejected"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got approval.Approval
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Status != approval.StatusApproved || *got.DecidedBy != "user@x.io" || *got.Basis != approval.BasisUserDirect {
		t.Errorf("identity must be server-derived: %+v", got)
	}
}

func TestApprovalDecide_NonHumanPrincipalsRefused(t *testing.T) {
	for _, kind := range []auth.PrincipalKind{auth.PrincipalOther, auth.PrincipalCustomer, ""} {
		f := newDecideFixture()
		a := f.pending(t, approval.KindMerge, approval.Subject{Repo: "o/r", PR: 1, HeadSHA: "a"})
		// even with a matching rule, an API-key/other principal cannot decide
		f.rules.Create(context.Background(), &approval.DelegationRule{WorkspaceID: f.ws, Principal: "desk", Kinds: []approval.Kind{approval.KindMerge}, CreatedBy: "u"})
		rec := f.decide(f.router("desk", kind), a, `{"decision":"approved","user_ref":"x"}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("kind %q: got %d want 403 (%s)", kind, rec.Code, rec.Body)
		}
		if got, _ := f.store.Get(context.Background(), a.ID); got.Status != approval.StatusPending {
			t.Errorf("kind %q: approval must stay pending", kind)
		}
	}
}

func TestApprovalDecide_SessionDelegation(t *testing.T) {
	f := newDecideFixture()
	f.rules.Create(context.Background(), &approval.DelegationRule{WorkspaceID: f.ws, Principal: "desk", Kinds: []approval.Kind{approval.KindMerge}, CreatedBy: "u"})
	h := f.router("desk", auth.PrincipalSession)

	// infra: over the ceiling regardless of rules
	infra := f.pending(t, approval.KindInfra, approval.Subject{Target: "vault:x"})
	rec := f.decide(h, infra, `{"decision":"approved","user_ref":"chat"}`)
	var e map[string]string
	json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != http.StatusForbidden || e["reason"] != "kind_not_delegable" {
		t.Errorf("infra: %d %s", rec.Code, rec.Body)
	}

	merge := f.pending(t, approval.KindMerge, approval.Subject{Repo: "o/r", PR: 1, HeadSHA: "a"})
	if rec := f.decide(h, merge, `{"decision":"approved"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing user_ref: got %d want 422", rec.Code)
	}
	rec = f.decide(h, merge, `{"decision":"approved","user_ref":"chat 2026-10-30","note":"ok"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body)
	}
	var got approval.Approval
	json.Unmarshal(rec.Body.Bytes(), &got)
	if *got.Basis != approval.BasisDeskDelegate || got.DelegationRuleID == nil || *got.DecidedBy != "desk" {
		t.Errorf("%+v", got)
	}
	// deciding again conflicts
	if rec := f.decide(h, merge, `{"decision":"rejected","note":"x","user_ref":"y"}`); rec.Code != http.StatusConflict {
		t.Errorf("redecide: got %d want 409", rec.Code)
	}
}

func TestApprovalDecide_BadInput(t *testing.T) {
	f := newDecideFixture()
	a := f.pending(t, approval.KindDesign, approval.Subject{Doc: "d.md"})
	h := f.router("u", auth.PrincipalHuman)
	for body, want := range map[string]int{
		`{`:                        http.StatusBadRequest,
		`{"decision":"withdrawn"}`: http.StatusUnprocessableEntity,
		`{"decision":"rejected"}`:  http.StatusUnprocessableEntity, // note required
		`{"decision":"revision_requested","note":""}`: http.StatusUnprocessableEntity,
	} {
		if rec := f.decide(h, a, body); rec.Code != want {
			t.Errorf("%s: got %d want %d", body, rec.Code, want)
		}
	}
	// approval id from another ticket
	other := newDecideFixture()
	b := other.pending(t, approval.KindDesign, approval.Subject{Doc: "d.md"})
	req := httptest.NewRequest("POST", "/tickets/"+f.tid.String()+"/approvals/"+b.ID.String()+"/decision", bytes.NewBufferString(`{"decision":"approved"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown approval: got %d want 404", rec.Code)
	}
}
