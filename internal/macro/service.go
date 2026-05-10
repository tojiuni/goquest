package macro

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (svc *Service) Create(ctx context.Context, workspaceID uuid.UUID, creatorID string, req CreateReq) (*Macro, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("macro name is required")
	}
	if strings.TrimSpace(req.Body) == "" {
		return nil, errors.New("macro body is required")
	}
	now := time.Now().UTC()
	m := &Macro{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		ProjectID:   req.ProjectID,
		Name:        req.Name,
		Body:        req.Body,
		CreatedBy:   creatorID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := svc.store.Create(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (svc *Service) Get(ctx context.Context, id uuid.UUID) (*Macro, error) {
	return svc.store.Get(ctx, id)
}

func (svc *Service) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Macro, error) {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return nil, errors.New("macro name cannot be empty")
	}
	if req.Body != nil && strings.TrimSpace(*req.Body) == "" {
		return nil, errors.New("macro body cannot be empty")
	}
	return svc.store.Update(ctx, id, req)
}

func (svc *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return svc.store.Delete(ctx, id)
}

func (svc *Service) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, projectID *uuid.UUID) ([]*Macro, error) {
	return svc.store.ListByWorkspace(ctx, workspaceID, projectID)
}
