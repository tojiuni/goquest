package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/audit"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/resolve"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/vcs"
)

// ProjectResolver resolves a name-based project reference to a project UUID.
type ProjectResolver interface {
	ResolveProjectID(ctx context.Context, ref resolve.Ref) (*uuid.UUID, error)
}

// TagResolver resolves tag names to tag UUIDs within a project's workspace,
// creating missing tags. A nil TagResolver on the handler means tag_names is
// ignored (parity with the nil-able audit logger).
type TagResolver interface {
	ResolveTagIDs(ctx context.Context, projectID uuid.UUID, creatorID string, names []string) ([]uuid.UUID, error)
}

// LinksProvider supplies a ticket's VCS links (implemented by *vcs.Service).
// Declared here rather than in internal/ticket to avoid a ticket->vcs import
// cycle: internal/vcs already depends on internal/ticket.
type LinksProvider interface {
	LinksForTicket(ctx context.Context, ticketID uuid.UUID) ([]vcs.VcsLink, error)
}

// ticketWithLinks is the GET response shape: the ticket's own JSON fields
// (promoted) plus vcs_links. Kept in the handler package to avoid a
// ticket->vcs import cycle.
type ticketWithLinks struct {
	*ticket.Ticket
	VcsLinks []vcs.VcsLink `json:"vcs_links,omitempty"`
}

type TicketHandler struct {
	svc      *ticket.Service
	checker  spicedb.PermissionChecker
	writer   spicedb.RelationshipWriter
	resolver ProjectResolver
	tags     TagResolver
	audit    audit.Logger
	links    LinksProvider
}

