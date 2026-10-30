package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/approval"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/ticket"
)

// ApprovalSvc is the subset of approval.Service the ticket-scoped routes need.
type ApprovalSvc interface {
	Request(ctx context.Context, ticketID uuid.UUID, in approval.RequestInput, principal, label string) (*approval.Approval, error)
	ListForTicket(ctx context.Context, ticketID uuid.UUID, status approval.Status, limit, offset int) ([]*approval.Approval, error)
	GetForTicket(ctx context.Context, ticketID, id uuid.UUID) (*approval.Approval, error)
	Withdraw(ctx context.Context, ticketID, id uuid.UUID, principal, note string) (*approval.Approval, error)
	Decide(ctx context.Context, in approval.DecideInput, actor approval.Actor) (*approval.Approval, error)
}

// WorkspaceOfTicket resolves the workspace a ticket belongs to (via its
// project); uuid.Nil with no error for a ticket without a project.
type WorkspaceOfTicket func(ctx context.Context, t *ticket.Ticket) (uuid.UUID, error)

type ApprovalHandler struct {
	svc         ApprovalSvc
	workspaceOf WorkspaceOfTicket
}

func NewApprovalHandler(svc ApprovalSvc, workspaceOf WorkspaceOfTicket) *ApprovalHandler {
	return &ApprovalHandler{svc: svc, workspaceOf: workspaceOf}
}

// Mounted under /tickets/{id}, behind RequireTicketView. Single-approval routes
// are nested under the ticket (not id-only) so an approval id from another
// ticket is a 404, never a cross-ticket read.

func (h *ApprovalHandler) denyCustomer(w http.ResponseWriter, r *http.Request) bool {
	// A magic-link customer token passes RequireTicketView for its own ticket;
	// approvals are internal and never customer-visible.
	if auth.CustomerFrom(r) != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return true
	}
	return false
}

type requestApprovalBody struct {
	Kind            approval.Kind           `json:"kind"`
	Subject         approval.Subject        `json:"subject"`
	RequestNote     string                  `json:"request_note"`
	Evidence        []approval.EvidenceItem `json:"evidence"`
	WithinDirection *bool                   `json:"within_direction"`
}

func (h *ApprovalHandler) Request(w http.ResponseWriter, r *http.Request) {
	if h.denyCustomer(w, r) {
		return
	}
	tid, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	var body requestApprovalBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	a, err := h.svc.Request(r.Context(), tid, approval.RequestInput{
		Kind: body.Kind, Subject: body.Subject, RequestNote: body.RequestNote,
		Evidence: body.Evidence, WithinDirection: body.WithinDirection,
	}, auth.RequesterID(r), r.Header.Get("X-Session-Label"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *ApprovalHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.denyCustomer(w, r) {
		return
	}
	tid, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	out, err := h.svc.ListForTicket(r.Context(), tid, approval.Status(r.URL.Query().Get("status")), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if out == nil {
		out = []*approval.Approval{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": out})
}

func (h *ApprovalHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.denyCustomer(w, r) {
		return
	}
	tid, aid, ok := h.ids(w, r)
	if !ok {
		return
	}
	a, err := h.svc.GetForTicket(r.Context(), tid, aid)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *ApprovalHandler) Withdraw(w http.ResponseWriter, r *http.Request) {
	if h.denyCustomer(w, r) {
		return
	}
	tid, aid, ok := h.ids(w, r)
	if !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body) // body is optional
	a, err := h.svc.Withdraw(r.Context(), tid, aid, auth.RequesterID(r), body.Note)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// Decide records a decision. The decider and the basis are derived from the
// authenticated principal and the stored delegation rules; any decided_by,
// basis or status in the body is ignored. Mount it behind RequireTicketManage.
func (h *ApprovalHandler) Decide(w http.ResponseWriter, r *http.Request) {
	if h.denyCustomer(w, r) {
		return
	}
	tid, aid, ok := h.ids(w, r)
	if !ok {
		return
	}
	var body struct {
		Decision approval.Status `json:"decision"`
		Note     string          `json:"note"`
		UserRef  string          `json:"user_ref"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	ws := uuid.Nil
	if t := auth.TicketFrom(r); t != nil && t.ProjectID != nil && h.workspaceOf != nil {
		var err error
		if ws, err = h.workspaceOf(r.Context(), t); err != nil {
			writeError(w, http.StatusInternalServerError, "resolve workspace")
			return
		}
	}
	a, err := h.svc.Decide(r.Context(), approval.DecideInput{
		TicketID: tid, WorkspaceID: ws, ApprovalID: aid,
		Decision: body.Decision, Note: body.Note, UserRef: body.UserRef,
	}, approval.Actor{ID: auth.RequesterID(r), Kind: actorKind(auth.PrincipalKindFrom(r))})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func actorKind(k auth.PrincipalKind) approval.ActorKind {
	switch k {
	case auth.PrincipalHuman:
		return approval.ActorHuman
	case auth.PrincipalSession:
		return approval.ActorSession
	}
	return approval.ActorOther
}

func (h *ApprovalHandler) ids(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	tid, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return uuid.Nil, uuid.Nil, false
	}
	aid, err := parseUUID(chi.URLParam(r, "aid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid approval id")
		return uuid.Nil, uuid.Nil, false
	}
	return tid, aid, true
}

func (h *ApprovalHandler) writeErr(w http.ResponseWriter, err error) {
	var denied *approval.DeniedError
	if errors.As(err, &denied) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error(), "reason": string(denied.Reason)})
		return
	}
	switch {
	case errors.Is(err, approval.ErrNotFound):
		writeError(w, http.StatusNotFound, "approval not found")
	case errors.Is(err, approval.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, approval.ErrInvalidSubject), errors.Is(err, approval.ErrInvalidInput):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, approval.ErrDuplicatePending), errors.Is(err, approval.ErrInvalidTransition), errors.Is(err, approval.ErrStale):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
