package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/mention"
)

// MentionQuerySvc is the subset of mention.Service the read endpoints need.
type MentionQuerySvc interface {
	ListViewsByActor(ctx context.Context, actorID string, limit, offset int) ([]*mention.View, error)
	CountUnseenByActor(ctx context.Context, actorID string) (int, error)
	MarkSeen(ctx context.Context, id uuid.UUID, actorID string) error
}

type MentionHandler struct {
	svc    MentionQuerySvc
	lookup directory.Lookuper
}

func NewMentionHandler(svc MentionQuerySvc, lookup directory.Lookuper) *MentionHandler {
	return &MentionHandler{svc: svc, lookup: lookup}
}

const (
	mentionListDefaultLimit = 20
	mentionListMaxLimit     = 100
)

// GET /users/mentions — the authenticated requester's mention inbox, newest
// first, with unseen count. Scoped to the caller's own identity only (the
// actor id comes from auth, never from a parameter).
func (h *MentionHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	actorID := auth.RequesterID(r)
	if actorID == "" {
		writeError(w, http.StatusUnauthorized, "missing requester")
		return
	}

	limit := mentionListDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, mentionListMaxLimit)
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	views, err := h.svc.ListViewsByActor(r.Context(), actorID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	unseen, err := h.svc.CountUnseenByActor(r.Context(), actorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.fillCreatedByNames(r.Context(), views)

	if views == nil {
		views = []*mention.View{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mentions":     views,
		"unseen_count": unseen,
		"limit":        limit,
		"offset":       offset,
	})
}

// POST /mentions/{id}/seen — mark one of the caller's mentions as seen.
func (h *MentionHandler) MarkSeen(w http.ResponseWriter, r *http.Request) {
	actorID := auth.RequesterID(r)
	if actorID == "" {
		writeError(w, http.StatusUnauthorized, "missing requester")
		return
	}
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mention id")
		return
	}
	if err := h.svc.MarkSeen(r.Context(), id, actorID); err != nil {
		if errors.Is(err, mention.ErrNotFound) {
			writeError(w, http.StatusNotFound, "mention not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fillCreatedByNames resolves mentioner ids to display names, best-effort:
// same degrade-to-raw-id policy as UsersHandler.Lookup — a lymphhub hiccup
// must not fail the inbox.
func (h *MentionHandler) fillCreatedByNames(ctx context.Context, views []*mention.View) {
	if h.lookup == nil || len(views) == 0 {
		return
	}
	seen := make(map[string]bool, len(views))
	var ids []string
	for _, v := range views {
		if !seen[v.CreatedBy] {
			seen[v.CreatedBy] = true
			ids = append(ids, v.CreatedBy)
		}
	}
	actors, err := h.lookup.Lookup(ctx, ids)
	if err != nil {
		return
	}
	names := make(map[string]string, len(actors))
	for _, a := range actors {
		names[a.ID] = a.DisplayName
	}
	for _, v := range views {
		v.CreatedByName = names[v.CreatedBy]
	}
}
