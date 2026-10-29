package auth

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	wskg "github.com/tojiuni/goquest/internal/workspace"
	pjpkg "github.com/tojiuni/goquest/internal/project"
)

type workspaceRoleGetter interface {
	GetMember(ctx context.Context, workspaceID uuid.UUID, userID string) (*wskg.Member, error)
}

type projectRoleGetter interface {
	GetMember(ctx context.Context, projectID uuid.UUID, userID string) (*pjpkg.Member, error)
}

type projectGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*pjpkg.Project, error)
}

// RequireProjectInWorkspace rejects requests whose `pid` project does not belong
// to the `wid` workspace in the URL. Workspace membership is checked against
// `wid` only, so without this a member of any workspace could address another
// workspace's project by pairing their own `wid` with a foreign `pid`.
// Mismatch and unknown project both return 404 so project ids can't be probed.
func RequireProjectInWorkspace(pj projectGetter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wid, err := uuid.Parse(chi.URLParam(r, "wid"))
			if err != nil {
				http.Error(w, "invalid workspace id", http.StatusBadRequest)
				return
			}
			pid, err := uuid.Parse(chi.URLParam(r, "pid"))
			if err != nil {
				http.Error(w, "invalid project id", http.StatusBadRequest)
				return
			}
			p, err := pj.Get(r.Context(), pid)
			if err != nil || p == nil || p.WorkspaceID != wid {
				http.Error(w, "project not found", http.StatusNotFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireWorkspaceRole enforces that the caller has at least `minRole` in the workspace
// identified by the `wid` URL parameter.
func RequireWorkspaceRole(ws workspaceRoleGetter, minRole wskg.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := RequesterID(r)
			if userID == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			wid, err := uuid.Parse(chi.URLParam(r, "wid"))
			if err != nil {
				http.Error(w, "invalid workspace id", http.StatusBadRequest)
				return
			}

			member, err := ws.GetMember(r.Context(), wid, userID)
			if err != nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			if !wskg.HasRole(member.Role, minRole) {
				http.Error(w, "insufficient workspace role", http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), workspaceRoleKey, member.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireProjectRole enforces that the caller has at least `minRole` in the project
// identified by the `pid` URL parameter.
// Falls back to the workspace role stored in context by RequireWorkspaceRole.
func RequireProjectRole(pj projectRoleGetter, minRole pjpkg.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := RequesterID(r)
			if userID == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			pid, err := uuid.Parse(chi.URLParam(r, "pid"))
			if err != nil {
				http.Error(w, "invalid project id", http.StatusBadRequest)
				return
			}

			member, err := pj.GetMember(r.Context(), pid, userID)
			if err == nil {
				// Explicit project role found.
				if !pjpkg.HasRole(member.Role, minRole) {
					http.Error(w, "insufficient project role", http.StatusForbidden)
					return
				}
				ctx := context.WithValue(r.Context(), projectRoleKey, member.Role)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// No explicit project role — fall back to workspace role.
			wsRole, _ := r.Context().Value(workspaceRoleKey).(wskg.Role)
			effectiveRole := wsRoleToProjectRole(wsRole)
			if !pjpkg.HasRole(effectiveRole, minRole) {
				http.Error(w, "insufficient project role", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// WorkspaceRoleFromCtx returns the workspace role stored by RequireWorkspaceRole.
func WorkspaceRoleFromCtx(ctx context.Context) wskg.Role {
	v, _ := ctx.Value(workspaceRoleKey).(wskg.Role)
	return v
}

// ProjectRoleFromCtx returns the project role stored by RequireProjectRole.
func ProjectRoleFromCtx(ctx context.Context) pjpkg.Role {
	v, _ := ctx.Value(projectRoleKey).(pjpkg.Role)
	return v
}

type rbacKey string

const (
	workspaceRoleKey rbacKey = "workspace_role"
	projectRoleKey   rbacKey = "project_role"
)

// wsRoleToProjectRole maps workspace roles to the equivalent project role.
func wsRoleToProjectRole(r wskg.Role) pjpkg.Role {
	switch r {
	case wskg.RoleOwner, wskg.RoleAdmin:
		return pjpkg.RoleAdmin
	case wskg.RoleMember:
		return pjpkg.RoleMember
	default:
		return pjpkg.RoleViewer
	}
}
