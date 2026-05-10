package macro

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, m *Macro) error
	Get(ctx context.Context, id uuid.UUID) (*Macro, error)
	Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Macro, error)
	Delete(ctx context.Context, id uuid.UUID) error
	// ListByWorkspace returns all macros in the workspace.
	// If projectID is non-nil, also restricts to that project (or workspace-wide null).
	ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, projectID *uuid.UUID) ([]*Macro, error)
}
