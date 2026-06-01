package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/spicedb"
)

// ContractService is the minimal interface consumed by ContractHandler.
type ContractService interface {
	Approve(ctx context.Context, actor, workspaceID, wfID string) (map[string]any, error)
	Abort(ctx context.Context, actor, workspaceID, wfID string) (map[string]any, error)
	SubmissionApprove(ctx context.Context, actor, workspaceID, wfID string) (map[string]any, error)
	RejectSubmission(ctx context.Context, actor, workspaceID, wfID, reason string) (map[string]any, error)
	RejectPlan(ctx context.Context, actor, workspaceID, wfID, reason string) (map[string]any, error)
}

// ContractHandler exposes contract workflow HTTP endpoints.
type ContractHandler struct {
	svc ContractService
}

// NewContractHandler wires the contract HTTP handler.
func NewContractHandler(svc ContractService) *ContractHandler {
	return &ContractHandler{svc: svc}
}

type rejectBody struct {
	Reason string `json:"reason"`
}

// Approve handles POST /api/v1/contracts/{wf_id}/approve.
// No body required — actor identity comes from the JWT claim.
func (h *ContractHandler) Approve(w http.ResponseWriter, r *http.Request) {
	actor, workspace, wfID, ok := h.extractCommon(w, r)
	if !ok {
		return
	}
	out, err := h.svc.Approve(r.Context(), actor, workspace, wfID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Abort handles POST /api/v1/contracts/{wf_id}/abort.
func (h *ContractHandler) Abort(w http.ResponseWriter, r *http.Request) {
	actor, workspace, wfID, ok := h.extractCommon(w, r)
	if !ok {
		return
	}
	out, err := h.svc.Abort(r.Context(), actor, workspace, wfID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// RejectPlan handles POST /api/v1/contracts/{wf_id}/reject.
func (h *ContractHandler) RejectPlan(w http.ResponseWriter, r *http.Request) {
	actor, workspace, wfID, ok := h.extractCommon(w, r)
	if !ok {
		return
	}
	var body rejectBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	out, err := h.svc.RejectPlan(r.Context(), actor, workspace, wfID, body.Reason)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SubmissionApprove handles POST /api/v1/goals/{wf_id}/submission-approve.
// No body required — actor identity comes from the JWT claim.
func (h *ContractHandler) SubmissionApprove(w http.ResponseWriter, r *http.Request) {
	actor, workspace, wfID, ok := h.extractCommon(w, r)
	if !ok {
		return
	}
	out, err := h.svc.SubmissionApprove(r.Context(), actor, workspace, wfID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// RejectSubmission handles POST /api/v1/goals/{wf_id}/reject.
func (h *ContractHandler) RejectSubmission(w http.ResponseWriter, r *http.Request) {
	actor, workspace, wfID, ok := h.extractCommon(w, r)
	if !ok {
		return
	}
	var body rejectBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	out, err := h.svc.RejectSubmission(r.Context(), actor, workspace, wfID, body.Reason)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// extractCommon pulls actor, workspace_id query param, and wf_id URL param from
// the request. Writes an appropriate error and returns ok=false on failure.
func (h *ContractHandler) extractCommon(w http.ResponseWriter, r *http.Request) (actor, workspace, wfID string, ok bool) {
	actor = auth.RequesterID(r)
	if actor == "" {
		writeError(w, http.StatusUnauthorized, "missing actor")
		return
	}
	workspace = r.URL.Query().Get("workspace_id")
	if workspace == "" {
		writeError(w, http.StatusBadRequest, "workspace_id query param required")
		return
	}
	wfID = chi.URLParam(r, "wf_id")
	if wfID == "" {
		writeError(w, http.StatusBadRequest, "wf_id required")
		return
	}
	ok = true
	return
}

func (h *ContractHandler) writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, spicedb.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
}
