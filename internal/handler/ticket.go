package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/resolve"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// ProjectResolver resolves a name-based project reference to a project UUID.
type ProjectResolver interface {
	ResolveProjectID(ctx context.Context, ref resolve.Ref) (*uuid.UUID, error)
}

type TicketHandler struct {
	svc      *ticket.Service
	checker  spicedb.PermissionChecker
	writer   spicedb.RelationshipWriter
	resolver ProjectResolver
}

func NewTicketHandler(svc *ticket.Service, checker spicedb.PermissionChecker, writer spicedb.RelationshipWriter, resolver ProjectResolver) *TicketHandler {
	return &TicketHandler{svc: svc, checker: checker, writer: writer, resolver: resolver}
}

func (h *TicketHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req ticket.CreateTicketReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.ProjectID == nil {
		ref := resolve.Ref{
			WorkspaceSlug: req.WorkspaceSlug,
			WorkspaceName: req.WorkspaceName,
			ProjectName:   req.ProjectName,
		}
		if !ref.Empty() {
			pid, rerr := h.resolver.ResolveProjectID(r.Context(), ref)
			if rerr != nil {
				switch {
				case errors.Is(rerr, resolve.ErrIncompleteRef),
					errors.Is(rerr, resolve.ErrWorkspaceNotFound),
					errors.Is(rerr, resolve.ErrProjectNotFound):
					writeError(w, http.StatusBadRequest, rerr.Error())
				default:
					writeError(w, http.StatusInternalServerError, "resolve project: "+rerr.Error())
				}
				return
			}
			req.ProjectID = pid
		}
	}

	requesterID := auth.RequesterID(r)
	t, err := h.svc.Create(r.Context(), req, requesterID)
	if err != nil {
		if errors.Is(err, ticket.ErrDuplicateTitleName) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Write SpiceDB relationships asynchronously (best-effort).
	// Use a fresh bounded context — r.Context() is cancelled when the handler
	// returns, which would race-cancel the detached write and silently drop the
	// ticket→project/requester/assignee tuples.
	if t.ProjectID != nil {
		tid, pid, reqID, asgID := t.ID, *t.ProjectID, t.RequesterID, t.AssigneeID
		go func() {
			bgctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if wErr := h.writer.WriteTicket(bgctx, tid, pid, reqID, asgID); wErr != nil {
				log.Printf("spicedb WriteTicket %s: %v (continuing)", tid, wErr)
			}
		}()
	}

	writeJSON(w, http.StatusCreated, t)
}

func (h *TicketHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := ticket.ListTicketsFilter{
		Limit:  parseIntQuery(q.Get("limit"), 50),
		Offset: parseIntQuery(q.Get("offset"), 0),
	}

	if s := q.Get("status"); s != "" {
		st := ticket.Status(s)
		f.Status = &st
	}
	if t := q.Get("type"); t != "" {
		tt := ticket.TicketType(t)
		f.Type = &tt
	}
	if p := q.Get("priority"); p != "" {
		pr := ticket.Priority(p)
		f.Priority = &pr
	}
	if d := q.Get("domain"); d != "" {
		dom := ticket.Domain(d)
		f.Domain = &dom
	}
	if at := q.Get("assignee_type"); at != "" {
		at2 := ticket.AssigneeType(at)
		f.AssigneeType = &at2
	}
	if a := q.Get("assignee_id"); a != "" {
		f.AssigneeID = &a
	}
	if req := q.Get("requester_id"); req != "" {
		f.RequesterID = &req
	}
	if pid := q.Get("project_id"); pid != "" {
		if u, err := uuid.Parse(pid); err == nil {
			f.ProjectID = &u
		}
	}
	if wid := q.Get("workspace_id"); wid != "" {
		if u, err := uuid.Parse(wid); err == nil {
			f.WorkspaceID = &u
		}
	}
	if q.Get("top_level_only") == "true" {
		f.TopLevelOnly = true
	}

	tickets, total, err := h.svc.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tickets": tickets,
		"total":   total,
		"limit":   f.Limit,
		"offset":  f.Offset,
	})
}

func (h *TicketHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// SpiceDB visibility check (skip if no project, e.g. legacy tickets).
	if t.ProjectID != nil {
		userID := auth.RequesterID(r)
		ok, checkErr := h.checker.CanViewTicket(r.Context(), t.ID, userID, spicedb.VisibilityMode(t.Visibility))
		if checkErr != nil {
			// Log but don't block on SpiceDB errors — fail open in degraded mode.
			_ = checkErr
		} else if !ok {
			writeError(w, http.StatusForbidden, "access denied")
			return
		}
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	var req ticket.UpdateTicketReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	t, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		if errors.Is(err, ticket.ErrInvalidTransition) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if errors.Is(err, ticket.ErrDuplicateTitleName) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Keep SpiceDB's assignee relation in sync after an update (best-effort).
	// Detached bounded context — r.Context() is cancelled when the handler returns.
	tid, asg := t.ID, t.AssigneeID
	go func() {
		bgctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if wErr := h.writer.ReplaceTicketAssignee(bgctx, tid, asg); wErr != nil {
			log.Printf("spicedb ReplaceTicketAssignee %s: %v (continuing)", tid, wErr)
		}
	}()

	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	t, err := h.svc.Cancel(r.Context(), id)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		if errors.Is(err, ticket.ErrInvalidTransition) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) Approve(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	var req ticket.ApproveTicketReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.OperatorID == "" {
		req.OperatorID = auth.RequesterID(r)
	}
	if req.OperatorID == "" {
		writeError(w, http.StatusUnauthorized, "operator identity required")
		return
	}

	if req.Decision == "" {
		writeError(w, http.StatusBadRequest, "decision is required")
		return
	}

	t, err := h.svc.Approve(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		if errors.Is(err, ticket.ErrInvalidTransition) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) ListSubtasks(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	subtasks, err := h.svc.ListSubtasks(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"subtasks": subtasks})
}

func (h *TicketHandler) SubmitAttempt(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	var req ticket.SubmitAttemptReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	userID := auth.RequesterID(r)
	attempt, err := h.svc.SubmitAttempt(r.Context(), id, userID, req)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, attempt)
}

func (h *TicketHandler) ListAttempts(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	attempts, err := h.svc.ListAttempts(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"attempts": attempts})
}

func parseUUID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

func parseIntQuery(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
