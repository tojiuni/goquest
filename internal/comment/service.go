package comment

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (svc *Service) Create(ctx context.Context, ticketID uuid.UUID, authorID string, kind AuthorKind, req CreateReq) (*Comment, error) {
	if strings.TrimSpace(req.Body) == "" {
		return nil, errors.New("comment body is required")
	}
	if kind == AuthorKindCustomer && req.IsInternal {
		return nil, errors.New("customers cannot post internal comments")
	}
	if req.IsDraft && kind != AuthorKindAgent {
		return nil, errors.New("only agents can post draft comments")
	}
	c := &Comment{
		ID:            uuid.New(),
		TicketID:      ticketID,
		AuthorID:      authorID,
		AuthorKind:    kind,
		Body:          req.Body,
		IsInternal:    req.IsInternal,
		IsDraft:       req.IsDraft,
		GardenerScore: req.GardenerScore,
		CreatedAt:     time.Now().UTC(),
	}
	if err := svc.store.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Publish converts a draft (e.g. AI suggestion) to a published comment that
// is visible to the customer. Only staff/system actors are permitted.
func (svc *Service) Publish(ctx context.Context, id uuid.UUID, actorKind AuthorKind) error {
	if actorKind != AuthorKindStaff && actorKind != AuthorKindSystem {
		return ErrForbidden
	}
	return svc.store.Publish(ctx, id)
}

func (svc *Service) Get(ctx context.Context, id uuid.UUID) (*Comment, error) {
	return svc.store.Get(ctx, id)
}

func (svc *Service) ListByTicket(ctx context.Context, ticketID uuid.UUID, includeInternal bool) ([]*Comment, error) {
	return svc.store.ListByTicket(ctx, ticketID, includeInternal)
}

func (svc *Service) Update(ctx context.Context, id uuid.UUID, authorID string, req UpdateReq) (*Comment, error) {
	if strings.TrimSpace(req.Body) == "" {
		return nil, errors.New("comment body is required")
	}
	existing, err := svc.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.AuthorID != authorID {
		return nil, ErrForbidden
	}
	return svc.store.Update(ctx, id, req.Body)
}

func (svc *Service) Delete(ctx context.Context, id uuid.UUID, authorID string) error {
	existing, err := svc.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing.AuthorID != authorID {
		return ErrForbidden
	}
	return svc.store.Delete(ctx, id)
}

var ErrForbidden = errors.New("comment: forbidden")
