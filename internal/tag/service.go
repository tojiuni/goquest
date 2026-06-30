package tag

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (svc *Service) Create(ctx context.Context, workspaceID uuid.UUID, creatorID string, req CreateReq) (*Tag, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("tag name is required")
	}
	color := req.Color
	if color == "" {
		color = "#1677ff"
	}
	t := &Tag{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Name:        req.Name,
		Color:       color,
		CreatedBy:   creatorID,
		CreatedAt:   time.Now().UTC(),
	}
	if err := svc.store.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (svc *Service) Get(ctx context.Context, id uuid.UUID) (*Tag, error) {
	return svc.store.Get(ctx, id)
}

func (svc *Service) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Tag, error) {
	return svc.store.Update(ctx, id, req)
}

func (svc *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return svc.store.Delete(ctx, id)
}

func (svc *Service) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Tag, error) {
	return svc.store.ListByWorkspace(ctx, workspaceID)
}

// GetOrCreate returns the workspace tag whose name matches (case-insensitive),
// creating it when absent. A concurrent creator losing the unique-index race
// (tags_workspace_lower_name_key) is absorbed by re-reading the now-present row.
func (svc *Service) GetOrCreate(ctx context.Context, workspaceID uuid.UUID, creatorID, name string) (*Tag, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("tag name is required")
	}
	existing, err := svc.store.GetByName(ctx, workspaceID, name)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	created, cerr := svc.Create(ctx, workspaceID, creatorID, CreateReq{Name: name})
	if cerr == nil {
		return created, nil
	}
	if !errors.Is(cerr, ErrDuplicateName) {
		return nil, cerr
	}
	// Lost the unique-index race: another creator inserted the same name. Re-read.
	if reread, rerr := svc.store.GetByName(ctx, workspaceID, name); rerr == nil {
		return reread, nil
	}
	return nil, cerr
}
