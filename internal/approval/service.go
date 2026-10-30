package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

type Service struct {
	store Store
	rules RuleStore
}

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

// ActorKind is how the caller authenticated, as classified by the auth layer.
type ActorKind string

const (
	ActorHuman   ActorKind = "human"
	ActorSession ActorKind = "session"
	ActorOther   ActorKind = "other"
)

// Actor is an authenticated caller. ID is the server-derived principal.
type Actor struct {
	ID   string
	Kind ActorKind
}

// DenyReason says why a decision was refused, for the API and the audit log.
type DenyReason string

const (
	ReasonPrincipalCannotDecide DenyReason = "principal_cannot_decide"
	ReasonKindNotDelegable      DenyReason = "kind_not_delegable"
	ReasonNoMatchingRule        DenyReason = "no_matching_rule"
	ReasonSelfApproval          DenyReason = "self_approval"
)

// DeniedError is returned when the caller is not allowed to decide this approval.
type DeniedError struct{ Reason DenyReason }

func (e *DeniedError) Error() string { return "decision denied: " + string(e.Reason) }

// DecideInput is a decision request. Who decides and on what basis is never an
// input: both come from the Actor and the stored delegation rules.
type DecideInput struct {
	TicketID    uuid.UUID
	WorkspaceID uuid.UUID // uuid.Nil for a ticket without a project
	ApprovalID  uuid.UUID
	Decision    Status
	Note        string
	UserRef     string // required for desk_delegated: where the user's go-ahead is recorded
}

// WithRules attaches the delegation rule store. Without one, no session can
// ever decide (only humans).
func (s *Service) WithRules(rs RuleStore) *Service { s.rules = rs; return s }

// Decide records a decision on a pending approval.
//
// human: basis=user_direct. session: basis=desk_delegated, allowed only when the
// kind is under the fixed delegation ceiling, the session is not the requester,
// and an active rule for this workspace names the session and covers the
// kind/repo/conditions. Every other caller is refused.
func (s *Service) Decide(ctx context.Context, in DecideInput, actor Actor) (*Approval, error) {
	switch in.Decision {
	case StatusApproved:
	case StatusRejected, StatusRevisionRequested:
		if strings.TrimSpace(in.Note) == "" {
			return nil, fmt.Errorf("%w: note is required to reject or request revision", ErrInvalidInput)
		}
	default:
		return nil, fmt.Errorf("%w: decision must be approved, rejected or revision_requested", ErrInvalidInput)
	}
	if len(in.Note) > maxNoteLen || len(in.UserRef) > maxEvidenceLen {
		return nil, fmt.Errorf("%w: note or user_ref too long", ErrInvalidInput)
	}
	if strings.TrimSpace(actor.ID) == "" {
		return nil, &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	a, err := s.GetForTicket(ctx, in.TicketID, in.ApprovalID)
	if err != nil {
		return nil, err
	}
	if a.Status != StatusPending {
		return nil, ErrInvalidTransition
	}

	d := &Decision{Status: in.Decision, DecidedBy: actor.ID, Note: in.Note}
	switch actor.Kind {
	case ActorHuman:
		d.Basis = BasisUserDirect
		d.UserRef = in.UserRef
	case ActorSession:
		rule, err := s.delegationRule(ctx, a, in.WorkspaceID, actor)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.UserRef) == "" {
			return nil, fmt.Errorf("%w: user_ref is required for a delegated decision", ErrInvalidInput)
		}
		d.Basis, d.DelegationRuleID, d.UserRef = BasisDeskDelegate, &rule.ID, in.UserRef
	default:
		return nil, &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	return s.store.Transition(ctx, a.ID, StatusPending, in.Decision, actor.ID, in.Note, d)
}

func (s *Service) delegationRule(ctx context.Context, a *Approval, ws uuid.UUID, actor Actor) (*DelegationRule, error) {
	if !Delegable(a.Kind, a.WithinDirection) {
		return nil, &DeniedError{Reason: ReasonKindNotDelegable}
	}
	// Defense in depth: a session account id is never an email. A person's id
	// here means a person's token reached the session path; refuse it even if a
	// rule somehow names it.
	if ValidatePrincipal(actor.ID) != nil {
		return nil, &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	if a.RequestedBy == actor.ID {
		return nil, &DeniedError{Reason: ReasonSelfApproval}
	}
	if s.rules == nil || ws == uuid.Nil {
		return nil, &DeniedError{Reason: ReasonNoMatchingRule}
	}
	rules, err := s.rules.ListByPrincipal(ctx, ws, actor.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, r := range rules {
		if r.Active(now) && r.matches(a) {
			return r, nil
		}
	}
	return nil, &DeniedError{Reason: ReasonNoMatchingRule}
}
