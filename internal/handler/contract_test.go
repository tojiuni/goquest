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
	approveErr           error
	approveOut           map[string]any
	abortErr             error
	abortOut             map[string]any
	submissionApproveErr error
	submissionApproveOut map[string]any
	rejectSubmissionErr  error
	rejectSubmissionOut  map[string]any
	rejectPlanErr        error
	rejectPlanOut        map[string]any

	// recorded call arguments
	lastActor  string
	lastWS     string
	lastWFID   string
	lastReason string
}

func (f *fakeContractSvc) Approve(_ context.Context, actor, ws, wfID string) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	return f.approveOut, f.approveErr
}

func (f *fakeContractSvc) Abort(_ context.Context, actor, ws, wfID string) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	return f.abortOut, f.abortErr
}

func (f *fakeContractSvc) SubmissionApprove(_ context.Context, actor, ws, wfID string) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	return f.submissionApproveOut, f.submissionApproveErr
}

func (f *fakeContractSvc) RejectSubmission(_ context.Context, actor, ws, wfID, reason string) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	f.lastReason = reason
	return f.rejectSubmissionOut, f.rejectSubmissionErr
}

func (f *fakeContractSvc) RejectPlan(_ context.Context, actor, ws, wfID, reason string) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	f.lastReason = reason
	return f.rejectPlanOut, f.rejectPlanErr
}

// withTestUser injects a requester ID into the request context using the
// existing auth.WithRequesterID helper (same pattern as authz_e2e_test.go).
func withTestUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(auth.WithRequesterID(r.Context(), userID))
}

// contractRouter builds a minimal chi router with all contract endpoints.
func contractRouter(h *handler.ContractHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/contracts/{wf_id}/approve", h.Approve)
	r.Post("/api/v1/contracts/{wf_id}/abort", h.Abort)
	r.Post("/api/v1/contracts/{wf_id}/reject", h.RejectPlan)
	r.Post("/api/v1/goals/{wf_id}/submission-approve", h.SubmissionApprove)
	r.Post("/api/v1/goals/{wf_id}/reject", h.RejectSubmission)
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

	// No body required; actor comes from JWT (injected via withTestUser).
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-123/approve?workspace_id=ws-abc",
		http.NoBody)
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
}

func TestContractApprove_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{approveErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-123/approve?workspace_id=ws-abc",
		http.NoBody)
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
		http.NoBody)
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
		http.NoBody)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
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

// ── RejectPlan tests ──────────────────────────────────────────────────────────

func TestContractRejectPlan_200(t *testing.T) {
	svc := &fakeContractSvc{
		rejectPlanOut: map[string]any{"status": "rejected"},
	}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-789/reject?workspace_id=ws-abc",
		jsonBody(t, map[string]any{"reason": "budget exceeded"}))
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastActor != "approver@example.com" {
		t.Errorf("actor: got %q", svc.lastActor)
	}
	if svc.lastWFID != "wf-789" {
		t.Errorf("wfID: got %q", svc.lastWFID)
	}
	if svc.lastReason != "budget exceeded" {
		t.Errorf("reason: got %q", svc.lastReason)
	}
}

func TestContractRejectPlan_400_MissingReason(t *testing.T) {
	svc := &fakeContractSvc{}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-789/reject?workspace_id=ws-abc",
		jsonBody(t, map[string]any{})) // no reason field
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractRejectPlan_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{rejectPlanErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/contracts/wf-789/reject?workspace_id=ws-abc",
		jsonBody(t, map[string]any{"reason": "nope"}))
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── SubmissionApprove tests ───────────────────────────────────────────────────

func TestContractSubmissionApprove_200(t *testing.T) {
	svc := &fakeContractSvc{
		submissionApproveOut: map[string]any{"status": "approved"},
	}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/goals/wf-100/submission-approve?workspace_id=ws-xyz",
		http.NoBody)
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastActor != "approver@example.com" {
		t.Errorf("actor: got %q", svc.lastActor)
	}
	if svc.lastWFID != "wf-100" {
		t.Errorf("wfID: got %q", svc.lastWFID)
	}
	if svc.lastWS != "ws-xyz" {
		t.Errorf("workspace: got %q", svc.lastWS)
	}
}

func TestContractSubmissionApprove_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{submissionApproveErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/goals/wf-100/submission-approve?workspace_id=ws-xyz",
		http.NoBody)
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── RejectSubmission tests ────────────────────────────────────────────────────

func TestContractRejectSubmission_200(t *testing.T) {
	svc := &fakeContractSvc{
		rejectSubmissionOut: map[string]any{"status": "rejected"},
	}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/goals/wf-200/reject?workspace_id=ws-xyz",
		jsonBody(t, map[string]any{"reason": "not ready"}))
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastReason != "not ready" {
		t.Errorf("reason: got %q", svc.lastReason)
	}
	if svc.lastWFID != "wf-200" {
		t.Errorf("wfID: got %q", svc.lastWFID)
	}
}

func TestContractRejectSubmission_400_MissingReason(t *testing.T) {
	svc := &fakeContractSvc{}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/goals/wf-200/reject?workspace_id=ws-xyz",
		jsonBody(t, map[string]any{})) // no reason
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractRejectSubmission_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{rejectSubmissionErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/goals/wf-200/reject?workspace_id=ws-xyz",
		jsonBody(t, map[string]any{"reason": "nope"}))
	req = withTestUser(req, "approver@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
