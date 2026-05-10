package ticket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
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

// XPAwarder is called when a ticket is completed to grant XP.
type XPAwarder interface {
	AwardXP(ctx context.Context, workspaceID, userID string, amount int) error
}

type Service struct {
	store  Store
	pub    Publisher
	nuclex NuclexEmitter
	xp     XPAwarder
}

func NewService(store Store, pub Publisher, nuclex NuclexEmitter, xp XPAwarder) *Service {
	return &Service{store: store, pub: pub, nuclex: nuclex, xp: xp}
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

	if req.CompletionMode == "" {
		req.CompletionMode = CompletionManual
	}
	if req.Visibility == "" {
		req.Visibility = VisibilityProject
	}

	now := time.Now().UTC()
	t := &Ticket{
		ID:             uuid.New(),
		Title:          req.Title,
		Description:    req.Description,
		Type:           req.Type,
		Status:         StatusPending,
		Priority:       req.Priority,
		AssigneeType:   req.AssigneeType,
		AssigneeID:     req.AssigneeID,
		RequesterID:    requesterID,
		SourceBot:      req.SourceBot,
		Domain:         req.Domain,
		AgentContext:   req.AgentContext,
		VikunjaTaskID:  req.VikunjaTaskID,
		Metadata:       req.Metadata,
		ProjectID:      req.ProjectID,
		ParentID:       req.ParentID,
		Visibility:     req.Visibility,
		CompletionMode: req.CompletionMode,
		CompletionSpec: req.CompletionSpec,
		RewardXP:       req.RewardXP,
		DueAt:          req.DueAt,
		CallbackURL:    req.CallbackURL,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.store.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}

	if len(req.TagIDs) > 0 {
		if err := s.store.SetTicketTags(ctx, t.ID, req.TagIDs); err != nil {
			log.Printf("warn: set ticket tags: %v", err)
		}
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
		// Award XP to requester on successful completion.
		if t.Status == StatusCompleted && t.RewardXP > 0 && t.ProjectID != nil && s.xp != nil {
			workspaceID := t.ProjectID.String() // proxy: workspace derived from context at higher layer
			if err := s.xp.AwardXP(ctx, workspaceID, t.RequesterID, t.RewardXP); err != nil {
				log.Printf("warn: award xp: %v", err)
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

func (s *Service) ListSubtasks(ctx context.Context, parentID uuid.UUID) ([]*Ticket, error) {
	return s.store.ListSubtasks(ctx, parentID)
}

func (s *Service) ListAttempts(ctx context.Context, ticketID uuid.UUID) ([]*Attempt, error) {
	return s.store.ListAttempts(ctx, ticketID)
}

// SubmitAttempt records an answer attempt for an answer-mode ticket.
// Returns the updated attempt (with IsCorrect set) and any error.
func (s *Service) SubmitAttempt(ctx context.Context, ticketID uuid.UUID, userID string, req SubmitAttemptReq) (*Attempt, error) {
	t, err := s.store.Get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if t.CompletionMode != CompletionAnswer {
		return nil, fmt.Errorf("ticket is not in answer mode")
	}
	if t.Status.IsTerminal() {
		return nil, fmt.Errorf("ticket is already closed")
	}

	var spec AnswerSpec
	specBytes, err := json.Marshal(t.CompletionSpec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return nil, fmt.Errorf("parse answer spec: %w", err)
	}

	if spec.MaxAttempts > 0 {
		count, err := s.store.CountAttempts(ctx, ticketID, userID)
		if err != nil {
			return nil, err
		}
		if count >= spec.MaxAttempts {
			return nil, fmt.Errorf("max attempts (%d) reached", spec.MaxAttempts)
		}
	}

	correct := false
	switch spec.AnswerType {
	case "exact":
		correct = strings.EqualFold(strings.TrimSpace(req.Answer), strings.TrimSpace(spec.CorrectAnswer))
	case "choice":
		correct = strings.EqualFold(req.Answer, spec.CorrectAnswer)
	default:
		// ai_eval: leave IsCorrect nil until AI grades it
	}

	a := &Attempt{
		ID:          uuid.New(),
		TicketID:    ticketID,
		UserID:      userID,
		Answer:      req.Answer,
		AttemptedAt: time.Now().UTC(),
	}
	if spec.AnswerType != "ai_eval" {
		a.IsCorrect = &correct
	}

	if err := s.store.CreateAttempt(ctx, a); err != nil {
		return nil, err
	}

	if a.IsCorrect != nil && *a.IsCorrect {
		completed := StatusCompleted
		if _, err := s.Update(ctx, ticketID, UpdateTicketReq{Status: &completed}); err != nil {
			log.Printf("warn: auto-complete ticket after correct answer: %v", err)
		}
	}

	return a, nil
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
