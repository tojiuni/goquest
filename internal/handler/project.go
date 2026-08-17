package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/spicedb"
)

type ProjectSvc interface {
	Create(ctx context.Context, workspaceID uuid.UUID, creatorID string, req project.CreateReq) (*project.Project, error)
	Get(ctx context.Context, id uuid.UUID) (*project.Project, error)
	Update(ctx context.Context, id uuid.UUID, req project.UpdateReq) (*project.Project, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*project.Project, error)
	GetMember(ctx context.Context, projectID uuid.UUID, userID string) (*project.Member, error)
	ListMembers(ctx context.Context, projectID uuid.UUID) ([]*project.Member, error)
	AddMember(ctx context.Context, projectID uuid.UUID, req project.SetRoleReq, targetUserID string) error
	SetMemberRole(ctx context.Context, projectID uuid.UUID, targetUserID string, req project.SetRoleReq) error
	RemoveMember(ctx context.Context, projectID uuid.UUID, targetUserID string) error
}

type ProjectHandler struct {
	svc     ProjectSvc
	checker spicedb.PermissionChecker
}

// NewProjectHandler wires the project HTTP handlers. checker may be nil in
// tests that don't exercise List — it is required for correct visibility
// filtering in production (goq-project-list-missing-spicedb-visibility-filter);
// callers that want unfiltered legacy behavior should pass &spicedb.NoopChecker{}
// explicitly rather than nil, since a nil checker panics in List.
func NewProjectHandler(svc ProjectSvc, checker spicedb.PermissionChecker) *ProjectHandler {
	return &ProjectHandler{svc: svc, checker: checker}
}

// POST /workspaces/:wid/projects
func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	var req project.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	requesterID := auth.RequesterID(r)
	p, err := h.svc.Create(r.Context(), wid, requesterID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// GET /workspaces/:wid/projects
//
// goq-project-list-missing-spicedb-visibility-filter: ListByWorkspace returns
// every project in the workspace with no per-caller filtering — the route is
// only gated by requireWsViewer (any workspace role), so a caller who only
// has project-level access to one project could see every other project's
// name/description too. Filter by the same SpiceDB can_view permission
// GET /tickets already uses (via visibleFilter), so the two endpoints agree.
func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	list, err := h.svc.ListByWorkspace(r.Context(), wid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	uid := auth.RequesterID(r)
	visibleIDs, unrestricted, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeProject, "can_view", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	if !unrestricted {
		visible := make(map[uuid.UUID]bool, len(visibleIDs))
		for _, s := range visibleIDs {
			if pid, err := uuid.Parse(s); err == nil {
				visible[pid] = true
			}
		}
		filtered := list[:0]
		for _, p := range list {
			if visible[p.ID] {
				filtered = append(filtered, p)
			}
		}
		list = filtered
	}

	writeJSON(w, http.StatusOK, map[string]any{"projects": list})
}

// GET /workspaces/:wid/projects/:pid
func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	p, err := h.svc.Get(r.Context(), pid)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// PATCH /workspaces/:wid/projects/:pid
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req project.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	p, err := h.svc.Update(r.Context(), pid, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// DELETE /workspaces/:wid/projects/:pid
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if err := h.svc.Delete(r.Context(), pid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /workspaces/:wid/projects/:pid/members
func (h *ProjectHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	members, err := h.svc.ListMembers(r.Context(), pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, members)
}

// POST /workspaces/:wid/projects/:pid/members
func (h *ProjectHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req project.AddMemberReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if err := h.svc.AddMember(r.Context(), pid, project.SetRoleReq{Role: req.Role}, req.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PATCH /workspaces/:wid/projects/:pid/members/:uid/role
func (h *ProjectHandler) SetMemberRole(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req project.SetRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	targetUID := chi.URLParam(r, "uid")
	if err := h.svc.SetMemberRole(r.Context(), pid, targetUID, req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /workspaces/:wid/projects/:pid/members/:uid
func (h *ProjectHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(chi.URLParam(r, "pid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	targetUID := chi.URLParam(r, "uid")
	if err := h.svc.RemoveMember(r.Context(), pid, targetUID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
