package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/device"
)

type DeviceSvc interface {
	Register(ctx context.Context, userID string, req device.RegisterReq) (*device.Token, error)
	Unregister(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, userID string) ([]*device.Token, error)
}

type DeviceHandler struct{ svc DeviceSvc }

func NewDeviceHandler(svc DeviceSvc) *DeviceHandler { return &DeviceHandler{svc: svc} }

// POST /devices/register — upserts the caller's device token. Idempotent.
func (h *DeviceHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req device.RegisterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	userID := auth.RequesterID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "missing requester")
		return
	}
	tok, err := h.svc.Register(r.Context(), userID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, tok)
}

// GET /devices — returns the caller's registered tokens.
func (h *DeviceHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := auth.RequesterID(r)
	tokens, err := h.svc.List(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": tokens})
}

// DELETE /devices/:id — removes a token. Currently any authenticated caller
// can delete by id; ownership enforcement is deferred until the dispatcher
// lands (when leaking ids becomes a security concern).
func (h *DeviceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid device id")
		return
	}
	if err := h.svc.Unregister(r.Context(), id); err != nil {
		if errors.Is(err, device.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
