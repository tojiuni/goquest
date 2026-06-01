package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
)

// fakeContractSvc is a test double for handler.ContractService.
type fakeContractSvc struct {
	approveErr error
	approveOut map[string]any
	abortErr   error
	abortOut   map[string]any

	// recorded call arguments
	lastActor    string
	lastWS       string
	lastWFID     string
	lastDecision bool
}

func (f *fakeContractSvc) Approve(_ context.Context, actor, ws, wfID string, decision bool) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	f.lastDecision = decision
	return f.approveOut, f.approveErr
}

func (f *fakeContractSvc) Abort(_ context.Context, actor, ws, wfID string) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	return f.abortOut, f.abortErr
}

// withTestUser injects a requester ID into the request context using the
// existing auth.WithRequesterID helper (same pattern as authz_e2e_test.go).
func withTestUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(auth.WithRequesterID(r.Context(), userID))
}

// contractRouter builds a minimal chi router with approve and abort endpoints.
func contractRouter(h *handler.ContractHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/contracts/{wf_id}/approve", h.Approve)
	r.Post("/api/v1/contracts/{wf_id}/abort", h.Abort)
	return r
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// ── Approve tests ─────────────────────────────────────────────────────────────

func TestContractApprove_200(t *testing.T) {
	svc := &fakeContractSvc{
		approveOut: map[string]any{"status": "approved"},
	}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-123/approve?workspace_id=ws-abc",
		jsonBody(t, map[string]any{"decision": true}))
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastActor != "user@example.com" {
		t.Errorf("actor: got %q want %q", svc.lastActor, "user@example.com")
	}
	if svc.lastWS != "ws-abc" {
		t.Errorf("workspace: got %q want %q", svc.lastWS, "ws-abc")
	}
	if svc.lastWFID != "wf-123" {
		t.Errorf("wfID: got %q want %q", svc.lastWFID, "wf-123")
	}
	if !svc.lastDecision {
		t.Error("expected decision=true")
	}
}

func TestContractApprove_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{approveErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-123/approve?workspace_id=ws-abc",
		jsonBody(t, map[string]any{"decision": false}))
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractApprove_502_UpstreamError(t *testing.T) {
	svc := &fakeContractSvc{approveErr: errors.New("connection refused")}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-123/approve?workspace_id=ws-abc",
		jsonBody(t, map[string]any{"decision": true}))
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractApprove_400_MissingWorkspace(t *testing.T) {
	svc := &fakeContractSvc{}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	// No workspace_id query param.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-123/approve",
		jsonBody(t, map[string]any{"decision": true}))
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── Abort tests ───────────────────────────────────────────────────────────────

func TestContractAbort_200(t *testing.T) {
	svc := &fakeContractSvc{
		abortOut: map[string]any{"status": "aborted"},
	}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-456/abort?workspace_id=ws-abc",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastWFID != "wf-456" {
		t.Errorf("wfID: got %q want %q", svc.lastWFID, "wf-456")
	}
}

func TestContractAbort_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{abortErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-456/abort?workspace_id=ws-abc",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractAbort_502_UpstreamError(t *testing.T) {
	svc := &fakeContractSvc{abortErr: errors.New("timeout")}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-456/abort?workspace_id=ws-abc",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractAbort_400_MissingWorkspace(t *testing.T) {
	svc := &fakeContractSvc{}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-456/abort",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractApprove_401_MissingActor(t *testing.T) {
	svc := &fakeContractSvc{}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	// No withTestUser call — actor is empty.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-123/approve?workspace_id=ws-abc",
		jsonBody(t, map[string]any{"decision": true}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}
