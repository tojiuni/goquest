package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/apikey"
	"github.com/tojiuni/goquest/internal/auth"
)

type APIKeySvc interface {
	Create(ctx context.Context, projectID uuid.UUID, req apikey.CreateReq, createdBy string) (*apikey.CreateResult, error)
	List(ctx context.Context, projectID uuid.UUID) ([]*apikey.APIKey, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type APIKeyHandler struct{ svc APIKeySvc }

func NewAPIKeyHandler(svc APIKeySvc) *APIKeyHandler { return &APIKeyHandler{svc: svc} }

// POST /workspaces/{wid}/projects/{pid}/api-keys
func (h *APIKeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	var req apikey.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	res, err := h.svc.Create(r.Context(), pid, req, auth.RequesterID(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, res)
}

// GET /workspaces/{wid}/projects/{pid}/api-keys
func (h *APIKeyHandler) List(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	keys, err := h.svc.List(r.Context(), pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"api_keys": keys})
}

// DELETE /workspaces/{wid}/projects/{pid}/api-keys/{kid}
func (h *APIKeyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	kid, err := uuid.Parse(chi.URLParam(r, "kid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}

	if err := h.svc.Delete(r.Context(), kid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
