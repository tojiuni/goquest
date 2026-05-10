package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/comment"
)

type CommentSvc interface {
	Create(ctx context.Context, ticketID uuid.UUID, authorID string, kind comment.AuthorKind, req comment.CreateReq) (*comment.Comment, error)
	ListByTicket(ctx context.Context, ticketID uuid.UUID, includeInternal bool) ([]*comment.Comment, error)
	Update(ctx context.Context, id uuid.UUID, authorID string, req comment.UpdateReq) (*comment.Comment, error)
	Delete(ctx context.Context, id uuid.UUID, authorID string) error
}

// CanSeeInternal returns true if the request context is allowed to see is_internal=true comments.
// Phase H1: any authenticated request (staff JWT) can see internal comments.
// Phase H3+: customer-token contexts must return false.
type CanSeeInternal func(r *http.Request) bool

type CommentHandler struct {
	svc            CommentSvc
	canSeeInternal CanSeeInternal
}

func NewCommentHandler(svc CommentSvc, canSeeInternal CanSeeInternal) *CommentHandler {
	if canSeeInternal == nil {
		canSeeInternal = func(*http.Request) bool { return true }
	}
	return &CommentHandler{svc: svc, canSeeInternal: canSeeInternal}
}

// POST /tickets/:id/comments
func (h *CommentHandler) Create(w http.ResponseWriter, r *http.Request) {
	tid, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	var req comment.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	authorID := auth.RequesterID(r)
	if authorID == "" {
		writeError(w, http.StatusUnauthorized, "missing requester")
		return
	}
	c, err := h.svc.Create(r.Context(), tid, authorID, comment.AuthorKindStaff, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// GET /tickets/:id/comments
func (h *CommentHandler) List(w http.ResponseWriter, r *http.Request) {
	tid, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	includeInternal := h.canSeeInternal(r)
	list, err := h.svc.ListByTicket(r.Context(), tid, includeInternal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": list})
}

// PATCH /tickets/:id/comments/:cid
func (h *CommentHandler) Update(w http.ResponseWriter, r *http.Request) {
	cid, err := parseUUID(chi.URLParam(r, "cid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid comment id")
		return
	}
	var req comment.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	authorID := auth.RequesterID(r)
	c, err := h.svc.Update(r.Context(), cid, authorID, req)
	if err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			writeError(w, http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrForbidden):
			writeError(w, http.StatusForbidden, "only the author can edit this comment")
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// DELETE /tickets/:id/comments/:cid
func (h *CommentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	cid, err := parseUUID(chi.URLParam(r, "cid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid comment id")
		return
	}
	authorID := auth.RequesterID(r)
	if err := h.svc.Delete(r.Context(), cid, authorID); err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			writeError(w, http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrForbidden):
			writeError(w, http.StatusForbidden, "only the author can delete this comment")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
