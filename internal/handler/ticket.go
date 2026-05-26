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

	// Visibility: restrict to SpiceDB-visible tickets. Two LookupResources calls —
	// project-visibility tickets need can_view_project, restricted need
	// can_view_restricted; the store combines them visibility-aware for an exact total.
	// In shadow (NoopChecker) both report unrestricted → no filter (identical to before).
	uid := auth.RequesterID(r)
	projIDs, unrP, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_project", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	restrIDs, unrR, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_restricted", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	// Invariant: a live Checker returns unrestricted=false for both calls; NoopChecker
	// returns true for both. So unrP and unrR always agree — there is no mixed state in
	// which we'd build a filter from one real set and one empty set. Any new
	// PermissionChecker must preserve this (return unrestricted consistently).
	if !unrP || !unrR {
		f.VisibleFilter = &ticket.VisibleTicketIDs{
			Project:    parseTicketUUIDs(projIDs),
			Restricted: parseTicketUUIDs(restrIDs),
		}
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
	// Access (and ticket fetch) is performed by RequireTicketView, which stashes the
	// ticket in context. If it's absent the middleware was not wired — fail loud rather
	// than open-fetching unauthorized, since this route is always gated.
	t := auth.TicketFrom(r)
	if t == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
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

	// Visibility: drop subtasks the caller cannot view (shadow NoopChecker → no-op).
	uid := auth.RequesterID(r)
	visible := subtasks[:0]
	for _, st := range subtasks {
		ok, cErr := h.checker.CanViewTicket(r.Context(), st.ID, uid, spicedb.VisibilityMode(st.Visibility))
		if cErr != nil {
			writeError(w, http.StatusInternalServerError, "visibility check: "+cErr.Error())
			return
		}
		if ok {
			visible = append(visible, st)
		}
	}
	subtasks = visible

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
	// Parent-ticket access already gated by RequireTicketView.
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

// parseTicketUUIDs parses LookupResources ids into uuids, skipping any that don't
// parse. Always returns a non-nil slice so it is safe as a SQL ANY($) argument.
func parseTicketUUIDs(ids []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		if u, err := uuid.Parse(s); err == nil {
			out = append(out, u)
		}
	}
	return out
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
