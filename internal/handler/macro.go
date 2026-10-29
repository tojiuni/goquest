package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/macro"
)

type MacroSvc interface {
	Create(ctx context.Context, workspaceID uuid.UUID, creatorID string, req macro.CreateReq) (*macro.Macro, error)
	Get(ctx context.Context, id uuid.UUID) (*macro.Macro, error)
	Update(ctx context.Context, id uuid.UUID, req macro.UpdateReq) (*macro.Macro, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, projectID *uuid.UUID) ([]*macro.Macro, error)
}

type MacroHandler struct{ svc MacroSvc }

func NewMacroHandler(svc MacroSvc) *MacroHandler { return &MacroHandler{svc: svc} }

// POST /workspaces/{wid}/macros
func (h *MacroHandler) Create(w http.ResponseWriter, r *http.Request) {
	wid, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	var req macro.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	creatorID := auth.RequesterID(r)
	m, err := h.svc.Create(r.Context(), wid, creatorID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// GET /workspaces/{wid}/macros?project_id=<uuid>
func (h *MacroHandler) List(w http.ResponseWriter, r *http.Request) {
	wid, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	var projectID *uuid.UUID
	if pid := r.URL.Query().Get("project_id"); pid != "" {
		if u, err := uuid.Parse(pid); err == nil {
			projectID = &u
		}
	}
	list, err := h.svc.ListByWorkspace(r.Context(), wid, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"macros": list})
}

// inWorkspace confirms macro `mid` belongs to the {wid} the caller was authorized
// for; the store addresses macros by id alone. Writes the 404/500 itself.
func (h *MacroHandler) inWorkspace(w http.ResponseWriter, r *http.Request, mid uuid.UUID) bool {
	wid, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return false
	}
	m, err := h.svc.Get(r.Context(), mid)
	if err != nil {
		if errors.Is(err, macro.ErrNotFound) {
			writeError(w, http.StatusNotFound, "macro not found")
			return false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if m.WorkspaceID != wid {
		writeError(w, http.StatusNotFound, "macro not found")
		return false
	}
	return true
}

// PATCH /workspaces/{wid}/macros/{mid}
func (h *MacroHandler) Update(w http.ResponseWriter, r *http.Request) {
	mid, err := parseUUID(chi.URLParam(r, "mid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid macro id")
		return
	}
	if !h.inWorkspace(w, r, mid) {
		return
	}
	var req macro.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	m, err := h.svc.Update(r.Context(), mid, req)
	if err != nil {
		if errors.Is(err, macro.ErrNotFound) {
			writeError(w, http.StatusNotFound, "macro not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// DELETE /workspaces/{wid}/macros/{mid}
func (h *MacroHandler) Delete(w http.ResponseWriter, r *http.Request) {
	mid, err := parseUUID(chi.URLParam(r, "mid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid macro id")
		return
	}
	if !h.inWorkspace(w, r, mid) {
		return
	}
	if err := h.svc.Delete(r.Context(), mid); err != nil {
		if errors.Is(err, macro.ErrNotFound) {
			writeError(w, http.StatusNotFound, "macro not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
