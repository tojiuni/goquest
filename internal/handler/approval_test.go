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

func approvalRouter(principal string, customer bool) http.Handler {
	h := NewApprovalHandler(approval.NewService(approvalStore))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if principal != "" {
				ctx = auth.WithRequesterID(ctx, principal)
			}
			if customer {
				ctx = auth.WithCustomerCtx(ctx, &auth.CustomerCtx{})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Post("/approvals", h.Request)
		r.Get("/approvals", h.List)
		r.Get("/approvals/{aid}", h.Get)
		r.Post("/approvals/{aid}/withdraw", h.Withdraw)
	})
	return r
}

var approvalStore *approval.MemStore

func doApproval(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const mergeBody = `{"kind":"merge","subject":{"repo":"o/r","pr":1,"head_sha":"abc"},"request_note":"n"}`

func TestApprovalHandler_RequestDerivesIdentity(t *testing.T) {
	approvalStore = approval.NewMemStore()
	tid := uuid.New()
	body := `{"kind":"merge","subject":{"repo":"o/r","pr":1,"head_sha":"abc"},"requested_by":"mallory","decided_by":"mallory","status":"approved","basis":"user_direct"}`
	rec := doApproval(t, approvalRouter("desk-1", false), "POST", "/tickets/"+tid.String()+"/approvals", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var a approval.Approval
	json.Unmarshal(rec.Body.Bytes(), &a)
	if a.RequestedBy != "desk-1" || a.Status != approval.StatusPending || a.DecidedBy != nil || a.Basis != nil {
		t.Errorf("identity/status must be server-derived: %+v", a)
	}
}

func TestApprovalHandler_RequestErrors(t *testing.T) {
	approvalStore = approval.NewMemStore()
	tid := uuid.New().String()
	cases := []struct {
		name, principal, body string
		want                  int
	}{
		{"no principal", "", mergeBody, http.StatusForbidden},
		{"reserved kind", "u", `{"kind":"ticket","subject":{"ticket":"x"}}`, http.StatusUnprocessableEntity},
		{"merge missing head_sha", "u", `{"kind":"merge","subject":{"repo":"o/r","pr":1}}`, http.StatusUnprocessableEntity},
		{"deviation needs direction", "u", `{"kind":"deviation","subject":{"doc":"d.md"}}`, http.StatusUnprocessableEntity},
		{"bad json", "u", `{`, http.StatusBadRequest},
		{"bad evidence", "u", `{"kind":"design","subject":{"doc":"d"},"evidence":[{"type":"","ref":""}]}`, http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		rec := doApproval(t, approvalRouter(c.principal, false), "POST", "/tickets/"+tid+"/approvals", c.body)
		if rec.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
	}
	h := approvalRouter("u", false)
	if rec := doApproval(t, h, "POST", "/tickets/"+tid+"/approvals", mergeBody); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d", rec.Code)
	}
	if rec := doApproval(t, h, "POST", "/tickets/"+tid+"/approvals", mergeBody); rec.Code != http.StatusConflict {
		t.Errorf("duplicate pending: got %d want 409", rec.Code)
	}
}

func TestApprovalHandler_CustomerTokenDenied(t *testing.T) {
	approvalStore = approval.NewMemStore()
	tid := uuid.New().String()
	h := approvalRouter("cust", true)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/tickets/" + tid + "/approvals", mergeBody},
		{"GET", "/tickets/" + tid + "/approvals", ""},
		{"GET", "/tickets/" + tid + "/approvals/" + uuid.New().String(), ""},
		{"POST", "/tickets/" + tid + "/approvals/" + uuid.New().String() + "/withdraw", ""},
	} {
		if rec := doApproval(t, h, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d want 403", c.method, c.path, rec.Code)
		}
	}
}

func TestApprovalHandler_TicketScopeAndWithdraw(t *testing.T) {
	approvalStore = approval.NewMemStore()
	tidA, tidB := uuid.New().String(), uuid.New().String()
	rec := doApproval(t, approvalRouter("req", false), "POST", "/tickets/"+tidA+"/approvals", mergeBody)
	var a approval.Approval
	json.Unmarshal(rec.Body.Bytes(), &a)
	path := "/tickets/" + tidA + "/approvals/" + a.ID.String()
	crossPath := "/tickets/" + tidB + "/approvals/" + a.ID.String()

	if rec := doApproval(t, approvalRouter("req", false), "GET", path, ""); rec.Code != http.StatusOK {
		t.Errorf("get own ticket: %d", rec.Code)
	}
	if rec := doApproval(t, approvalRouter("req", false), "GET", crossPath, ""); rec.Code != http.StatusNotFound {
		t.Errorf("cross-ticket get: got %d want 404", rec.Code)
	}
	if rec := doApproval(t, approvalRouter("req", false), "POST", crossPath+"/withdraw", ""); rec.Code != http.StatusNotFound {
		t.Errorf("cross-ticket withdraw: got %d want 404", rec.Code)
	}
	if rec := doApproval(t, approvalRouter("someone-else", false), "POST", path+"/withdraw", ""); rec.Code != http.StatusForbidden {
		t.Errorf("non-requester withdraw: got %d want 403", rec.Code)
	}
	if rec := doApproval(t, approvalRouter("req", false), "POST", path+"/withdraw", `{"note":"changed my mind"}`); rec.Code != http.StatusOK {
		t.Fatalf("withdraw: %d %s", rec.Code, rec.Body)
	}
	if rec := doApproval(t, approvalRouter("req", false), "POST", path+"/withdraw", ""); rec.Code != http.StatusConflict {
		t.Errorf("second withdraw: got %d want 409", rec.Code)
	}

	var list struct{ Approvals []approval.Approval }
	rec = doApproval(t, approvalRouter("req", false), "GET", "/tickets/"+tidA+"/approvals?status=withdrawn", "")
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Approvals) != 1 || list.Approvals[0].Status != approval.StatusWithdrawn || len(list.Approvals[0].Events) != 0 {
		t.Errorf("list: %+v", list)
	}
	rec = doApproval(t, approvalRouter("req", false), "GET", "/tickets/"+tidB+"/approvals", "")
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"approvals":[]`)) {
		t.Errorf("empty list must be [] not null: %d %s", rec.Code, rec.Body)
	}
}
