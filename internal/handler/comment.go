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
	"github.com/tojiuni/goquest/internal/mention"
	"github.com/tojiuni/goquest/internal/ticket"
)

type CommentSvc interface {
	Create(ctx context.Context, ticketID uuid.UUID, authorID string, kind comment.AuthorKind, req comment.CreateReq) (*comment.Comment, error)
	ListByTicket(ctx context.Context, ticketID uuid.UUID, includeInternal bool) ([]*comment.Comment, error)
	Update(ctx context.Context, id uuid.UUID, authorID string, req comment.UpdateReq) (*comment.Comment, error)
	Delete(ctx context.Context, id uuid.UUID, authorID string) error
	Publish(ctx context.Context, id uuid.UUID, actorKind comment.AuthorKind) error
}

// CanSeeInternal returns true if the request context is allowed to see is_internal=true comments.
// Phase H1: any authenticated request (staff JWT) can see internal comments.
// Phase H3+: customer-token contexts must return false.
type CanSeeInternal func(r *http.Request) bool

// TicketGetter is the subset of ticket.Service the mention hook needs: just
// enough to resolve a comment's ticket to its project (mentions are
// validated against project membership).
type TicketGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*ticket.Ticket, error)
}

// MentionSvc is the subset of mention.Service the comment-create hook needs.
type MentionSvc interface {
	CreateFromBody(ctx context.Context, ticketID, projectID uuid.UUID, commentID *uuid.UUID, source mention.Source, authorID, body string) ([]*mention.Mention, error)
}

type CommentHandler struct {
	svc            CommentSvc
	canSeeInternal CanSeeInternal
	tickets        TicketGetter
	mentions       MentionSvc
}

func NewCommentHandler(svc CommentSvc, canSeeInternal CanSeeInternal) *CommentHandler {
	if canSeeInternal == nil {
		canSeeInternal = func(*http.Request) bool { return true }
	}
	return &CommentHandler{svc: svc, canSeeInternal: canSeeInternal}
}

// WithMentions wires @mention extraction into comment creation: after a
// comment is created, its body is scanned for @mentions of the ticket's
// project members and recorded via mentions.CreateFromBody. Optional — a
// handler without this wired simply skips mention extraction (used by tests
// that construct CommentHandler directly via NewCommentHandler).
func (h *CommentHandler) WithMentions(tickets TicketGetter, mentions MentionSvc) *CommentHandler {
	h.tickets = tickets
	h.mentions = mentions
	return h
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
	h.extractMentions(r.Context(), tid, c.ID, authorID, req.Body)
	writeJSON(w, http.StatusCreated, c)
}

// extractMentions is best-effort: a failure to resolve/record @mentions must
// never fail the comment creation itself, so errors are swallowed here.
func (h *CommentHandler) extractMentions(ctx context.Context, ticketID, commentID uuid.UUID, authorID, body string) {
	if h.tickets == nil || h.mentions == nil {
		return
	}
	t, err := h.tickets.Get(ctx, ticketID)
	if err != nil || t.ProjectID == nil {
		return
	}
	_, _ = h.mentions.CreateFromBody(ctx, ticketID, *t.ProjectID, &commentID, mention.SourceComment, authorID, body)
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

// POST /tickets/:id/comments/:cid/publish — staff converts an AI draft into a
// customer-visible reply. (Phase H5)
func (h *CommentHandler) Publish(w http.ResponseWriter, r *http.Request) {
	cid, err := parseUUID(chi.URLParam(r, "cid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid comment id")
		return
	}
	// Phase H5: only staff JWT can publish. Customers can't reach this route
	// because canSeeInternal-style gating is at handler level; staff is enforced
	// here by requiring JWT (no customer-token).
	if err := h.svc.Publish(r.Context(), cid, comment.AuthorKindStaff); err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			writeError(w, http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrForbidden):
			writeError(w, http.StatusForbidden, "only staff can publish drafts")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
