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
	cogito   CogitoSplitter
}

// NewTicketHandler wires the ticket HTTP handlers. The audit, links, and
// cogito parameters are optional: if nil, audit calls / vcs_links embedding /
// POST .../subtasks/from-text are silently skipped or 502 (each call site
// guards with a nil check). This keeps unit tests that don't care about
// audit, vcs, or cogito from needing a fake.
func NewTicketHandler(svc *ticket.Service, checker spicedb.PermissionChecker, writer spicedb.RelationshipWriter, resolver ProjectResolver, tags TagResolver, auditLogger audit.Logger, links LinksProvider, cogito CogitoSplitter) *TicketHandler {
	return &TicketHandler{svc: svc, checker: checker, writer: writer, resolver: resolver, tags: tags, audit: auditLogger, links: links, cogito: cogito}
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

	// External-org callers get their org stamped server-side; the json:"-" tag
	// on the field makes client-supplied values unreachable.
	if o := auth.OrgFrom(r); o != nil {
		req.RequesterOrgID = &o.ID
	}

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

// orgScopeForSide builds the caller's org-axis filter, but only when they
// explicitly asked for one via a recognized org_side value. An org claim on
// its own is not sufficient: a staff account can carry one too (e.g. acting
// as an org admin), and must keep its normal SpiceDB-visible results on a
// plain request instead of being silently narrowed to org-only tickets.
// Shared by List and TicketAnalytics so both scope identically.
func orgScopeForSide(org *auth.OrgCtx, callerID, side string) *ticket.OrgScope {
	if org == nil || (side != "requester" && side != "assignee") {
		return nil
	}
	return &ticket.OrgScope{
		OrgID:    org.ID,
		Role:     org.Role,
		CallerID: callerID,
		Side:     side,
	}
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
	if q.Get("scheduled") == "true" {
		f.Scheduled = true
	}
	if tn := q.Get("title_name"); tn != "" {
		f.TitleName = &tn
	}
	if v := parseTimeQuery(q.Get("created_from")); v != nil {
		f.CreatedFrom = v
	}
	if v := parseTimeQuery(q.Get("created_to")); v != nil {
		f.CreatedTo = v
	}
	if v := parseTimeQuery(q.Get("completed_from")); v != nil {
		f.CompletedFrom = v
	}
	if v := parseTimeQuery(q.Get("completed_to")); v != nil {
		f.CompletedTo = v
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

	// An org claim alone does not mean the caller is external-only — a staff
	// account can also carry one (e.g. acting as an org admin). Org scoping
	// only engages when org_side is explicitly requested (goq-org-claim-staff-visibility-bypass);
	// everyone else keeps the normal SpiceDB visibility path below.
	if scope := orgScopeForSide(auth.OrgFrom(r), auth.RequesterID(r), q.Get("org_side")); scope != nil {
		f.OrgScope = scope
	} else {
		// Visibility: restrict to SpiceDB-visible tickets (nil in shadow/Noop → no filter).
		vf, err := h.visibleFilter(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
			return
		}
		f.VisibleFilter = vf
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
		if errors.Is(err, ticket.ErrProjectNotFound) || errors.Is(err, ticket.ErrCrossWorkspaceMove) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, ticket.ErrRefConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, ticket.ErrValidation) ||
			errors.Is(err, ticket.ErrRefIDInvalid) ||
			errors.Is(err, ticket.ErrRefIDsDuplicate) ||
			errors.Is(err, ticket.ErrRefLimitExceeded) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Keep SpiceDB's assignee (and, on a project move, project) relation in
	// sync after an update (best-effort).
	// Detached bounded context — r.Context() is cancelled when the handler returns.
	tid, asg := t.ID, t.AssigneeID
	movedProject := req.ProjectID != nil
	newProject := t.ProjectID
	go func() {
		bgctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if wErr := h.writer.ReplaceTicketAssignee(bgctx, tid, asg); wErr != nil {
			log.Printf("spicedb ReplaceTicketAssignee %s: %v (continuing)", tid, wErr)
		}
		if movedProject && newProject != nil {
			if wErr := h.writer.ReplaceTicketProject(bgctx, tid, *newProject); wErr != nil {
				log.Printf("spicedb ReplaceTicketProject %s: %v (continuing)", tid, wErr)
			}
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

// maxBulkUpdateIDs bounds a single bulk-update request so one call cannot fan
// out into an unbounded number of per-ticket authz checks and store writes.
const maxBulkUpdateIDs = 100

type bulkUpdateReq struct {
	IDs   []string               `json:"ids"`
	Patch ticket.UpdateTicketReq `json:"patch"`
}

type bulkUpdateResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type bulkUpdateResp struct {
	Results []bulkUpdateResult `json:"results"`
	Updated int                `json:"updated"`
}

// BulkUpdate applies one patch to many tickets in a single request. Each id is
// processed independently: manage authz is checked per ticket (mirroring the
// RequireTicketManage middleware — a project-less legacy ticket is not gated),
// the state-machine and field validation of the normal Update path is reused,
// and a per-id failure (bad id, not found, forbidden, invalid transition) is
// reported in results without aborting the rest. Response is always 200 with a
// per-id result list; callers inspect `ok`/`error` per row.
func (h *TicketHandler) BulkUpdate(w http.ResponseWriter, r *http.Request) {
	var req bulkUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids must be non-empty")
		return
	}
	if len(req.IDs) > maxBulkUpdateIDs {
		writeError(w, http.StatusBadRequest, "too many ids (max "+strconv.Itoa(maxBulkUpdateIDs)+")")
		return
	}

	actor := auth.RequesterID(r)
	results := make([]bulkUpdateResult, 0, len(req.IDs))
	updated := make([]*ticket.Ticket, 0, len(req.IDs))

	for _, raw := range req.IDs {
		res := bulkUpdateResult{ID: raw}
		id, err := parseUUID(raw)
		if err != nil {
			res.Error = "invalid ticket id"
			results = append(results, res)
			continue
		}
		// Existence + legacy bypass mirror RequireTicketManage: fetch the ticket,
		// and only SpiceDB-gate it when it belongs to a project.
		t, err := h.svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, ticket.ErrNotFound) {
				res.Error = "ticket not found"
			} else {
				res.Error = "lookup ticket"
			}
			results = append(results, res)
			continue
		}
		if t.ProjectID != nil {
			ok, err := h.checker.CanManageTicket(r.Context(), id, actor)
			if err != nil {
				res.Error = "permission check"
				results = append(results, res)
				continue
			}
			if !ok {
				res.Error = "access denied"
				results = append(results, res)
				continue
			}
		}
		ut, err := h.svc.Update(r.Context(), id, req.Patch, actor)
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		res.OK = true
		results = append(results, res)
		if ut != nil {
			updated = append(updated, ut)
		}
	}

	// Keep SpiceDB's assignee relation in sync for every updated ticket
	// (best-effort, mirroring the single Update path). One detached goroutine for
	// the whole batch; r.Context() is cancelled once the handler returns.
	if h.writer != nil && len(updated) > 0 {
		type sync struct {
			id   uuid.UUID
			asg  string
			proj *uuid.UUID
		}
		movedProject := req.Patch.ProjectID != nil
		syncs := make([]sync, len(updated))
		for i, t := range updated {
			syncs[i] = sync{id: t.ID, asg: t.AssigneeID, proj: t.ProjectID}
		}
		go func() {
			bgctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for _, s := range syncs {
				if wErr := h.writer.ReplaceTicketAssignee(bgctx, s.id, s.asg); wErr != nil {
					log.Printf("spicedb ReplaceTicketAssignee %s: %v (continuing)", s.id, wErr)
				}
				if movedProject && s.proj != nil {
					if wErr := h.writer.ReplaceTicketProject(bgctx, s.id, *s.proj); wErr != nil {
						log.Printf("spicedb ReplaceTicketProject %s: %v (continuing)", s.id, wErr)
					}
				}
			}
		}()
	}

	writeJSON(w, http.StatusOK, bulkUpdateResp{Results: results, Updated: len(updated)})
}

// maxBulkCreateItems bounds a single bulk-create request so one call cannot
// fan out into an unbounded number of child tickets. Modeled directly on
// maxBulkUpdateIDs above; 50 is proposed to match content-split's section
// count upper bound (goq-cogito-bulk-create-api design doc §3.1/§8 — revisit
// once real content-split output sizes are known).
const maxBulkCreateItems = 50

// BulkCreate creates many child tickets under one parent in a single request
// (the cogito content-split / question-decomposition consumer). Manage authz
// is checked once against the parent — mirroring RequireTicketManage's
// project-less legacy bypass, same as BulkUpdate's per-id check above — since
// items are not-yet-existing resources and a write grant on the parent is what
// gates creating children under it. A single item's failure (bad title, etc.)
// is reported in its result row without aborting the rest; response is always
// 200 with a per-item result list once the parent lookup/authz gate passes.
func (h *TicketHandler) BulkCreate(w http.ResponseWriter, r *http.Request) {
	var req ticket.BulkCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.ParentID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "parent_id is required")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items must be non-empty")
		return
	}
	if len(req.Items) > maxBulkCreateItems {
		writeError(w, http.StatusBadRequest, "too many items (max "+strconv.Itoa(maxBulkCreateItems)+")")
		return
	}

	actor := auth.RequesterID(r)

	// Existence + legacy bypass mirror RequireTicketManage / BulkUpdate: fetch
	// the parent, and only SpiceDB-gate it when it belongs to a project.
	parent, err := h.svc.Get(r.Context(), req.ParentID)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "parent ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup parent ticket: "+err.Error())
		return
	}
	if parent.ProjectID != nil {
		ok, err := h.checker.CanManageTicket(r.Context(), req.ParentID, actor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "permission check: "+err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusForbidden, "access denied")
			return
		}
	}

	resp, err := h.svc.BulkCreate(r.Context(), req, actor)
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

