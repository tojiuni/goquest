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
	"github.com/tojiuni/goquest/internal/contract"
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
	listErr              error
	listOut              map[string]any
	getErr               error
	getOut               map[string]any

	// recorded call arguments
	lastActor  string
	lastWS     string
	lastWFID   string
	lastReason string
	lastQueue  string
	lastLimit  int
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

func (f *fakeContractSvc) List(_ context.Context, actor, ws, queue string, limit int) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastQueue = queue
	f.lastLimit = limit
	return f.listOut, f.listErr
}

func (f *fakeContractSvc) Get(_ context.Context, actor, ws, wfID string) (map[string]any, error) {
	f.lastActor = actor
	f.lastWS = ws
	f.lastWFID = wfID
	return f.getOut, f.getErr
}

// withTestUser injects a requester ID into the request context using the
// existing auth.WithRequesterID helper (same pattern as authz_e2e_test.go).
func withTestUser(r *http.Request, userID string) *http.Request {
	return r.WithContext(auth.WithRequesterID(r.Context(), userID))
}

// contractRouter builds a minimal chi router with all contract endpoints.
func contractRouter(h *handler.ContractHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/contracts", h.List)
	r.Get("/api/v1/contracts/{wf_id}", h.Get)
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

// ── List tests ────────────────────────────────────────────────────────────────

func TestContractList_200_DefaultLimit(t *testing.T) {
	svc := &fakeContractSvc{
		listOut: map[string]any{
			"items": []any{
				map[string]any{"workflow_id": "wf-1", "state": "running"},
			},
			"queue": "running",
			"count": 1,
		},
	}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts?workspace_id=ws-abc&state=running",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastActor != "user@example.com" {
		t.Errorf("actor: got %q", svc.lastActor)
	}
	if svc.lastWS != "ws-abc" {
		t.Errorf("workspace: got %q", svc.lastWS)
	}
	if svc.lastQueue != "running" {
		t.Errorf("queue: got %q, want %q (raw passthrough)", svc.lastQueue, "running")
	}
	if svc.lastLimit != 50 {
		t.Errorf("limit: got %d, want default 50", svc.lastLimit)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["queue"] != "running" {
		t.Errorf("response queue: got %v", body["queue"])
	}
}

func TestContractList_200_ExplicitLimitClamp(t *testing.T) {
	svc := &fakeContractSvc{listOut: map[string]any{"items": []any{}}}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	// Above maxListLimit (200) — should clamp.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts?workspace_id=ws-abc&state=gate_pending&limit=9999",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.lastLimit != 200 {
		t.Errorf("limit clamp: got %d, want 200", svc.lastLimit)
	}
	// gate_pending is passed through to the service untranslated; the service
	// (not the handler) is responsible for queueMap. Verify we forwarded it.
	if svc.lastQueue != "gate_pending" {
		t.Errorf("queue passthrough: got %q", svc.lastQueue)
	}
}

func TestContractList_401_MissingActor(t *testing.T) {
	svc := &fakeContractSvc{}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts?workspace_id=ws-abc&state=running",
		http.NoBody)
	// no withTestUser
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestContractList_200_WorkspaceOptional(t *testing.T) {
	// iOS Contracts tab omits workspace_id; backend must still serve.
	svc := &fakeContractSvc{listOut: map[string]any{"items": []any{}}}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts?state=running", http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.lastWS != "" {
		t.Fatalf("expected empty workspace forwarded to service, got %q", svc.lastWS)
	}
}

func TestContractList_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{listErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts?workspace_id=ws-other&state=running",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractList_502_UpstreamError(t *testing.T) {
	svc := &fakeContractSvc{listErr: errors.New("contract-plane: connection refused")}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts?workspace_id=ws-abc&state=running",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

// ── Get tests ─────────────────────────────────────────────────────────────────

func TestContractGet_200(t *testing.T) {
	svc := &fakeContractSvc{
		getOut: map[string]any{
			"workflow_id": "wf-xyz",
			"state":       "running",
			"gate_state":  "awaiting_admin",
		},
	}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/wf-xyz?workspace_id=ws-abc",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastWFID != "wf-xyz" {
		t.Errorf("wfID: got %q", svc.lastWFID)
	}
	if svc.lastWS != "ws-abc" {
		t.Errorf("workspace: got %q", svc.lastWS)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["workflow_id"] != "wf-xyz" {
		t.Errorf("workflow_id passthrough: got %v", body["workflow_id"])
	}
	if body["gate_state"] != "awaiting_admin" {
		t.Errorf("gate_state passthrough: got %v", body["gate_state"])
	}
}

func TestContractGet_403_Forbidden(t *testing.T) {
	svc := &fakeContractSvc{getErr: spicedb.ErrForbidden}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/wf-xyz?workspace_id=ws-other",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractGet_404_NotFound(t *testing.T) {
	svc := &fakeContractSvc{getErr: contract.ErrNotFound}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/wf-missing?workspace_id=ws-abc",
		http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContractGet_200_WorkspaceOptional(t *testing.T) {
	// iOS detail screen omits workspace_id; backend must still serve.
	svc := &fakeContractSvc{getOut: map[string]any{"workflow_id": "wf-xyz"}}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/wf-xyz", http.NoBody)
	req = withTestUser(req, "user@example.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.lastWS != "" {
		t.Fatalf("expected empty workspace forwarded to service, got %q", svc.lastWS)
	}
}

func TestContractGet_401_MissingActor(t *testing.T) {
	svc := &fakeContractSvc{}
	h := handler.NewContractHandler(svc)
	router := contractRouter(h)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/contracts/wf-xyz?workspace_id=ws-abc",
		http.NoBody)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
