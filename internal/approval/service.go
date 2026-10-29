package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	maxNoteLen     = 2000
	maxEvidence    = 20
	maxLabelLen    = 64
	maxEvidenceLen = 500
)

var (
	// ErrForbidden means the caller may not perform this action on the approval.
	ErrForbidden = errors.New("not allowed")
	// ErrInvalidInput is a request the caller can fix (bad kind, oversized field).
	ErrInvalidInput = errors.New("invalid approval request")
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// RequestInput is what a caller may send when asking for an approval. The
// requester identity is never part of it; it comes from the authenticated
// principal.
type RequestInput struct {
	Kind            Kind
	Subject         Subject
	RequestNote     string
	Evidence        []EvidenceItem
	WithinDirection *bool
}

// Request records a pending approval on ticketID. principal is the
// authenticated requester; label is the optional, unverified session label.
func (s *Service) Request(ctx context.Context, ticketID uuid.UUID, in RequestInput, principal, label string) (*Approval, error) {
	if strings.TrimSpace(principal) == "" {
		return nil, fmt.Errorf("%w: requester identity required", ErrForbidden)
	}
	if in.Kind == KindTicket {
		// kind=ticket is written only by the legacy /approve bridge (plan P5).
		return nil, fmt.Errorf("%w: kind %q is reserved", ErrInvalidInput, in.Kind)
	}
	if len(in.RequestNote) > maxNoteLen {
		return nil, fmt.Errorf("%w: request_note too long", ErrInvalidInput)
	}
	if len(in.Evidence) > maxEvidence {
		return nil, fmt.Errorf("%w: too many evidence items", ErrInvalidInput)
	}
	for _, e := range in.Evidence {
		if strings.TrimSpace(e.Type) == "" || strings.TrimSpace(e.Ref) == "" || len(e.Ref) > maxEvidenceLen || len(e.Note) > maxEvidenceLen {
			return nil, fmt.Errorf("%w: evidence needs type and ref (max %d chars)", ErrInvalidInput, maxEvidenceLen)
		}
	}
	wd := in.WithinDirection
	if in.Kind != KindDeviation {
		wd = nil // only meaningful for deviation
	}
	if len(label) > maxLabelLen {
		label = label[:maxLabelLen]
	}
	return s.store.Create(ctx, NewReq{
		TicketID: ticketID, Kind: in.Kind, Subject: in.Subject,
		RequestedBy: principal, RequestedByLabel: label,
		RequestNote: in.RequestNote, Evidence: in.Evidence, WithinDirection: wd,
	})
}

// ListForTicket returns the ticket's approvals, newest first.
func (s *Service) ListForTicket(ctx context.Context, ticketID uuid.UUID, status Status, limit, offset int) ([]*Approval, error) {
	return s.store.List(ctx, ListFilter{TicketID: &ticketID, Status: status, Limit: limit, Offset: offset})
}

// GetForTicket returns the approval only if it belongs to ticketID, so a
// caller authorised for one ticket cannot read another ticket's approvals by id.
func (s *Service) GetForTicket(ctx context.Context, ticketID, id uuid.UUID) (*Approval, error) {
	a, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.TicketID != ticketID {
		return nil, ErrNotFound
	}
	return a, nil
}

// Withdraw lets the requester pull back a pending approval.
func (s *Service) Withdraw(ctx context.Context, ticketID, id uuid.UUID, principal, note string) (*Approval, error) {
	a, err := s.GetForTicket(ctx, ticketID, id)
	if err != nil {
		return nil, err
	}
	if principal == "" || a.RequestedBy != principal {
		return nil, fmt.Errorf("%w: only the requester can withdraw", ErrForbidden)
	}
	if len(note) > maxNoteLen {
		return nil, fmt.Errorf("%w: note too long", ErrInvalidInput)
	}
	return s.store.Transition(ctx, id, a.Status, StatusWithdrawn, principal, note, nil)
}