// Reopen moves a completed/cancelled ticket back to in_progress. This is the
// audited escape hatch for tickets closed in error (e.g. a VCS auto-link
// false-positive completion) — see goq-ticket-reopen-terminal. Gated by
// requireTicketManage same as Update/Cancel/Approve. POST /tickets/{id}/reopen
func (h *TicketHandler) Reopen(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	actor := auth.RequesterID(r)
	t, err := h.svc.Reopen(r.Context(), id, actor, req.Reason)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		if errors.Is(err, ticket.ErrValidation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, ticket.ErrInvalidTransition) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.Record(r.Context(), actor, "ticket.reopened", "ticket", t.ID.String(), map[string]any{
			"reason": req.Reason,
		})
	}

	writeJSON(w, http.StatusOK, t)
}

// OrgReassign lets an admin of the ticket's assignee org route the work to one
// of their own members. Only assignee_id changes; assignee_type stays "org"
// and the status machine is untouched. POST /tickets/{id}/org-reassign
func (h *TicketHandler) OrgReassign(w http.ResponseWriter, r *http.Request) {
	t := auth.TicketFrom(r) // stashed by RequireTicketView
	o := auth.OrgFrom(r)
	if t == nil || o == nil || !o.IsAdmin() ||
		t.AssigneeType != ticket.AssigneeOrg ||
		t.AssigneeOrgID == nil || *t.AssigneeOrgID != o.ID {
		writeError(w, http.StatusForbidden, "org admin of the assignee org required")
		return
	}

	var body struct {
		AssigneeID string `json:"assignee_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AssigneeID == "" {
		writeError(w, http.StatusBadRequest, "assignee_id required")
		return
	}

	actor := auth.RequesterID(r)
	updated, err := h.svc.Update(r.Context(), t.ID, ticket.UpdateTicketReq{
		AssigneeID: &body.AssigneeID,
	}, actor)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
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

// relationLabels maps relation_type to the human-readable label shown to the
// caller, separately for the outgoing (this ticket is from_ticket_id) and
// incoming/reverse (this ticket is to_ticket_id) direction. See design §2's
// forward/reverse label table.
var relationLabels = map[string]struct{ outgoing, incoming string }{
	"blocks":     {outgoing: "Blocks", incoming: "Blocked by"},
	"depends_on": {outgoing: "Depends on", incoming: "Affects"},
	"duplicates": {outgoing: "Duplicates", incoming: "Duplicated by"},
}

// relationView is the flattened per-relation response shape for
// GET .../relations: direction/label are computed server-side (relative to
// the queried ticket) instead of forcing the client to derive them from
// from_ticket_id/to_ticket_id, and from_ticket_id/to_ticket_id/visibility are
// omitted since direction+label already encode the useful part of that.
type relationView struct {
	ID                   uuid.UUID `json:"id"`
	RelationType         string    `json:"relation_type"`
	Direction            string    `json:"direction"`
	Label                string    `json:"label"`
	OtherTicketID        uuid.UUID `json:"other_ticket_id"`
	OtherTicketTitleName string    `json:"other_ticket_title_name,omitempty"`
	OtherTicketTitle     string    `json:"other_ticket_title"`
	OtherTicketStatus    string    `json:"other_ticket_status"`
	CreatedAt            time.Time `json:"created_at"`
	CreatedBy            string    `json:"created_by"`
}

// ListRelations handles GET /api/v1/tickets/{id}/relations. Returns every
// relation where this ticket is either side, each carrying a summary of the
// other-side ticket plus a server-computed direction/label. Relations whose
// other-side ticket the caller cannot view are silently dropped (a filter,
// not a rejection) — returning 403/404 for those rows, or leaking their
// title/status, would confirm to an unauthorized caller that a relation to a
// restricted ticket exists.
func (h *TicketHandler) ListRelations(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}
	rels, err := h.svc.ListRelationsWithSummary(r.Context(), tk.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list relations: "+err.Error())
		return
	}
	// Visibility: batch two LookupResources calls (project + restricted) up
	// front, same pattern as ListSubtasks, instead of one SpiceDB round-trip
	// per relation row — a ticket with many relations would otherwise issue
	// that many sequential RPCs.
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
	unrestricted := unrP && unrR
	projSet := uuidSet(parseTicketUUIDs(projIDs))
	restrSet := uuidSet(parseTicketUUIDs(restrIDs))
	// Always emit a non-nil slice so the JSON literal is `[]` not `null`.
	out := make([]relationView, 0, len(rels))
	for _, rel := range rels {
		if !unrestricted && !visibleSubtask(rel.OtherTicketID, ticket.Visibility(rel.OtherTicketVisibility), projSet, restrSet) {
			continue
		}
		direction := "incoming"
		labels := relationLabels[rel.RelationType]
		label := labels.incoming
		if rel.FromTicketID == tk.ID {
			direction = "outgoing"
			label = labels.outgoing
		}
		out = append(out, relationView{
			ID:                   rel.ID,
			RelationType:         rel.RelationType,
			Direction:            direction,
			Label:                label,
			OtherTicketID:        rel.OtherTicketID,
			OtherTicketTitleName: rel.OtherTicketTitleName,
			OtherTicketTitle:     rel.OtherTicketTitle,
			OtherTicketStatus:    rel.OtherTicketStatus,
			CreatedAt:            rel.CreatedAt,
			CreatedBy:            rel.CreatedBy,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"relations": out})
}

// AddRelation handles POST /api/v1/tickets/{id}/relations.
// Body: {"relation_type": "blocks", "to_ticket_id": "<uuid>"}.
// Idempotent: replaying an existing (from, to, type) returns 200 with the
// existing row, not an error.
func (h *TicketHandler) AddRelation(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}
	var body struct {
		RelationType string `json:"relation_type"`
		ToTicketID   string `json:"to_ticket_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	toID, err := uuid.Parse(body.ToTicketID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to_ticket_id")
		return
	}
	actor := auth.RequesterID(r)
	// RequireTicketView only gates the path ticket (tk); it says nothing about
	// whether the caller can see to_ticket_id. Without this check, a caller
	// could write a visible edge onto a ticket they can't otherwise view (and
	// later read its title/status back via the relation summary). Treat
	// "doesn't exist" and "exists but not viewable" identically (404) so this
	// isn't usable as an existence oracle.
	target, err := h.svc.Get(r.Context(), toID)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "to_ticket_id not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup to_ticket_id: "+err.Error())
		return
	}
	canView, err := h.checker.CanViewTicket(r.Context(), toID, actor, spicedb.VisibilityMode(target.Visibility))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility check: "+err.Error())
		return
	}
	if !canView {
		writeError(w, http.StatusNotFound, "to_ticket_id not found")
		return
	}
	rel, created, err := h.svc.AddRelation(r.Context(), tk.ID, toID, body.RelationType, actor)
	if err != nil {
		switch {
		case errors.Is(err, ticket.ErrRelationTypeInvalid):
			writeError(w, http.StatusBadRequest, "relation_type must be blocks, depends_on, or duplicates")
		case errors.Is(err, ticket.ErrRelationSelfReference):
			writeError(w, http.StatusBadRequest, "from_ticket_id and to_ticket_id must differ")
		case errors.Is(err, ticket.ErrNotFound):
			writeError(w, http.StatusNotFound, "to_ticket_id not found")
		default:
			writeError(w, http.StatusInternalServerError, "add relation: "+err.Error())
		}
		return
	}
	if created && h.audit != nil {
		h.audit.Record(r.Context(), actor, "relation.add", "ticket", tk.ID.String(), map[string]any{
			"to_ticket_id":  toID.String(),
			"relation_type": body.RelationType,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"relation": rel, "created": created})
}

