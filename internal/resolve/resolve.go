// Package resolve turns human-friendly workspace/project identifiers into a project UUID.
package resolve

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/workspace"
)

var (
	// ErrIncompleteRef: a workspace identifier was given without project_name, or vice versa.
	ErrIncompleteRef     = errors.New("resolve: workspace identifier and project_name must be provided together")
	ErrWorkspaceNotFound = errors.New("resolve: workspace not found")
	ErrProjectNotFound   = errors.New("resolve: project not found")
)

// Ref identifies a project by names instead of UUID.
type Ref struct {
	WorkspaceSlug string
	WorkspaceName string
	ProjectName   string
}

func (r Ref) hasWorkspace() bool {
	return strings.TrimSpace(r.WorkspaceSlug) != "" || strings.TrimSpace(r.WorkspaceName) != ""
}

// Empty reports whether the ref carries no identifying information at all.
func (r Ref) Empty() bool {
	return !r.hasWorkspace() && strings.TrimSpace(r.ProjectName) == ""
}

type workspaceLookup interface {
	GetBySlug(ctx context.Context, slug string) (*workspace.Workspace, error)
	GetByName(ctx context.Context, name string) (*workspace.Workspace, error)
}

type projectLookup interface {
	GetByWorkspaceAndName(ctx context.Context, workspaceID uuid.UUID, name string) (*project.Project, error)
}

type Resolver struct {
	ws workspaceLookup
	pj projectLookup
}

func New(ws workspaceLookup, pj projectLookup) *Resolver {
	return &Resolver{ws: ws, pj: pj}
}

// ResolveProjectID resolves ref to a project UUID.
//   - Empty ref → (nil, nil): caller leaves project unset.
//   - Workspace id present XOR project_name → ErrIncompleteRef.
//   - Workspace: slug preferred when both slug and name are set.
func (r *Resolver) ResolveProjectID(ctx context.Context, ref Ref) (*uuid.UUID, error) {
	if ref.Empty() {
		return nil, nil
	}
	if ref.hasWorkspace() == (strings.TrimSpace(ref.ProjectName) == "") {
		return nil, ErrIncompleteRef
	}

	var (
		ws  *workspace.Workspace
		err error
	)
	if strings.TrimSpace(ref.WorkspaceSlug) != "" {
		ws, err = r.ws.GetBySlug(ctx, ref.WorkspaceSlug)
	} else {
		ws, err = r.ws.GetByName(ctx, ref.WorkspaceName)
	}
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrWorkspaceNotFound
		}
		return nil, err
	}

	p, err := r.pj.GetByWorkspaceAndName(ctx, ws.ID, ref.ProjectName)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	return &p.ID, nil
}
