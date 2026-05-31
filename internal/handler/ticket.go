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

	// Visibility: keep only subtasks the caller can view. Two LookupResources calls
	// (project + restricted), then membership-test each child by its own visibility.
	// shadow (NoopChecker) → unrestricted → keep all (no-op).
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
	if !unrP || !unrR {
		projSet := uuidSet(parseTicketUUIDs(projIDs))
		restrSet := uuidSet(parseTicketUUIDs(restrIDs))
		visible := subtasks[:0]
		for _, st := range subtasks {
			if visibleSubtask(st.ID, st.Visibility, projSet, restrSet) {
				visible = append(visible, st)
			}
		}
		subtasks = visible
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

// --- Refs (ticket-scoped) ---

// ListRefs handles GET /api/v1/tickets/{id}/refs. RequireTicketView has already
// authorized the caller and stashed the ticket; we only need the id.
func (h *TicketHandler) ListRefs(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}
	refs, err := h.svc.ListRefs(r.Context(), tk.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list refs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
}

// AddRefs handles POST /api/v1/tickets/{id}/refs. Body: {"ref_ids": [...]}.
// Validation/limit errors are surfaced as 4xx with a stable error message.
func (h *TicketHandler) AddRefs(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}
	var body struct {
		RefIDs []string `json:"ref_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if len(body.RefIDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"refs": []ticket.TicketRef{}})
		return
	}
	actor := auth.RequesterID(r)
	refs, err := h.svc.AddRefs(r.Context(), tk.ID, body.RefIDs, actor)
	if err != nil {
		switch {
		case errors.Is(err, ticket.ErrRefIDInvalid):
			writeError(w, http.StatusBadRequest, "invalid ref id (empty or >512 chars)")
		case errors.Is(err, ticket.ErrRefIDsDuplicate):
			writeError(w, http.StatusBadRequest, "duplicate ref id in request")
		case errors.Is(err, ticket.ErrRefLimitExceeded):
			writeError(w, http.StatusUnprocessableEntity, "max 50 refs per ticket")
		case errors.Is(err, ticket.ErrRefConflict):
			writeError(w, http.StatusConflict, "ref already exists for this ticket")
		default:
			writeError(w, http.StatusInternalServerError, "add refs: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
}

// RemoveRef handles DELETE /api/v1/tickets/{id}/refs/{l1_id}. The store-layer
// RemoveRef is idempotent — removing a never-attached id is also 204.
func (h *TicketHandler) RemoveRef(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}
	l1 := chi.URLParam(r, "l1_id")
	if l1 == "" {
		writeError(w, http.StatusBadRequest, "missing l1_id")
		return
	}
	if err := h.svc.RemoveRef(r.Context(), tk.ID, l1); err != nil {
		writeError(w, http.StatusInternalServerError, "remove ref: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReorderRefs handles PATCH /api/v1/tickets/{id}/refs/reorder.
// Body: {"ordered_l1_ids": [...]}. Returns the new ordered slice on success.
func (h *TicketHandler) ReorderRefs(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}
	var body struct {
		OrderedL1IDs []string `json:"ordered_l1_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := h.svc.ReorderRefs(r.Context(), tk.ID, body.OrderedL1IDs); err != nil {
		switch {
		case errors.Is(err, ticket.ErrRefIDInvalid):
			writeError(w, http.StatusBadRequest, "invalid ref id")
		case errors.Is(err, ticket.ErrRefIDsDuplicate):
			writeError(w, http.StatusBadRequest, "duplicate ref id")
		default:
			writeError(w, http.StatusBadRequest, "reorder: "+err.Error())
		}
		return
	}
	refs, err := h.svc.ListRefs(r.Context(), tk.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list refs after reorder: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
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

// visibleSubtask reports whether a subtask is visible given the caller's two
// LookupResources sets, honoring the subtask's own visibility mode. Any non-restricted
// visibility is treated as project-scoped; only two Visibility values exist today, and
// if a third is added this and the SQL predicate in store_pg.go must change together.
func visibleSubtask(id uuid.UUID, vis ticket.Visibility, projSet, restrSet map[uuid.UUID]bool) bool {
	if vis == ticket.VisibilityRestricted {
		return restrSet[id]
	}
	return projSet[id]
}

func uuidSet(ids []uuid.UUID) map[uuid.UUID]bool {
	m := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
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
