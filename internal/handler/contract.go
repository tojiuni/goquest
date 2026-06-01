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
	Approve(ctx context.Context, actor, workspaceID, wfID string, decision bool) (map[string]any, error)
	Abort(ctx context.Context, actor, workspaceID, wfID string) (map[string]any, error)
}

// ContractHandler exposes contract approve/abort HTTP endpoints.
type ContractHandler struct {
	svc ContractService
}

// NewContractHandler wires the contract HTTP handler.
func NewContractHandler(svc ContractService) *ContractHandler {
	return &ContractHandler{svc: svc}
}

type approveBody struct {
	Decision bool `json:"decision"`
}

// Approve handles POST /api/v1/contracts/{wf_id}/approve.
func (h *ContractHandler) Approve(w http.ResponseWriter, r *http.Request) {
	actor := auth.RequesterID(r)
	if actor == "" {
		writeError(w, http.StatusUnauthorized, "missing actor")
		return
	}
	workspace := r.URL.Query().Get("workspace_id")
	if workspace == "" {
		writeError(w, http.StatusBadRequest, "workspace_id query param required")
		return
	}
	wfID := chi.URLParam(r, "wf_id")
	if wfID == "" {
		writeError(w, http.StatusBadRequest, "wf_id required")
		return
	}
	var body approveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	out, err := h.svc.Approve(r.Context(), actor, workspace, wfID, body.Decision)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Abort handles POST /api/v1/contracts/{wf_id}/abort.
func (h *ContractHandler) Abort(w http.ResponseWriter, r *http.Request) {
	actor := auth.RequesterID(r)
	if actor == "" {
		writeError(w, http.StatusUnauthorized, "missing actor")
		return
	}
	workspace := r.URL.Query().Get("workspace_id")
	if workspace == "" {
		writeError(w, http.StatusBadRequest, "workspace_id query param required")
		return
	}
	wfID := chi.URLParam(r, "wf_id")
	if wfID == "" {
		writeError(w, http.StatusBadRequest, "wf_id required")
		return
	}
	out, err := h.svc.Abort(r.Context(), actor, workspace, wfID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *ContractHandler) writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, spicedb.ErrForbidden) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
}
