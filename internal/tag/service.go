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
