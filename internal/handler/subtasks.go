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
	DecomposeQuestion(ctx context.Context, message string, mode cogitoclient.DecomposeMode, history []cogitoclient.ChatTurn) ([]cogitoclient.SubQuestion, []cogitoclient.TaskCandidate, error)
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

// chatTurnReq mirrors cogitoclient.ChatTurn for JSON decoding of the
// subtasks-from-chat request body's optional history.
type chatTurnReq struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// subtasksFromChatReq is the POST /tickets/{parent_id}/subtasks/from-chat
// request body. History is optional prior conversation context passed
// through to cogito's DecomposeQuestion RPC.
type subtasksFromChatReq struct {
	Message string        `json:"message"`
	History []chatTurnReq `json:"history,omitempty"`
}

// SubtasksFromChat decomposes a chat question into task candidates under
// parent_id via cogito's DecomposeQuestion RPC (mode=TASK_CANDIDATES), then
// bulk-creates the resulting candidates as real sub-tickets
// (goq-cogito-subtasks-from-chat, design doc §3.4/§5). Structurally this is
// SubtasksFromText's sibling: same authz/parent-lookup/error-handling shape,
// only the cogito call and request/response mapping differ.
//
// This endpoint always requests DecomposeModeTaskCandidates — its whole
// purpose is creating sub-tickets, so there is no "sub-questions only"
// variant exposed over REST.
func (h *TicketHandler) SubtasksFromChat(w http.ResponseWriter, r *http.Request) {
	parentID, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	var req subtasksFromChatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	actor := auth.RequesterID(r)

	// Existence + manage authz mirror SubtasksFromText above: fetch the
	// parent, and only SpiceDB-gate it when it belongs to a project (legacy
	// bypass).
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
	history := make([]cogitoclient.ChatTurn, 0, len(req.History))
	for _, turn := range req.History {
		history = append(history, cogitoclient.ChatTurn{Role: turn.Role, Content: turn.Content})
	}
	_, candidates, err := h.cogito.DecomposeQuestion(r.Context(), req.Message, cogitoclient.DecomposeModeTaskCandidates, history)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cogito DecomposeQuestion: "+err.Error())
		return
	}
	if len(candidates) == 0 {
		writeError(w, http.StatusBadRequest, "no task candidates produced")
		return
	}

	if len(candidates) > maxBulkCreateItems {
		candidates = candidates[:maxBulkCreateItems]
	}
	items := make([]ticket.BulkCreateItem, 0, len(candidates))
	for _, c := range candidates {
		items = append(items, ticket.BulkCreateItem{Title: c.Title, Description: c.Description})
	}

	resp, err := h.svc.BulkCreate(r.Context(), ticket.BulkCreateReq{
		ParentID:  parentID,
		Items:     items,
		Source:    ticket.BulkCreateSourceQuestionDecomposition,
		SourceRef: req.Message,
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