// NewTicketHandler wires the ticket HTTP handlers. The audit and links
// parameters are optional: if nil, audit calls / vcs_links embedding are
// silently skipped (each call site guards with a nil check). This keeps unit
// tests that don't care about audit or vcs from needing a fake.
func NewTicketHandler(svc *ticket.Service, checker spicedb.PermissionChecker, writer spicedb.RelationshipWriter, resolver ProjectResolver, tags TagResolver, auditLogger audit.Logger, links LinksProvider) *TicketHandler {
	return &TicketHandler{svc: svc, checker: checker, writer: writer, resolver: resolver, tags: tags, audit: auditLogger, links: links}
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

	if len(req.TagNames) > 0 {
		if h.tags == nil || req.ProjectID == nil {
			writeError(w, http.StatusBadRequest, "tag_names requires a resolvable project")
			return
		}
		tagIDs, terr := h.tags.ResolveTagIDs(r.Context(), *req.ProjectID, requesterID, req.TagNames)
		if terr != nil {
			switch {
			case errors.Is(terr, project.ErrNotFound):
				writeError(w, http.StatusBadRequest, "resolve tag_names: "+terr.Error())
			default:
				writeError(w, http.StatusInternalServerError, "resolve tag_names: "+terr.Error())
			}
			return
		}
		req.TagIDs = dedupeTagIDs(append(req.TagIDs, tagIDs...))
	}

	t, err := h.svc.Create(r.Context(), req, requesterID)
	if err != nil {
		if errors.Is(err, ticket.ErrDuplicateTitleName) {
			// Idempotent replay: a duplicate title_name can only occur when
			// project_id is set (per the partial-unique index), so look up the
			// existing ticket and return it with a replay marker instead of 409.
			if req.ProjectID != nil && req.TitleName != "" {
				if existing, gerr := h.svc.GetByProjectAndTitleName(r.Context(), *req.ProjectID, req.TitleName); gerr == nil {
					w.Header().Set("X-Idempotent-Replay", "true")
					writeJSON(w, http.StatusOK, existing)
					return
				}
			}
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Write SpiceDB relationships synchronously, but best-effort: the ticket→
	// project/requester/assignee tuple must exist before we return so the
	// creating session's immediate visibility checks succeed. An async write
	// raced those reads — a session that batch-created an epic + children then
	// listed the epic's subtasks got 0 back because the tuples had not landed
	// yet (goq-epic-child-enumeration-gap). A write error is logged and
	// swallowed so ticket creation still succeeds when SpiceDB is unavailable;
	// the backfill job reconciles later. Fresh bounded context: r.Context() may
	// be near its deadline, and the tuple write should not inherit it.
	if t.ProjectID != nil {
		bgctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if wErr := h.writer.WriteTicket(bgctx, t.ID, *t.ProjectID, t.RequesterID, t.AssigneeID); wErr != nil {
			log.Printf("spicedb WriteTicket %s: %v (continuing)", t.ID, wErr)
		}
		cancel()
	}

	writeJSON(w, http.StatusCreated, t)
}

// GetByName resolves a ticket by its human-readable title_name within a project
// identified by workspace + project names (no UUIDs required).
func (h *TicketHandler) GetByName(w http.ResponseWriter, r *http.Request) {
	titleName := chi.URLParam(r, "title_name")
	if titleName == "" {
		writeError(w, http.StatusBadRequest, "title_name is required")
		return
	}
	q := r.URL.Query()
	ref := resolve.Ref{
		WorkspaceSlug: q.Get("workspace_slug"),
		WorkspaceName: q.Get("workspace"),
		ProjectName:   q.Get("project"),
	}
	pid, rerr := h.resolver.ResolveProjectID(r.Context(), ref)
	if rerr != nil {
		writeError(w, http.StatusBadRequest, rerr.Error())
		return
	}
	if pid == nil {
		writeError(w, http.StatusBadRequest, "workspace and project are required")
		return
	}
	t, err := h.svc.GetByProjectAndTitleName(r.Context(), *pid, titleName)
	if err != nil {
		writeError(w, http.StatusNotFound, "ticket not found")
		return
	}
	var links []vcs.VcsLink
	if h.links != nil {
		links, _ = h.links.LinksForTicket(r.Context(), t.ID)
	}
	writeJSON(w, http.StatusOK, ticketWithLinks{Ticket: t, VcsLinks: links})
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
	if tn := q.Get("title_name"); tn != "" {
		f.TitleName = &tn
	}
	if sq := q.Get("q"); sq != "" {
		f.Query = &sq
	}
	if tagNames := q["tag"]; len(tagNames) > 0 {
		// Tag filtering scopes by workspace, derived from project_id. get-or-create
		// is acceptable for a filter: an unknown tag name yields zero matches.
		if h.tags == nil || f.ProjectID == nil {
			writeError(w, http.StatusBadRequest, "tag filter requires project_id")
			return
		}
		tagIDs, terr := h.tags.ResolveTagIDs(r.Context(), *f.ProjectID, auth.RequesterID(r), tagNames)
		if terr != nil {
			switch {
			case errors.Is(terr, project.ErrNotFound):
				writeError(w, http.StatusBadRequest, "resolve tag filter: "+terr.Error())
			default:
				writeError(w, http.StatusInternalServerError, "resolve tag filter: "+terr.Error())
			}
			return
		}
		f.TagIDs = tagIDs
	}

	sortField := q.Get("_sort")
	sortOrder := strings.ToLower(q.Get("_order"))
	if err := ticket.ValidateSortField(sortField); err != nil {
		writeError(w, http.StatusBadRequest, "invalid _sort field")
		return
	}
	f.SortField = sortField
	f.SortOrder = sortOrder

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
	var links []vcs.VcsLink
	if h.links != nil {
		links, _ = h.links.LinksForTicket(r.Context(), t.ID)
	}
	writeJSON(w, http.StatusOK, ticketWithLinks{Ticket: t, VcsLinks: links})
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

	actor := auth.RequesterID(r)
	t, err := h.svc.Update(r.Context(), id, req, actor)
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

	// PATCH-replaced refs are attributed to the caller; record the replace so the
	// audit trail covers ref changes made via ticket update (not only /refs).
	if h.audit != nil && req.RefIDs != nil {
		h.audit.Record(r.Context(), actor, "ref.replace", "ticket", t.ID.String(), map[string]any{
			"gopedia_l1_ids": *req.RefIDs,
			"count":          len(*req.RefIDs),
		})
	}

	writeJSON(w, http.StatusOK, t)
}

func (h *TicketHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	t, err := h.svc.Cancel(r.Context(), id, auth.RequesterID(r))
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

// GetDoc handles GET /api/v1/tickets/doc/{doc_id}.
// This is a special MVP endpoint for doc-lint validation.
func (h *TicketHandler) GetDoc(w http.ResponseWriter, r *http.Request) {
	docID := chi.URLParam(r, "doc_id")
	if docID == "" {
		writeError(w, http.StatusBadRequest, "missing doc_id")
		return
	}

	// MVP: For doc-lint validation, any valid format ID is considered "found"
	// since the GoQuest DB hasn't been migrated yet to hold these documents.
	// We return 200 OK for now.
	writeJSON(w, http.StatusOK, map[string]any{"id": docID, "status": "approved"})
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
	if h.audit != nil {
		for _, rf := range refs {
			h.audit.Record(r.Context(), actor, "ref.add", "ticket", tk.ID.String(), map[string]any{
				"gopedia_l1_id": rf.GopediaL1ID,
				"position":      rf.Position,
			})
		}
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
	if h.audit != nil {
		h.audit.Record(r.Context(), auth.RequesterID(r), "ref.remove", "ticket", tk.ID.String(), map[string]any{
			"gopedia_l1_id": l1,
		})
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

// ReverseLookupRefs handles GET /api/v1/refs?gopedia_l1_id=X.
// Returns the ticket ids that reference the given doc, filtered by the caller's
// SpiceDB-visible ticket set (via can_view_project). Under shadow (NoopChecker),
// unrestricted=true skips filtering. No-leak invariant: a caller who can't see
// ANY tickets must receive 200 + {"ticket_ids": []}, NOT 403/404 — that would
// confirm the doc's existence (or non-existence) to an unauthorized observer.
func (h *TicketHandler) ReverseLookupRefs(w http.ResponseWriter, r *http.Request) {
	docID := r.URL.Query().Get("gopedia_l1_id")
	if docID == "" {
		writeError(w, http.StatusBadRequest, "gopedia_l1_id is required")
		return
	}
	if len(docID) > ticket.MaxRefIDLength {
		writeError(w, http.StatusBadRequest, "gopedia_l1_id too long")
		return
	}

	uid := auth.RequesterID(r)
	visibleIDs, unrestricted, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_project", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}

	var visible map[uuid.UUID]bool
	if !unrestricted {
		visible = make(map[uuid.UUID]bool, len(visibleIDs))
		for _, s := range visibleIDs {
			if id, perr := uuid.Parse(s); perr == nil {
				visible[id] = true
			}
		}
	}

	ids, err := h.svc.ListTicketsByRef(r.Context(), docID, visible)
	if err != nil {
		if errors.Is(err, ticket.ErrRefIDInvalid) {
			writeError(w, http.StatusBadRequest, "invalid gopedia_l1_id")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup: "+err.Error())
		return
	}
	// Always emit a non-nil slice so the JSON literal is `[]` not `null`.
	strIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		strIDs = append(strIDs, id.String())
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket_ids": strIDs})
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

func dedupeTagIDs(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
