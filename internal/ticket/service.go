package ticket

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

type Publisher interface {
	PublishCreated(ctx context.Context, t *Ticket) error
	PublishAssigned(ctx context.Context, t *Ticket) error
	PublishUpdated(ctx context.Context, t *Ticket) error
	PublishCompleted(ctx context.Context, t *Ticket) error
}

type NuclexEmitter interface {
	EmitCompleted(ctx context.Context, t *Ticket) error
	EmitFailed(ctx context.Context, t *Ticket) error
}

type Service struct {
	store  Store
	pub    Publisher
	nuclex NuclexEmitter
}

func NewService(store Store, pub Publisher, nuclex NuclexEmitter) *Service {
	return &Service{store: store, pub: pub, nuclex: nuclex}
}

// Create creates a new ticket. requesterID is pre-extracted from the JWT by the HTTP handler.
func (s *Service) Create(ctx context.Context, req CreateTicketReq, requesterID string) (*Ticket, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if req.Type == "" {
		req.Type = TypeTask
	}
	if req.Priority == "" {
		req.Priority = PriorityNormal
	}
	if req.AssigneeType == "" {
		req.AssigneeType = AssigneeHuman
	}
	if req.Domain != "" {
		if err := validateDomain(req.Domain); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	t := &Ticket{
		ID:            uuid.New(),
		Title:         req.Title,
		Description:   req.Description,
		Type:          req.Type,
		Status:        StatusPending,
		Priority:      req.Priority,
		AssigneeType:  req.AssigneeType,
		AssigneeID:    req.AssigneeID,
		RequesterID:   requesterID,
		SourceBot:     req.SourceBot,
		Domain:        req.Domain,
		AgentContext:  req.AgentContext,
		VikunjaTaskID: req.VikunjaTaskID,
		Metadata:      req.Metadata,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.store.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}

	if err := s.pub.PublishCreated(ctx, t); err != nil {
		log.Printf("warn: publish created: %v", err)
	}

	if t.AssigneeType == AssigneeAgent {
		if err := s.pub.PublishAssigned(ctx, t); err != nil {
			log.Printf("warn: publish assigned: %v", err)
		}
	}

	return t, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateTicketReq) (*Ticket, error) {
	t, err := s.store.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	if err := s.pub.PublishUpdated(ctx, t); err != nil {
		log.Printf("warn: publish updated: %v", err)
	}

	if t.Status == StatusCompleted || t.Status == StatusFailed {
		if err := s.pub.PublishCompleted(ctx, t); err != nil {
			log.Printf("warn: publish completed: %v", err)
		}
		if s.nuclex != nil {
			var emitErr error
			if t.Status == StatusCompleted {
				emitErr = s.nuclex.EmitCompleted(ctx, t)
			} else {
				emitErr = s.nuclex.EmitFailed(ctx, t)
			}
			if emitErr != nil {
				log.Printf("warn: nuclex emit: %v", emitErr)
			}
		}
	}

	if req.AssigneeType != nil && *req.AssigneeType == AssigneeAgent {
		if err := s.pub.PublishAssigned(ctx, t); err != nil {
			log.Printf("warn: publish assigned: %v", err)
		}
	}

	return t, nil
}

func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	cancelled := StatusCancelled
	return s.Update(ctx, id, UpdateTicketReq{Status: &cancelled})
}

func (s *Service) List(ctx context.Context, f ListTicketsFilter) ([]*Ticket, int, error) {
	return s.store.List(ctx, f)
}

var validDomains = map[Domain]struct{}{
	DomainInfra: {}, DomainData: {}, DomainCode: {},
	DomainMonitor: {}, DomainKnowledge: {}, DomainPM: {},
}

func validateDomain(d Domain) error {
	if _, ok := validDomains[d]; !ok {
		return fmt.Errorf("invalid domain: %s", d)
	}
	return nil
}
