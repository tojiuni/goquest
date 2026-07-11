package handler

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// visibleFilter builds the caller's SpiceDB visibility filter: nil when the
// checker reports unrestricted access (shadow/Noop → no restriction), else the
// project + restricted id sets. Shared by List and TicketAnalytics so read and
// aggregate paths scope identically. A live Checker returns unrestricted=false
// for both lookups and Noop returns true for both, so the two always agree.
func (h *TicketHandler) visibleFilter(r *http.Request) (*ticket.VisibleTicketIDs, error) {
	uid := auth.RequesterID(r)
	projIDs, unrP, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_project", uid)
	if err != nil {
		return nil, err
	}
	restrIDs, unrR, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_restricted", uid)
	if err != nil {
		return nil, err
	}
	if unrP && unrR {
		return nil, nil
	}
	return &ticket.VisibleTicketIDs{
		Project:    parseTicketUUIDs(projIDs),
		Restricted: parseTicketUUIDs(restrIDs),
	}, nil
}

type analyticsResp struct {
	GroupBy string             `json:"group_by"`
	Buckets []ticket.AggBucket `json:"buckets"`
	Total   int                `json:"total"`
}

// TicketAnalytics serves GET /analytics/tickets?group_by=…: COUNT(*) grouped by
// one whitelisted column, scoped by the same optional filters as ticket list
// (project_id, workspace_id, status) plus the caller's SpiceDB visibility. The
// group_by value is validated against a whitelist in the store; an unknown key
// is a 400.
func (h *TicketHandler) TicketAnalytics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	groupBy := q.Get("group_by")
	if groupBy == "" {
		writeError(w, http.StatusBadRequest, "group_by is required")
		return
	}

	var f ticket.ListTicketsFilter
	if s := q.Get("status"); s != "" {
		st := ticket.Status(s)
		f.Status = &st
	}
	if pid := q.Get("project_id"); pid != "" {
		u, err := uuid.Parse(pid)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid project_id")
			return
		}
		f.ProjectID = &u
	}
	if wid := q.Get("workspace_id"); wid != "" {
		u, err := uuid.Parse(wid)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		f.WorkspaceID = &u
	}

	vf, err := h.visibleFilter(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	f.VisibleFilter = vf

	buckets, err := h.svc.Aggregate(r.Context(), groupBy, f)
	if err != nil {
		if errors.Is(err, ticket.ErrInvalidGroupBy) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total := 0
	for _, b := range buckets {
		total += b.Count
	}
	if buckets == nil {
		buckets = []ticket.AggBucket{}
	}
	writeJSON(w, http.StatusOK, analyticsResp{GroupBy: groupBy, Buckets: buckets, Total: total})
}
