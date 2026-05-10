package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/template"
	"github.com/tojiuni/goquest/internal/ticket"
)

type TemplateSvc interface {
	Create(ctx context.Context, projectID uuid.UUID, req template.CreateReq, createdBy string) (*template.Template, error)
	List(ctx context.Context, projectID uuid.UUID) ([]*template.Template, error)
	Get(ctx context.Context, id uuid.UUID) (*template.Template, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ToTicketReq(t *template.Template, overrides ticket.CreateTicketReq) ticket.CreateTicketReq
}

type TicketInstantiator interface {
	Create(ctx context.Context, req ticket.CreateTicketReq, requesterID string) (*ticket.Ticket, error)
}

type TemplateHandler struct {
	svc    TemplateSvc
	tickets TicketInstantiator
}

func NewTemplateHandler(svc TemplateSvc, tickets TicketInstantiator) *TemplateHandler {
	return &TemplateHandler{svc: svc, tickets: tickets}
}

// POST /workspaces/{wid}/projects/{pid}/templates
func (h *TemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	var req template.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tmpl, err := h.svc.Create(r.Context(), pid, req, auth.RequesterID(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, tmpl)
}

// GET /workspaces/{wid}/projects/{pid}/templates
func (h *TemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	templates, err := h.svc.List(r.Context(), pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

// GET /workspaces/{wid}/projects/{pid}/templates/{tid}
func (h *TemplateHandler) Get(w http.ResponseWriter, r *http.Request) {
	tid, err := uuid.Parse(chi.URLParam(r, "tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template id")
		return
	}

	tmpl, err := h.svc.Get(r.Context(), tid)
	if err != nil {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}

	writeJSON(w, http.StatusOK, tmpl)
}

// DELETE /workspaces/{wid}/projects/{pid}/templates/{tid}
func (h *TemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
	tid, err := uuid.Parse(chi.URLParam(r, "tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template id")
		return
	}

	if err := h.svc.Delete(r.Context(), tid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// POST /workspaces/{wid}/projects/{pid}/templates/{tid}/instantiate
// Creates a ticket from a template with optional field overrides.
func (h *TemplateHandler) Instantiate(w http.ResponseWriter, r *http.Request) {
	tid, err := uuid.Parse(chi.URLParam(r, "tid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template id")
		return
	}

	tmpl, err := h.svc.Get(r.Context(), tid)
	if err != nil {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}

	var overrides ticket.CreateTicketReq
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&overrides)
	}

	ticketReq := h.svc.ToTicketReq(tmpl, overrides)
	t, err := h.tickets.Create(r.Context(), ticketReq, auth.RequesterID(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, t)
}
