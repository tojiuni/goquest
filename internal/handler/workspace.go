package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/workspace"
)

type WorkspaceSvc interface {
	EnsurePersonalWorkspace(ctx context.Context, userID, displayName string) (*workspace.Workspace, error)
	Create(ctx context.Context, ownerID string, req workspace.CreateReq) (*workspace.Workspace, error)
	Get(ctx context.Context, id uuid.UUID) (*workspace.Workspace, error)
	Update(ctx context.Context, id uuid.UUID, req workspace.UpdateReq) (*workspace.Workspace, error)
	Delete(ctx context.Context, id uuid.UUID) error
	ListByUser(ctx context.Context, userID string) ([]*workspace.Workspace, error)
	GetMember(ctx context.Context, workspaceID uuid.UUID, userID string) (*workspace.Member, error)
	ListMembers(ctx context.Context, workspaceID uuid.UUID) ([]*workspace.Member, error)
	SetMemberRole(ctx context.Context, workspaceID uuid.UUID, targetUserID string, req workspace.SetRoleReq, callerRole workspace.Role) error
	RemoveMember(ctx context.Context, workspaceID uuid.UUID, targetUserID string, callerRole workspace.Role) error
	CreateInvitation(ctx context.Context, workspaceID uuid.UUID, inviterID string, req workspace.InviteReq) (*workspace.Invitation, error)
	ListInvitations(ctx context.Context, workspaceID uuid.UUID) ([]*workspace.Invitation, error)
	DeleteInvitation(ctx context.Context, invID uuid.UUID) error
	AcceptInvitation(ctx context.Context, token, userID string) (*workspace.Workspace, error)
}

type WorkspaceHandler struct {
	svc WorkspaceSvc
}

func NewWorkspaceHandler(svc WorkspaceSvc) *WorkspaceHandler {
	return &WorkspaceHandler{svc: svc}
}

// POST /workspaces
func (h *WorkspaceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req workspace.CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ownerID := auth.RequesterID(r)
	ws, err := h.svc.Create(r.Context(), ownerID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// GET /workspaces
func (h *WorkspaceHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := auth.RequesterID(r)
	list, err := h.svc.ListByUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": list})
}

// GET /workspaces/:wid
func (h *WorkspaceHandler) Get(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	ws, err := h.svc.Get(r.Context(), wid)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// PATCH /workspaces/:wid
func (h *WorkspaceHandler) Update(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	var req workspace.UpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	ws, err := h.svc.Update(r.Context(), wid, req)
	if err != nil {
		if errors.Is(err, workspace.ErrDuplicateName) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// DELETE /workspaces/:wid
func (h *WorkspaceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	if err := h.svc.Delete(r.Context(), wid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /workspaces/:wid/members
func (h *WorkspaceHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	members, err := h.svc.ListMembers(r.Context(), wid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// PATCH /workspaces/:wid/members/:uid/role
func (h *WorkspaceHandler) SetMemberRole(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	targetUID := chi.URLParam(r, "uid")
	var req workspace.SetRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	callerRole := auth.WorkspaceRoleFromCtx(r.Context())
	if err := h.svc.SetMemberRole(r.Context(), wid, targetUID, req, callerRole); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /workspaces/:wid/members/:uid
func (h *WorkspaceHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	targetUID := chi.URLParam(r, "uid")
	callerRole := auth.WorkspaceRoleFromCtx(r.Context())
	if err := h.svc.RemoveMember(r.Context(), wid, targetUID, callerRole); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /workspaces/:wid/invitations
func (h *WorkspaceHandler) CreateInvitation(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	var req workspace.InviteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	inviterID := auth.RequesterID(r)
	inv, err := h.svc.CreateInvitation(r.Context(), wid, inviterID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, inv)
}

// GET /workspaces/:wid/invitations
func (h *WorkspaceHandler) ListInvitations(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	list, err := h.svc.ListInvitations(r.Context(), wid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": list})
}

// DELETE /workspaces/:wid/invitations/:iid
func (h *WorkspaceHandler) DeleteInvitation(w http.ResponseWriter, r *http.Request) {
	iid, err := uuid.Parse(chi.URLParam(r, "iid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid invitation id")
		return
	}
	if err := h.svc.DeleteInvitation(r.Context(), iid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /invitations/:token/accept
func (h *WorkspaceHandler) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	userID := auth.RequesterID(r)
	ws, err := h.svc.AcceptInvitation(r.Context(), token, userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ws)
}
