package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/approval"
	"github.com/tojiuni/goquest/internal/auth"
)

func ruleRouter(svc DelegationRuleSvc, principal string, kind auth.PrincipalKind) http.Handler {
	h := NewDelegationRuleHandler(svc)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithPrincipalKind(auth.WithRequesterID(req.Context(), principal), kind)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/workspaces/{wid}/delegation-rules", func(r chi.Router) {
		r.Get("/", h.List)
		r.Post("/", h.Create)
		r.Delete("/{rid}", h.Revoke)
	})
	return r
}

func ruleDo(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
	return rec
}

func TestDelegationRuleHandler_Lifecycle(t *testing.T) {
	svc := approval.NewService(approval.NewMemStore()).WithRules(approval.NewMemRuleStore())
	ws := uuid.New().String()
	base := "/workspaces/" + ws + "/delegation-rules"
	person := ruleRouter(svc, "owner@x.io", auth.PrincipalHuman)

	rec := ruleDo(person, "POST", base+"/", `{"principal":"tokenhub-geneso","kinds":["merge"],"user_ref":"chat","created_by":"mallory"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var r approval.DelegationRule
	json.Unmarshal(rec.Body.Bytes(), &r)
	if r.CreatedBy != "owner@x.io" {
		t.Errorf("created_by must come from the principal, got %q", r.CreatedBy)
	}

	rec = ruleDo(person, "GET", base+"/", "")
	var list struct {
		Rules []approval.DelegationRule `json:"delegation_rules"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list.Rules) != 1 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec := ruleDo(person, "DELETE", base+"/"+r.ID.String(), ""); rec.Code != http.StatusNoContent {
		t.Errorf("revoke: %d", rec.Code)
	}
	if rec := ruleDo(person, "DELETE", base+"/"+r.ID.String(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("second revoke: got %d want 404", rec.Code)
	}
}

func TestDelegationRuleHandler_OnlyAPersonAndValidInput(t *testing.T) {
	svc := approval.NewService(approval.NewMemStore()).WithRules(approval.NewMemRuleStore())
	base := "/workspaces/" + uuid.New().String() + "/delegation-rules"
	good := `{"principal":"tokenhub-geneso","kinds":["merge"],"user_ref":"chat"}`

	// an owner-role agent / API key / device-code session still cannot create or revoke
	for _, k := range []auth.PrincipalKind{auth.PrincipalSession, auth.PrincipalOther, auth.PrincipalCustomer, ""} {
		h := ruleRouter(svc, "owner@x.io", k)
		if rec := ruleDo(h, "POST", base+"/", good); rec.Code != http.StatusForbidden {
			t.Errorf("create as %q: got %d want 403", k, rec.Code)
		}
		if rec := ruleDo(h, "DELETE", base+"/"+uuid.New().String(), ""); rec.Code != http.StatusForbidden {
			t.Errorf("revoke as %q: got %d want 403", k, rec.Code)
		}
	}
	person := ruleRouter(svc, "owner@x.io", auth.PrincipalHuman)
	for body, want := range map[string]int{
		`{`: http.StatusBadRequest,
		`{"principal":"lyckabc@gmail.com","kinds":["merge"],"user_ref":"x"}`:          http.StatusUnprocessableEntity,
		`{"principal":"p","kinds":["infra"],"user_ref":"x"}`:                          http.StatusUnprocessableEntity,
		`{"principal":"p","kinds":["merge"]}`:                                         http.StatusUnprocessableEntity,
		`{"principal":"p","kinds":["merge"],"user_ref":"x","conditions":{"vibes":1}}`: http.StatusUnprocessableEntity,
	} {
		if rec := ruleDo(person, "POST", base+"/", body); rec.Code != want {
			t.Errorf("%s: got %d want %d", body, rec.Code, want)
		}
	}
	if rec := ruleDo(person, "GET", "/workspaces/"+uuid.New().String()+"/delegation-rules/", ""); !bytes.Contains(rec.Body.Bytes(), []byte(`"delegation_rules":[]`)) {
		t.Errorf("empty list must be []: %s", rec.Body)
	}
}