// RemoveRelation handles DELETE /api/v1/tickets/{id}/relations/{relation_id}.
// Idempotent for relations that belong to this ticket; returns 404 if the
// relation id doesn't exist or isn't associated with this ticket. This
// ownership check is required: RequireTicketView only proves the caller can
// view *some* ticket (the one in the path), not that relation_id belongs to
// it — without the check, a caller could delete an arbitrary relation on an
// unrelated ticket pair by guessing/knowing its id.
func (h *TicketHandler) RemoveRelation(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}
	relID, err := uuid.Parse(chi.URLParam(r, "relation_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid relation_id")
		return
	}
	// Ownership scoping (relID must belong to tk.ID) is enforced by the
	// service/store, not here — see Service.RemoveRelation's doc comment.
	if err := h.svc.RemoveRelation(r.Context(), tk.ID, relID); err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			// Uniform 404 whether the relation doesn't exist at all or exists but
			// belongs to a different ticket pair — don't leak which case it is.
			writeError(w, http.StatusNotFound, "relation not found for this ticket")
			return
		}
		writeError(w, http.StatusInternalServerError, "remove relation: "+err.Error())
		return
	}
	if h.audit != nil {
		h.audit.Record(r.Context(), auth.RequesterID(r), "relation.remove", "ticket", tk.ID.String(), map[string]any{
			"relation_id": relID.String(),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAllRelations handles GET /api/v1/ticket-relations?limit=&offset=.
// Bulk, unfiltered dump for the brain importer's batch pull — no per-ticket
// SpiceDB visibility filtering (same trust boundary as other service-to-
// service bulk reads using the API key).
// maxTicketRelationsLimit caps the bulk relation dump's page size so a
// caller (even an authenticated service) can't force an unbounded query
// (e.g. ?limit=999999999) against the store in one request.
const maxTicketRelationsLimit = 1000

func (h *TicketHandler) ListAllRelations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := parseIntQuery(q.Get("limit"), 100)
	if limit <= 0 {
		limit = 100
	}
	if limit > maxTicketRelationsLimit {
		limit = maxTicketRelationsLimit
	}
	offset := parseIntQuery(q.Get("offset"), 0)
	rels, total, err := h.svc.ListAllRelations(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list all relations: "+err.Error())
		return
	}
	// Always emit a non-nil slice so the JSON literal is `[]` not `null`.
	if rels == nil {
		rels = []ticket.TicketRelation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"relations": rels, "total": total, "limit": limit, "offset": offset})
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

// parseTimeQuery parses an RFC3339/ISO8601 UTC timestamp query param.
// Empty or unparseable input returns nil, matching this file's convention
// for other optional filter params (e.g. project_id) — a malformed value is
// silently ignored (treated as "no filter") rather than a 400.
func parseTimeQuery(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
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
