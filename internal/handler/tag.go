package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/tag"
)

type TagSvc interface {
	Create(ctx context.Context, workspaceID uuid.UUID, creatorID string, req tag.CreateReq) (*tag.Tag, error)
	Get(ctx context.Context, id uuid.UUID) (*tag.Tag, error)
	Update(ctx context.Context, id uuid.UUID, req tag.UpdateReq) (*tag.Tag, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*tag.Tag, error)
}

type TagHandler struct{ svc TagSvc }

func NewTagHandler(svc TagSvc) *TagHandler { return &TagHandler{svc: svc} }

func (h *TagHandler) Create(w http.ResponseWriter, r *http.Request) {
	wid, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req tag.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	creatorID := auth.RequesterID(r)
	t, err := h.svc.Create(r.Context(), wid, creatorID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, t)
}

func (h *TagHandler) List(w http.ResponseWriter, r *http.Request) {
	wid, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	tags, err := h.svc.ListByWorkspace(r.Context(), wid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// inWorkspace confirms tag `tid` belongs to the {wid} the caller was authorized
// for; the store addresses tags by id alone. Writes the 404/500 itself.
func (h *TagHandler) inWorkspace(w http.ResponseWriter, r *http.Request, tid uuid.UUID) bool {
	wid, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return false
	}
	t, err := h.svc.Get(r.Context(), tid)
	if err != nil {
		if errors.Is(err, tag.ErrNotFound) {
			writeError(w, http.StatusNotFound, "tag not found")
			return false
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if t.WorkspaceID != wid {
		writeError(w, http.StatusNotFound, "tag not found")
		return false
	}
	return true
}

func (h *TagHandler) Get(w http.ResponseWriter, r *http.Request) {
	tid, err := parseUUID(chi.URLParam(r, "tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if !h.inWorkspace(w, r, tid) {
		return
	}

	t, err := h.svc.Get(r.Context(), tid)
	if err != nil {
		if errors.Is(err, tag.ErrNotFound) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TagHandler) Update(w http.ResponseWriter, r *http.Request) {
	tid, err := parseUUID(chi.URLParam(r, "tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if !h.inWorkspace(w, r, tid) {
		return
	}

	var req tag.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	t, err := h.svc.Update(r.Context(), tid, req)
	if err != nil {
		if errors.Is(err, tag.ErrNotFound) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TagHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tid, err := parseUUID(chi.URLParam(r, "tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if !h.inWorkspace(w, r, tid) {
		return
	}

	if err := h.svc.Delete(r.Context(), tid); err != nil {
		if errors.Is(err, tag.ErrNotFound) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
