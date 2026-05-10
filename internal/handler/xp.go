package handler

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/xp"
)

type XPSvc interface {
	GetUserXP(ctx context.Context, workspaceID, userID string) (*xp.UserXP, error)
	ListLevels(ctx context.Context) ([]*xp.Level, error)
}

type XPHandler struct{ svc XPSvc }

func NewXPHandler(svc XPSvc) *XPHandler { return &XPHandler{svc: svc} }

// GetMyXP returns the caller's XP/level in a workspace.
// GET /workspaces/{wid}/xp/me
func (h *XPHandler) GetMyXP(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	userID := auth.RequesterID(r)

	u, err := h.svc.GetUserXP(r.Context(), wid, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "xp record not found")
		return
	}

	writeJSON(w, http.StatusOK, u)
}

// ListLevels returns all level thresholds.
// GET /xp/levels
func (h *XPHandler) ListLevels(w http.ResponseWriter, r *http.Request) {
	levels, err := h.svc.ListLevels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"levels": levels})
}
