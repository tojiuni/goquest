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
	ListLeaderboard(ctx context.Context, workspaceID string, limit int) ([]*xp.UserXP, error)
	GetXPHistory(ctx context.Context, workspaceID, userID string, limit int) ([]*xp.XPEvent, error)
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

// Leaderboard returns workspace members ranked by XP descending.
// GET /workspaces/{wid}/xp/leaderboard
func (h *XPHandler) Leaderboard(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	entries, err := h.svc.ListLeaderboard(r.Context(), wid, 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": entries})
}

// GetXPHistory returns the caller's XP gains from completed tickets.
// GET /workspaces/{wid}/xp/history
func (h *XPHandler) GetXPHistory(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	userID := auth.RequesterID(r)
	events, err := h.svc.GetXPHistory(r.Context(), wid, userID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": events})
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
