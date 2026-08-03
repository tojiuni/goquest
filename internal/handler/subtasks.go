package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/cogitoclient"
	"github.com/tojiuni/goquest/internal/ticket"
)

// CogitoSplitter is the subset of *cogitoclient.Client this handler needs,
// declared here (rather than importing the concrete type directly into the
// TicketHandler struct) so unit tests can inject a fake instead of dialing a
// real cogito gRPC endpoint. *cogitoclient.Client satisfies this implicitly.
type CogitoSplitter interface {
	SplitContent(ctx context.Context, text, title string) ([]cogitoclient.Section, []string, error)
}

// subtasksFromTextReq is the POST /tickets/{parent_id}/subtasks/from-text
// request body. Title is optional prompt context passed through to cogito.
type subtasksFromTextReq struct {
	Text  string `json:"text"`
	Title string `json:"title,omitempty"`
}

// SubtasksFromText splits free-form text into child tickets under parent_id
// via cogito's SplitContent RPC, then bulk-creates the resulting sections as
// real sub-tickets (goq-cogito-subtasks-from-text, design doc §3.3/§5). This
// is thin wiring on top of two already-landed pieces — cogitoclient.Client
// and ticket.Service.BulkCreate — not a new business-logic layer.
//
// v1 only takes each top-level Section's Title/Contents; SubSections are not
// recursed into (out of scope per the design doc — a later addition would
// walk the tree into nested sub-tickets).
func (h *TicketHandler) SubtasksFromText(w http.ResponseWriter, r *http.Request) {
	parentID, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	var req subtasksFromTextReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	actor := auth.RequesterID(r)

	// Existence + manage authz mirror BulkCreate above: fetch the parent, and
	// only SpiceDB-gate it when it belongs to a project (legacy bypass).
	parent, err := h.svc.Get(r.Context(), parentID)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "parent ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup parent ticket: "+err.Error())
		return
	}
	if parent.ProjectID != nil {
		ok, err := h.checker.CanManageTicket(r.Context(), parentID, actor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "permission check: "+err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusForbidden, "access denied")
			return
		}
	}

	if h.cogito == nil {
		writeError(w, http.StatusBadGateway, "cogito unavailable")
		return
	}
	sections, _, err := h.cogito.SplitContent(r.Context(), req.Text, req.Title)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cogito SplitContent: "+err.Error())
		return
	}
	if len(sections) == 0 {
		writeError(w, http.StatusBadRequest, "no sections produced")
		return
	}

	if len(sections) > maxBulkCreateItems {
		sections = sections[:maxBulkCreateItems]
	}
	items := make([]ticket.BulkCreateItem, 0, len(sections))
	for _, s := range sections {
		items = append(items, ticket.BulkCreateItem{Title: s.Title, Description: s.Contents})
	}

	resp, err := h.svc.BulkCreate(r.Context(), ticket.BulkCreateReq{
		ParentID: parentID,
		Items:    items,
		Source:   ticket.BulkCreateSourceContentSplit,
	}, actor)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "parent ticket not found")
			return
		}
		if errors.Is(err, ticket.ErrValidation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
