package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/project"
)

type ProjectHandler struct {
	svc *project.Service
}

func NewProjectHandler(svc *project.Service) *ProjectHandler {
	return &ProjectHandler{svc: svc}
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
	writeJSON(w, http.StatusOK, list)
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
	var req project.SetRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	targetUID := chi.URLParam(r, "uid")
	if err := h.svc.AddMember(r.Context(), pid, req, targetUID); err != nil {
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

