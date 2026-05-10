package template

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) Create(ctx context.Context, projectID uuid.UUID, req CreateReq, createdBy string) (*Template, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if req.CompletionMode == "" {
		req.CompletionMode = ticket.CompletionManual
	}
	if req.Type == "" {
		req.Type = ticket.TypeTask
	}
	if req.Priority == "" {
		req.Priority = ticket.PriorityNormal
	}

	t := &Template{
		ID:             uuid.New(),
		ProjectID:      projectID,
		Name:           req.Name,
		Title:          req.Title,
		Description:    req.Description,
		Type:           req.Type,
		Priority:       req.Priority,
		CompletionMode: req.CompletionMode,
		CompletionSpec: req.CompletionSpec,
		RewardXP:       req.RewardXP,
		CreatedBy:      createdBy,
		CreatedAt:      time.Now().UTC(),
	}

	if err := s.store.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}
	return t, nil
}

func (s *Service) List(ctx context.Context, projectID uuid.UUID) ([]*Template, error) {
	return s.store.List(ctx, projectID)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Template, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.Delete(ctx, id)
}

// ToTicketReq converts a template into a CreateTicketReq for instantiation.
func (s *Service) ToTicketReq(t *Template, overrides ticket.CreateTicketReq) ticket.CreateTicketReq {
	req := ticket.CreateTicketReq{
		Title:          t.Title,
		Description:    t.Description,
		Type:           t.Type,
		Priority:       t.Priority,
		CompletionMode: t.CompletionMode,
		CompletionSpec: t.CompletionSpec,
		RewardXP:       t.RewardXP,
		ProjectID:      &t.ProjectID,
	}
	// Allow caller to override specific fields.
	if overrides.Title != "" {
		req.Title = overrides.Title
	}
	if overrides.AssigneeID != "" {
		req.AssigneeID = overrides.AssigneeID
		req.AssigneeType = overrides.AssigneeType
	}
	if overrides.DueAt != nil {
		req.DueAt = overrides.DueAt
	}
	return req
}
